// The spinning detector (DESIGN.md 6.17): an agent that keeps calling tools without getting
// anywhere looks healthy to every other check (it is working, it makes tool calls, it never ends
// a turn), and burns money. This flags it stuck: spinning, explains why on the card, and asks the
// agent once for a changed hypothesis.
//
// It is deliberately conservative: a false alarm sends Jeff to look at a healthy agent. Over the
// last SpinWindow tool calls (at least SpinMinSpan apart from first to last) the agent counts as
// spinning only when all of these hold:
//
//   - few different calls: at most a third of the calls are distinct (tool + normalised input);
//   - nothing new lately: the second half of the window brings fewer than three calls not seen
//     in the first half (an agent exploring is not spinning);
//   - nothing changed: no edit or write with new content, no commit, and the working tree's
//     fingerprint (sampled by crowsnest) has not changed during the window;
//   - the failing tests did not change: the set of failing test names in the window's results is
//     the same in both halves.
//
// It reads the job's own transcript (Claude Code's JSONL), so it is tested against recorded
// transcripts in testdata/spinning. Codex jobs have no transcript crowsnest reads: they are
// bounded by time and turns instead (6.16).

package jobs

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/landing"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/transcript"
)

const (
	// SpinWindow is how many recent tool calls the detector looks at.
	SpinWindow = 30
	// SpinMinSpan is the least time those calls must span.
	SpinMinSpan = 10 * time.Minute
	// spinDistinctShare is the largest share of distinct calls that still counts as repeating.
	spinDistinctShare = 1.0 / 3
	// spinNovelCalls is how many new calls in the second half mean the agent is exploring.
	spinNovelCalls = 3
	// spinEvery is how often one job's transcript is read for it.
	spinEvery = time.Minute
	// spinReadBytes is how much of the transcript's end is read.
	spinReadBytes = 1 << 20
)

// Step is one tool call and what came back, as the detector sees it.
type Step struct {
	At      time.Time
	Tool    string
	Sig     string   // tool + normalised input
	Changes string   // a file change or a commit: its own signature (path + content); "" for none
	Failing []string // failing test names found in the result
	IsError bool
}

// jobWatch is the supervisor's memory of one job between ticks.
type jobWatch struct {
	budgetAt    time.Time
	receiptAt   time.Time
	feedbackAt  time.Time // the last failed attempt to type pending feedback (R6-C07)
	checkReruns int       // check runs in a row voided by a moving HEAD (R6-S8)
	heldNoteAt  time.Time // the last "an automatic prompt waits for a hold" journal line (R7-C04)
	spinAt      time.Time
	spinSize    int64
}

func (s *Service) watchOf(id string) *jobWatch {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	if s.watch == nil {
		s.watch = map[string]*jobWatch{}
	}
	watch := s.watch[id]
	if watch == nil {
		watch = &jobWatch{}
		s.watch[id] = watch
	}
	return watch
}

// transcriptLine is the part of a Claude Code transcript line the detector reads.
type transcriptLine struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Message   struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type contentItem struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	Text      string          `json:"text"`
}

