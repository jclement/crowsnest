// Spend from the browser (DESIGN.md 7.11): the breakdown for a range of local days grouped by
// job, model or day (GET /spend), and the header's today and this week with the orchestrator's
// line (GET /spend/summary). A job's own spend rides on GET /jobs and /jobs/{id}. Every figure is
// an estimate; each response carries the price table's date and the note saying so.

package httpd

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/spend"
)

// maxSpendRangeDays bounds one report: a year of rollups is a few thousand rows at most.
const maxSpendRangeDays = 366

func (s *Server) registerSpendRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/spend", s.requireAuth(http.HandlerFunc(s.handleSpendReport)))
	mux.Handle("GET /api/v1/spend/summary", s.requireAuth(http.HandlerFunc(s.handleSpendSummary)))
}

func (s *Server) spendMeter(w http.ResponseWriter) *spend.Meter {
	if s.deps.Spend == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "Spend is not measured on this server.")
	}
	return s.deps.Spend
}

// handleSpendReport serves GET /spend?from=YYYY-MM-DD&to=YYYY-MM-DD&group=job|model|day. Both
// dates are local and inclusive; they default to this week (Monday) and today.
func (s *Server) handleSpendReport(w http.ResponseWriter, r *http.Request) {
	meter := s.spendMeter(w)
	if meter == nil {
		return
	}
	query := r.URL.Query()
	now := meter.Now().In(meter.Location())
	from, to := query.Get("from"), query.Get("to")
	if from == "" {
		from = spend.WeekStart(now).Format(time.DateOnly)
	}
	if to == "" {
		to = now.Format(time.DateOnly)
	}
	start, errFrom := time.Parse(time.DateOnly, from)
	end, errTo := time.Parse(time.DateOnly, to)
	if errFrom != nil || errTo != nil || end.Before(start) || end.Sub(start) > maxSpendRangeDays*24*time.Hour {
		writeError(w, http.StatusBadRequest, codeValidation, "Give from and to as YYYY-MM-DD, from no later than to, at most a year apart.")
		return
	}
	group := query.Get("group")
	if group != "" && group != spend.GroupJob && group != spend.GroupModel && group != spend.GroupDay {
		writeError(w, http.StatusBadRequest, codeValidation, "group is job, model or day.")
		return
	}
	report, err := meter.Report(r.Context(), from, to, group, s.jobTitles)
	if err != nil {
		s.deps.Logger.Error("spend report failed", "err", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "Could not read spend; see the server log.")
		return
	}
	writeData(w, http.StatusOK, report)
}

// spendSummary is GET /spend/summary: the period totals and the orchestrator conversation's
// all-time line.
type spendSummary struct {
	spend.Period
	OrchestratorTotal spend.Summary `json:"orchestrator_total"`
}

func (s *Server) handleSpendSummary(w http.ResponseWriter, r *http.Request) {
	meter := s.spendMeter(w)
	if meter == nil {
		return
	}
	period, err := meter.Period(r.Context(), meter.Location())
	if err != nil {
		s.deps.Logger.Error("spend summary failed", "err", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "Could not read spend; see the server log.")
		return
	}
	orchestrator, err := meter.Orchestrator(r.Context())
	if err != nil {
		s.deps.Logger.Error("orchestrator spend failed", "err", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "Could not read spend; see the server log.")
		return
	}
	writeData(w, http.StatusOK, spendSummary{Period: period, OrchestratorTotal: orchestrator})
}

// jobTitles labels a report's job rows with the jobs' titles (the board's newest jobs; an older
// job keeps its id as the label).
func (s *Server) jobTitles(ctx context.Context, subjects []string) map[string]string {
	out := map[string]string{}
	if s.deps.Jobs == nil {
		return out
	}
	wanted := map[string]bool{}
	for _, subject := range subjects {
		if strings.HasPrefix(subject, "j_") {
			wanted[subject] = true
		}
	}
	if len(wanted) == 0 {
		return out
	}
	list, err := s.deps.Jobs.List(ctx, nil)
	if err != nil {
		return out
	}
	for _, job := range list {
		if wanted[job.ID] {
			out[job.ID] = job.Title
		}
	}
	return out
}
