// --demo spend: plausible usage for the seeded jobs and the orchestrator, written through the
// same counting path the transcripts go through (Meter.Record), so the board, the detail chart
// and the breakdown sheet have something real-shaped to show. Deterministic per job id, so
// screenshots do not change from run to run beyond the clock.

package spend

import (
	"context"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"time"

	"github.com/jclement/crowsnest/internal/transcript"
)

// DemoSubject is one subject to seed: calls spread evenly from Start to End on Model.
type DemoSubject struct {
	Subject string
	Model   string
	Start   time.Time
	End     time.Time
	Calls   int
	// Subagent adds a few haiku calls in the middle, as a Task tool would.
	Subagent bool
}

// SeedDemo records the subjects' calls; a subject may appear more than once (one stretch of work
// each). It leaves alone a subject that already had spend before it started.
func SeedDemo(ctx context.Context, meter *Meter, subjects []DemoSubject) error {
	had := map[string]bool{}
	for _, subject := range subjects {
		if existing, err := meter.opts.Store.GetSpendTotals(ctx, subject.Subject); err == nil && existing.Turns > 0 {
			had[subject.Subject] = true
		}
	}
	for _, subject := range subjects {
		if had[subject.Subject] {
			continue
		}
		if err := meter.Record(ctx, subject.Subject, demoCalls(subject)); err != nil {
			return fmt.Errorf("seeding demo spend for %s: %w", subject.Subject, err)
		}
	}
	return nil
}

// demoCalls builds a conversation's worth of calls: the context grows call by call (cache reads
// climb), each call writes a little to the cache and produces a few hundred output tokens, and
// a tool call rides on most of them.
func demoCalls(subject DemoSubject) []transcript.UsageEntry {
	seed := fnv.New64a()
	prefix := fmt.Sprintf("%s_%d", subject.Subject, subject.Start.Unix())
	_, _ = seed.Write([]byte(prefix))
	random := rand.New(rand.NewPCG(seed.Sum64(), 7)) //nolint:gosec // demo figures, not secrets
	calls := max(subject.Calls, 1)
	span := subject.End.Sub(subject.Start)
	out := make([]transcript.UsageEntry, 0, calls+4)
	context := int64(18_000)
	for i := range calls {
		at := subject.Start.Add(span * time.Duration(i) / time.Duration(calls))
		write := int64(500 + random.IntN(4000))
		entry := transcript.UsageEntry{
			MessageID: fmt.Sprintf("demo_%s_%d", prefix, i), Model: subject.Model, At: at,
			Input: int64(1 + random.IntN(6)), Output: int64(80 + random.IntN(1800)), CacheRead: context, CacheWrite5m: write,
		}
		if i == 0 {
			entry.CacheWrite5m, entry.CacheWrite1h, entry.CacheRead = 0, context, 0
		}
		if random.IntN(10) < 7 {
			entry.ToolUseIDs = []string{fmt.Sprintf("toolu_demo_%s_%d", prefix, i)}
		}
		out = append(out, entry)
		context += write
		if subject.Subagent && i == calls/2 {
			out = append(out, demoSubagent(prefix, at, random)...)
		}
	}
	return out
}

func demoSubagent(subject string, at time.Time, random *rand.Rand) []transcript.UsageEntry {
	var out []transcript.UsageEntry
	for i := range 4 {
		out = append(out, transcript.UsageEntry{
			MessageID: fmt.Sprintf("demo_%s_sub%d", subject, i), Model: "claude-haiku-5-5", At: at.Add(time.Duration(i) * 20 * time.Second),
			Input: 3, Output: int64(100 + random.IntN(400)), CacheWrite5m: 9000, CacheRead: int64(i) * 9000, Sidechain: true,
			ToolUseIDs: []string{fmt.Sprintf("toolu_demo_%s_sub%d", subject, i)},
		})
	}
	return out
}
