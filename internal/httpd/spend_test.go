// Tests for GET /spend and /spend/summary (grouping, validation, the estimate note, the
// orchestrator's line), and a job's spend on GET /jobs and /jobs/{id}.

package httpd

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/spend"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/transcript"
)

type spendRig struct {
	h      *harness
	meter  *spend.Meter
	jobs   *jobs.Service
	cookie *http.Cookie
}

func newSpendRig(t *testing.T) *spendRig {
	t.Helper()
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Date(2026, 11, 26, 15, 0, 0, 0, time.UTC) // a Thursday
	meter := spend.NewMeter(spend.Options{Store: db, Prices: spend.Embedded(), Location: time.UTC, Now: func() time.Time { return now }})
	service := jobs.New(jobs.Options{Store: db, Logger: slog.New(slog.DiscardHandler), Spend: meter, Now: func() time.Time { return now }})
	job, err := service.Create(ctx, jobs.CreateInput{Brief: "Measure me\n\nplease"}, "")
	if err != nil {
		t.Fatal(err)
	}
	record := func(subject string, entries ...transcript.UsageEntry) {
		if err := meter.Record(ctx, subject, entries); err != nil {
			t.Fatal(err)
		}
	}
	million := int64(1_000_000)
	record(job.ID, transcript.UsageEntry{MessageID: "a", Model: "claude-opus-5-5", At: now.Add(-time.Hour), Input: million, ToolUseIDs: []string{"t1"}})
	record(job.ID, transcript.UsageEntry{MessageID: "b", Model: "claude-opus-5-5", At: now.AddDate(0, 0, -2), Output: million})
	record(spend.OrchestratorSubject, transcript.UsageEntry{MessageID: "o", Model: "sonnet", At: now.Add(-2 * time.Hour), Input: million})
	h := newHarness(t, harnessOptions{jobs: service, spend: meter})
	return &spendRig{h: h, meter: meter, jobs: service, cookie: h.login()}
}

func (r *spendRig) get(t *testing.T, path string, into any) int {
	t.Helper()
	response := r.h.do(withCookie(request(http.MethodGet, path, ""), r.cookie))
	if response.Code == http.StatusOK && into != nil {
		var body struct{ Data json.RawMessage }
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body.Data, into); err != nil {
			t.Fatal(err)
		}
	}
	return response.Code
}

func TestSpendRoutesNeedASession(t *testing.T) {
	r := newSpendRig(t)
	for _, path := range []string{"/api/v1/spend", "/api/v1/spend/summary"} {
		if response := r.h.do(request(http.MethodGet, path, "")); response.Code != http.StatusUnauthorized {
			t.Errorf("%s without a session: %d", path, response.Code)
		}
	}
}

func TestSpendReport(t *testing.T) {
	r := newSpendRig(t)
	var report spend.Report
	if code := r.get(t, "/api/v1/spend?from=2026-11-23&to=2026-11-26&group=job", &report); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if len(report.Rows) != 2 || report.Rows[0].Key != "j_1" || report.Rows[0].Label != "Measure me" || report.Rows[1].Label != "Orchestrator" {
		t.Fatalf("rows: %+v", report.Rows)
	}
	// $4 in + $20 out on opus, $2 in on sonnet.
	if report.Total.CostUSD == nil || *report.Total.CostUSD != 26 || report.Note == "" || report.Checked == "" {
		t.Fatalf("total: %+v", report)
	}
	if code := r.get(t, "/api/v1/spend?from=2026-11-26&to=2026-11-26&group=model", &report); code != http.StatusOK || len(report.Rows) != 2 {
		t.Fatalf("by model today: %d %+v", code, report.Rows)
	}
	if code := r.get(t, "/api/v1/spend?group=day", &report); code != http.StatusOK || report.From != "2026-11-23" || report.To != "2026-11-26" || len(report.Rows) != 2 {
		t.Fatalf("default range by day: %+v", report)
	}
	for _, query := range []string{"?from=yesterday", "?from=2026-11-26&to=2026-11-01", "?from=2020-01-01&to=2026-01-01", "?group=week"} {
		if code := r.get(t, "/api/v1/spend"+query, nil); code != http.StatusBadRequest {
			t.Errorf("%s: %d", query, code)
		}
	}
}

func TestSpendSummaryAndJobs(t *testing.T) {
	r := newSpendRig(t)
	var summary struct {
		Today             spend.Row     `json:"today"`
		Week              spend.Row     `json:"week"`
		Orchestrator      spend.Row     `json:"orchestrator_today"`
		OrchestratorTotal spend.Summary `json:"orchestrator_total"`
		WeekStart         string        `json:"week_start"`
	}
	if code := r.get(t, "/api/v1/spend/summary", &summary); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if *summary.Today.CostUSD != 6 || *summary.Week.CostUSD != 26 || *summary.Orchestrator.CostUSD != 2 || summary.WeekStart != "2026-11-23" ||
		summary.OrchestratorTotal.InputTokens != 1_000_000 {
		t.Fatalf("summary: %+v", summary)
	}
	var list []jobs.Job
	if code := r.get(t, "/api/v1/jobs", &list); code != http.StatusOK || len(list) != 1 || list[0].Spend == nil || *list[0].Spend.CostUSD != 24 ||
		list[0].Spend.ToolCalls != 1 {
		t.Fatalf("board: %d %+v", code, list)
	}
	var detail jobs.Detail
	if code := r.get(t, "/api/v1/jobs/j_1", &detail); code != http.StatusOK || detail.Spend == nil || len(detail.SpendTurns) != 2 {
		t.Fatalf("detail: %d %+v", code, detail)
	}
	text, err := r.jobs.GetForAgent(context.Background(), "j_1")
	if err != nil || !strings.Contains(text, "## Spend so far") || !strings.Contains(text, "$24.00 est.") || !strings.Contains(text, "not billed per token") {
		t.Fatalf("job_get: %v\n%s", err, text)
	}
}