// ParseSteps reads tool calls and their results from a Claude Code transcript (JSONL), in order.
// Lines it does not understand are skipped.
func ParseSteps(r io.Reader) []Step {
	var steps []Step
	index := map[string]int{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	for scanner.Scan() {
		var line transcriptLine
		if json.Unmarshal(scanner.Bytes(), &line) != nil || len(line.Message.Content) == 0 || line.Message.Content[0] != '[' {
			continue
		}
		var items []contentItem
		if json.Unmarshal(line.Message.Content, &items) != nil {
			continue
		}
		for _, item := range items {
			switch {
			case line.Type == "assistant" && item.Type == "tool_use":
				step := Step{At: line.Timestamp, Tool: item.Name, Sig: item.Name + " " + normaliseInput(item.Input)}
				step.Changes = changeSignature(item.Name, item.Input)
				index[item.ID] = len(steps)
				steps = append(steps, step)
			case line.Type == "user" && item.Type == "tool_result":
				i, ok := index[item.ToolUseID]
				if !ok {
					continue
				}
				result := resultText(item.Content)
				steps[i].IsError = item.IsError || looksLikeError(result)
				steps[i].Failing = failingTests(result)
				if steps[i].IsError && steps[i].Tool != "Bash" {
					steps[i].Changes = "" // an edit that failed changed nothing
				}
			}
		}
	}
	return steps
}

var (
	digitRun   = regexp.MustCompile(`[0-9]{4,}`)
	hexRun     = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
	spaceRun   = regexp.MustCompile(`\s+`)
	commitCall = regexp.MustCompile(`\bgit\s+(-C\s+\S+\s+)?commit\b`)
	errorWords = regexp.MustCompile(`(?im)^(error|fatal|panic)[: ]|\bexit (status|code) [1-9]|^FAIL\b|command not found|no such file`)
	goFail     = regexp.MustCompile(`(?m)^\s*--- FAIL: (\S+)`)
	jsFail     = regexp.MustCompile(`(?m)^\s*(?:FAIL|✗|×|✕)\s+(\S+(?:\s+>\s+[^\n]+)?)`)
	pyFail     = regexp.MustCompile(`(?m)^FAILED\s+(\S+)`)
)

// normaliseInput reduces a tool input to what makes two calls "the same": hashes, long numbers
// (ids, timestamps) and whitespace do not count; short numbers do (pkg3 and pkg14 differ).
func normaliseInput(input json.RawMessage) string {
	var fields map[string]any
	if json.Unmarshal(input, &fields) != nil {
		return ""
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		if key == "description" || key == "timeout" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		value := fmt.Sprint(fields[key])
		value = hexRun.ReplaceAllString(value, "#")
		value = digitRun.ReplaceAllString(value, "0")
		value = spaceRun.ReplaceAllString(value, " ")
		if len(value) > 400 {
			value = value[:400]
		}
		fmt.Fprintf(&b, "%s=%s;", key, value)
	}
	return b.String()
}

// changeSignature is a call's change to the tree (path and new content, or a commit), "" when
// the call changes nothing.
func changeSignature(tool string, input json.RawMessage) string {
	var fields map[string]any
	if json.Unmarshal(input, &fields) != nil {
		return ""
	}
	switch tool {
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		body, _ := json.Marshal(fields)
		sum := sha256.Sum256(body)
		return tool + ":" + hex.EncodeToString(sum[:8])
	case "Bash":
		if command, ok := fields["command"].(string); ok && commitCall.MatchString(command) {
			return "commit:" + command
		}
	}
	return ""
}

func resultText(content json.RawMessage) string {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	var items []contentItem
	if json.Unmarshal(content, &items) == nil {
		var b strings.Builder
		for _, item := range items {
			b.WriteString(item.Text)
			b.WriteString("\n")
		}
		return b.String()
	}
	return ""
}

func looksLikeError(result string) bool { return errorWords.MatchString(result) }

// failingTests finds failing test names in a tool result (go test, Jest/Vitest, pytest).
func failingTests(result string) []string {
	seen := map[string]bool{}
	var out []string
	for _, pattern := range []*regexp.Regexp{goFail, jsFail, pyFail} {
		for _, match := range pattern.FindAllStringSubmatch(result, 50) {
			name := strings.TrimSpace(match[1])
			if name != "" && !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}

// SpinVerdict is the detector's answer, with its evidence.
type SpinVerdict struct {
	Spinning bool
	Why      string // the evidence in words (also when it is not spinning: why not)
}

// DetectSpinning applies the rule to steps (oldest first). treeChangedAt is when crowsnest last
// saw the working tree change (zero: unknown).
func DetectSpinning(steps []Step, treeChangedAt time.Time) SpinVerdict {
	if len(steps) < SpinWindow {
		return SpinVerdict{Why: fmt.Sprintf("only %d tool calls so far", len(steps))}
	}
	window := steps[len(steps)-SpinWindow:]
	first, last := window[0].At, window[len(window)-1].At
	span := last.Sub(first)
	if first.IsZero() || span < SpinMinSpan {
		return SpinVerdict{Why: fmt.Sprintf("the last %d calls span %s", SpinWindow, span.Round(time.Second))}
	}
	if !treeChangedAt.IsZero() && treeChangedAt.After(first) {
		return SpinVerdict{Why: "the working tree changed during the window"}
	}
	half := len(window) / 2
	counts := map[string]int{}
	early := map[string]bool{}
	changes := map[string]int{}
	var earlyFailing, lateFailing []string
	for i, step := range window {
		counts[step.Sig]++
		if i < half {
			early[step.Sig] = true
		}
		if step.Changes != "" {
			changes[step.Changes]++
		}
		if len(step.Failing) > 0 {
			if i < half {
				earlyFailing = step.Failing
			} else {
				lateFailing = step.Failing
			}
		}
	}
	for signature, n := range changes {
		if n == 1 {
			return SpinVerdict{Why: "it changed a file or committed (" + strings.SplitN(signature, ":", 2)[0] + ")"}
		}
	}
	if earlyFailing != nil && lateFailing != nil && strings.Join(earlyFailing, ",") != strings.Join(lateFailing, ",") {
		return SpinVerdict{Why: "the failing tests changed"}
	}
	novel := 0
	for _, step := range window[half:] {
		if !early[step.Sig] {
			novel++
			early[step.Sig] = true
		}
	}
	if novel >= spinNovelCalls {
		return SpinVerdict{Why: fmt.Sprintf("%d new kinds of call lately: it is exploring", novel)}
	}
	if float64(len(counts)) > float64(len(window))*spinDistinctShare {
		return SpinVerdict{Why: fmt.Sprintf("%d different calls out of %d", len(counts), len(window))}
	}
	top, topCount := "", 0
	for signature, n := range counts {
		if n > topCount || (n == topCount && signature < top) {
			top, topCount = signature, n
		}
	}
	why := fmt.Sprintf("%d tool calls in %d minutes, only %d different (the commonest, %d times: %s), no file changed and nothing committed",
		len(window), int(span.Minutes()), len(counts), topCount, oneLine(top, 80))
	if failing := firstNonNil(lateFailing, earlyFailing); len(failing) > 0 {
		why += fmt.Sprintf(", the same failing tests (%s)", strings.Join(capList(failing, 3), ", "))
	}
	return SpinVerdict{Spinning: true, Why: why}
}

func firstNonNil(lists ...[]string) []string {
	for _, list := range lists {
		if list != nil {
			return list
		}
	}
	return nil
}

// ---- The supervisor's side ----------------------------------------------------------------

// treeFingerprint is the working tree's state: HEAD and the porcelain status with each dirty
// file's size and time, hashed.
func treeFingerprint(ctx context.Context, dir string) string {
	snap, err := landing.Snap(ctx, dir, "", "")
	if err != nil {
		return ""
	}
	sum := sha256.New()
	sum.Write([]byte(snap.Head))
	for _, line := range snap.Dirty {
		sum.Write([]byte(line))
		if len(line) > 3 {
			if info, err := os.Stat(dir + "/" + strings.TrimSpace(line[3:])); err == nil {
				_, _ = fmt.Fprintf(sum, "%d%d", info.Size(), info.ModTime().UnixNano())
			}
		}
	}
	return hex.EncodeToString(sum.Sum(nil)[:12])
}

// tickSpinning samples a running job's tree and, at most once a minute, reads its transcript for
// the spinning rule.
func (s *Service) tickSpinning(ctx context.Context, r store.JobRecord) {
	if State(r.State) != StateRunning || r.Engine != EngineClaude || IsStanding(r.AgentName) || s.opts.Launch.ClaudeConfigDir == "" || r.ClaudeSessionID == "" {
		return
	}
	if r.Stuck && Cause(r.Cause) != CauseSpinning {
		return
	}
	id := FormatID(r.ID)
	watch := s.watchOf(id)
	now := s.opts.Now()
	if !watch.spinAt.IsZero() && now.Sub(watch.spinAt) < spinEvery {
		return
	}
	watch.spinAt = now
	dir := workDirOf(r)
	treeChangedAt := r.TreeChangedAt
	if fingerprint := treeFingerprint(ctx, dir); fingerprint != "" && fingerprint != r.TreeHash {
		treeChangedAt = now
		_, _ = s.mutate(ctx, id, func(rec *store.JobRecord, _ *change) error {
			rec.TreeHash, rec.TreeChangedAt = fingerprint, now
			return nil
		})
	}
	path, err := transcript.Locate(s.opts.Launch.ClaudeConfigDir, dir, r.ClaudeSessionID)
	if err != nil {
		return
	}
	info, err := os.Stat(path)
	if err != nil || (info.Size() == watch.spinSize && (!r.Stuck || Cause(r.Cause) != CauseSpinning)) {
		return
	}
	watch.spinSize = info.Size()
	verdict := DetectSpinning(ParseSteps(bytes.NewReader(readTail(path, spinReadBytes))), treeChangedAt)
	s.applySpinning(ctx, id, verdict)
}

// readTail reads the last n bytes of a file, from the first whole line.
func readTail(path string, n int64) []byte {
	file, err := os.Open(path) //nolint:gosec // the job's own transcript, located by crowsnest
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil
	}
	offset := max(info.Size()-n, 0)
	body := make([]byte, info.Size()-offset)
	if _, err := file.ReadAt(body, offset); err != nil && err != io.EOF {
		return nil
	}
	if offset > 0 {
		if i := bytes.IndexByte(body, '\n'); i >= 0 {
			body = body[i+1:]
		}
	}
	return body
}

// applySpinning flags or clears stuck: spinning, telling the agent and the human once.
func (s *Service) applySpinning(ctx context.Context, id string, verdict SpinVerdict) {
	message := ""
	job, err := s.mutate(ctx, id, func(r *store.JobRecord, c *change) error {
		spinning := r.Stuck && Cause(r.Cause) == CauseSpinning
		switch {
		case verdict.Spinning && !spinning && !r.Stuck && State(r.State) == StateRunning:
			r.Stuck, r.Cause, r.CauseDetail = true, string(CauseSpinning), verdict.Why
			c.event(EventStuck, "Stuck: spinning: "+verdict.Why, nil)
			c.notify = fmt.Sprintf("crowsnest: job %s is busy but not getting anywhere: %s", FormatID(r.ID), oneLine(r.Title, 80))
			message = fmt.Sprintf("[crowsnest] Job %s looks stuck in a loop: %s. Step back: say in job_update what you believe is wrong and what evidence would change your mind, "+
				"then try something different. If you cannot get further, call job_update with state blocked, or ask the human.", FormatID(r.ID), verdict.Why)
		case !verdict.Spinning && spinning:
			r.Stuck, r.Cause, r.CauseDetail = false, "", ""
			c.event(EventHook, "No longer spinning: "+verdict.Why, nil)
		default:
			return errSkip
		}
		return nil
	})
	if err != nil || message == "" {
		return
	}
	s.typeContinuation(ctx, job, "the spinning nudge", message)
}
