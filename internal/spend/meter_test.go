package spend

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/transcript"
)

// The golden transcript (testdata/projects/-work-repo/sess-1.jsonl and its subagent file) has, in
// the shapes Claude Code 2.1.332 writes:
//
//	msg_A  opus-5-5, three records (thinking, text, tool_use); the first copy has fewer output
//	       tokens; 1-hour cache write; 23:58 UTC on the 25th
//	msg_B  opus-5-5, two tool_use blocks in one record (one is a Task), cache write without the
//	       5m/1h split; just after midnight UTC
//	msg_S  an old-style sidechain record inline, dated haiku-4-5 id
//	msg_X  <synthetic>: skipped
//	msg_C  opus-5-5 in fast mode
//	msg_D  haiku-5-5 in the subagent file, twice
//	msg_E  haiku-5-5 with a 120k prompt: the long-context tier
var goldenTotals = store.SpendCounts{Input: 120112, Output: 580, CacheWrite5m: 6000, CacheWrite1h: 20000, CacheRead: 41000, Turns: 6, ToolCalls: 4}

const goldenCostUSD = 0.2484782

func testMeter(t *testing.T, db *store.Store, configDir string, loc *time.Location, sources ...Source) *Meter {
	t.Helper()
	if loc == nil {
		loc = time.UTC
	}
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	return NewMeter(Options{
		Store: db, Prices: Embedded(), ClaudeConfigDir: configDir, Location: loc, Now: func() time.Time { return now },
		Sources: func(context.Context) ([]Source, error) { return sources, nil },
	})
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// copyGolden copies testdata/projects into a temporary Claude config dir so tests can append.
func copyGolden(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	err := filepath.Walk("testdata/projects", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dir, "projects", strings.TrimPrefix(path, "testdata/projects"))
		if info.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func assertCost(t *testing.T, summary Summary, want float64) {
	t.Helper()
	if summary.CostUSD == nil || math.Abs(*summary.CostUSD-want) > 1e-9 {
		t.Fatalf("cost = %v, want %v", summary.CostUSD, want)
	}
}

func TestMeterGoldenTranscript(t *testing.T) {
	ctx := context.Background()
	db := openStore(t)
	meter := testMeter(t, db, copyGolden(t), nil, Source{Subject: "j_1", Sessions: []Session{{ID: "sess-1", Cwd: "/work/repo"}}})
	if !meter.Poll(ctx) {
		t.Fatal("first poll changed nothing")
	}
	totals, err := db.GetSpendTotals(ctx, "j_1")
	if err != nil {
		t.Fatal(err)
	}
	if totals.SpendCounts != goldenTotals {
		t.Fatalf("totals\n got %+v\nwant %+v", totals.SpendCounts, goldenTotals)
	}
	summary, err := meter.SpendOf(ctx, JobInfo{ID: "j_1", Engine: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	assertCost(t, summary, goldenCostUSD)
	if want := []string{"claude-haiku-4-5", "claude-haiku-5-5", "claude-opus-5-5"}; strings.Join(summary.Models, ",") != strings.Join(want, ",") {
		t.Fatalf("models = %v", summary.Models)
	}
	if summary.CacheWriteTokens != 26000 || summary.CacheHitShare <= 0.2 || summary.CacheHitShare >= 0.3 {
		t.Fatalf("cache: %+v", summary)
	}
	// A second poll with nothing new adds nothing.
	if meter.Poll(ctx) {
		t.Fatal("second poll changed something")
	}
	days, err := db.SpendDailyRange(ctx, "2026-11-25", "2026-11-25")
	if err != nil || len(days) != 1 || days[0].Output != 120 || days[0].Model != "claude-opus-5-5" {
		t.Fatalf("the 25th: %+v %v", days, err)
	}
	keys := map[string]bool{}
	all, _ := db.SpendDailyRange(ctx, "2026-11-26", "2026-11-26")
	for _, row := range all {
		keys[row.Model] = true
	}
	for _, key := range []string{"claude-opus-5-5", "claude-opus-5-5+fast", "claude-haiku-4-5", "claude-haiku-5-5", "claude-haiku-5-5+long"} {
		if !keys[key] {
			t.Errorf("no daily row for %s: %v", key, keys)
		}
	}
	turns, err := meter.Turns(ctx, "j_1")
	if err != nil || len(turns) != 6 {
		t.Fatalf("turns: %d %v", len(turns), err)
	}
}

// Appending a larger copy of a call already counted (in a later read) adds only the difference;
// a partial last line waits for its newline.
func TestMeterIncrementalAndDuplicatesAcrossReads(t *testing.T) {
	ctx := context.Background()
	db := openStore(t)
	dir := copyGolden(t)
	meter := testMeter(t, db, dir, nil, Source{Subject: "j_1", Sessions: []Session{{ID: "sess-1", Cwd: "/work/repo"}}})
	meter.Poll(ctx)
	path := filepath.Join(dir, "projects", "-work-repo", "sess-1.jsonl")
	appendTo(t, path, `{"type":"assistant","timestamp":"2026-11-26T00:03:01Z","message":{"id":"msg_C","model":"claude-opus-5-5","content":[{"type":"text","text":"more"}],"usage":{"input_tokens":2,"output_tokens":100,"cache_read_input_tokens":21000,"speed":"fast"}}}`+"\n"+
		`{"type":"assistant","timestamp":"2026-11-26T00:05:00Z","message":{"id":"msg_F","model":"claude-opus-5-5","content":[],"usage":{"input_tokens":1,"output_tokens":`)
	meter.Poll(ctx)
	totals, _ := db.GetSpendTotals(ctx, "j_1")
	if totals.Output != goldenTotals.Output+60 || totals.Turns != goldenTotals.Turns {
		t.Fatalf("after a larger copy: %+v", totals.SpendCounts)
	}
	appendTo(t, path, `7}}}`+"\n")
	meter.Poll(ctx)
	totals, _ = db.GetSpendTotals(ctx, "j_1")
	if totals.Output != goldenTotals.Output+67 || totals.Turns != goldenTotals.Turns+1 {
		t.Fatalf("after the partial line completed: %+v", totals.SpendCounts)
	}
}

// A job restarted under a new session id keeps its first session's spend and adds the new one's;
// a crowsnest restart (a new Meter over the same store) resumes from the stored cursors.
func TestMeterSessionChangeAndRestart(t *testing.T) {
	ctx := context.Background()
	db := openStore(t)
	dir := copyGolden(t)
	project := filepath.Join(dir, "projects", "-work-repo")
	meter := testMeter(t, db, dir, nil, Source{Subject: "j_1", Sessions: []Session{{ID: "sess-1", Cwd: "/work/repo"}}})
	meter.Poll(ctx)

	appendTo(t, filepath.Join(project, "sess-2.jsonl"),
		`{"type":"assistant","timestamp":"2026-11-26T01:00:00Z","message":{"id":"msg_N","model":"claude-opus-5-5","content":[],"usage":{"input_tokens":10,"output_tokens":20}}}`+"\n")
	// The source now names only the new session, as the job record does after a restart.
	restarted := testMeter(t, db, dir, nil, Source{Subject: "j_1", Sessions: []Session{{ID: "sess-2", Cwd: "/work/repo"}}})
	restarted.Poll(ctx)
	totals, _ := db.GetSpendTotals(ctx, "j_1")
	if totals.Input != goldenTotals.Input+10 || totals.Turns != goldenTotals.Turns+1 {
		t.Fatalf("after a new session in a new process: %+v", totals.SpendCounts)
	}
	// The old session's file still grows (a late subagent record) and is still read.
	appendTo(t, filepath.Join(project, "sess-1", "subagents", "agent-a1.jsonl"),
		`{"isSidechain":true,"type":"assistant","timestamp":"2026-11-26T01:01:00Z","message":{"id":"msg_L","model":"claude-haiku-5-5","content":[],"usage":{"input_tokens":1,"output_tokens":1}}}`+"\n")
	restarted.Poll(ctx)
	totals, _ = db.GetSpendTotals(ctx, "j_1")
	if totals.Turns != goldenTotals.Turns+2 {
		t.Fatalf("old session's late record: %+v", totals.SpendCounts)
	}
	// Deleting the transcripts (rotation) keeps what was counted.
	if err := os.RemoveAll(project); err != nil {
		t.Fatal(err)
	}
	restarted.Poll(ctx)
	after, _ := db.GetSpendTotals(ctx, "j_1")
	if after.SpendCounts != totals.SpendCounts {
		t.Fatalf("totals changed after the files went: %+v", after.SpendCounts)
	}
}

// Rollups follow the local calendar: two calls either side of local midnight land on two days,
// whatever UTC says.
func TestMeterRollupAcrossLocalMidnight(t *testing.T) {
	ctx := context.Background()
	db := openStore(t)
	// A fixed UTC-7 zone, not a named one: the tz database's idea of Alberta's offset changes.
	mountain := time.FixedZone("UTC-7", -7*3600)
	meter := testMeter(t, db, t.TempDir(), mountain)
	// 06:59 and 07:01 UTC on the 26th are 23:59 on the 25th and 00:01 on the 26th at UTC-7.
	err := meter.Record(ctx, "j_2", []transcript.UsageEntry{
		{MessageID: "m1", Model: "claude-sonnet-5-5", At: time.Date(2026, 11, 26, 6, 59, 0, 0, time.UTC), Input: 1000, Output: 100},
		{MessageID: "m2", Model: "claude-sonnet-5-5", At: time.Date(2026, 11, 26, 7, 1, 0, 0, time.UTC), Input: 2000, Output: 200},
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := meter.Report(ctx, "2026-11-25", "2026-11-26", GroupDay, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Rows) != 2 || report.Rows[0].Key != "2026-11-25" || report.Rows[0].InputTokens != 1000 || report.Rows[1].InputTokens != 2000 {
		t.Fatalf("rows: %+v", report.Rows)
	}
	// $2 / $10 per million: 3000 in and 300 out.
	assertCost(t, report.Total.Summary, 0.009)
}

// A settled job is read once more after the grace period and then never again.
func TestMeterFinalAfterSettled(t *testing.T) {
	ctx := context.Background()
	db := openStore(t)
	dir := copyGolden(t)
	settled := time.Date(2026, 11, 26, 11, 0, 0, 0, time.UTC) // an hour before the meter's now
	meter := testMeter(t, db, dir, nil, Source{Subject: "j_1", Sessions: []Session{{ID: "sess-1"}}, Settled: &settled})
	meter.Poll(ctx)
	appendTo(t, filepath.Join(dir, "projects", "-work-repo", "sess-1.jsonl"),
		`{"type":"assistant","timestamp":"2026-11-26T11:30:00Z","message":{"id":"msg_Z","model":"claude-opus-5-5","content":[],"usage":{"input_tokens":1,"output_tokens":1}}}`+"\n")
	if meter.Poll(ctx) {
		t.Fatal("a final subject was read again")
	}
	totals, _ := db.GetSpendTotals(ctx, "j_1")
	if totals.SpendCounts != goldenTotals || !strings.Contains(totals.StateJSON, `"final":true`) {
		t.Fatalf("final: %+v %s", totals.SpendCounts, totals.StateJSON)
	}
}

// Codex and orchestrator-held jobs are "not measured", never zero; an unknown model has tokens
// and no cost; a mix is partial.
func TestSummariesNotMeasuredAndUnknownModels(t *testing.T) {
	ctx := context.Background()
	db := openStore(t)
	meter := testMeter(t, db, t.TempDir(), nil)
	if err := meter.Record(ctx, "j_3", []transcript.UsageEntry{{MessageID: "m", Model: "gpt-9", Input: 500, Output: 5}}); err != nil {
		t.Fatal(err)
	}
	if err := meter.Record(ctx, "j_4", []transcript.UsageEntry{
		{MessageID: "m1", Model: "gpt-9", Input: 500}, {MessageID: "m2", Model: "sonnet", Input: 1_000_000},
	}); err != nil {
		t.Fatal(err)
	}
	summaries, err := meter.Summaries(ctx, []JobInfo{
		{ID: "j_1", Engine: "codex"}, {ID: "j_2", Engine: "claude", Orchestrated: true},
		{ID: "j_3", Engine: "claude"}, {ID: "j_4", Engine: "claude"}, {ID: "j_5", Engine: "claude"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if s := summaries["j_1"]; s.Measured || s.Reason != ReasonCodex {
		t.Errorf("codex: %+v", s)
	}
	if s := summaries["j_2"]; s.Measured || s.Reason != ReasonOrchestrator {
		t.Errorf("orchestrated: %+v", s)
	}
	if s := summaries["j_3"]; !s.Measured || s.CostUSD != nil || s.TotalTokens != 505 || s.UnpricedTokens != 505 {
		t.Errorf("unknown model: %+v", s)
	}
	if s := summaries["j_4"]; !s.CostPartial || s.CostUSD == nil || *s.CostUSD != 2 || s.UnpricedTokens != 500 {
		t.Errorf("partial: %+v", s)
	}
	if s := summaries["j_5"]; !s.Measured || s.TotalTokens != 0 || s.CostUSD != nil {
		t.Errorf("never measured: %+v", s)
	}
}

func appendTo(t *testing.T, path, text string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.WriteString(text); err != nil {
		t.Fatal(err)
	}
}
