// Reports: the spend breakdown for a range of local days, grouped by job, model or day, and the
// header's "today" and "this week" (DESIGN.md 7.11). Everything is summed from spend_daily and
// priced when read.

package spend

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

// Groupings for a report.
const (
	GroupJob   = "job"
	GroupModel = "model"
	GroupDay   = "day"
)

// Note is the sentence every spend view carries (decision 62).
const Note = "Estimated API-equivalent cost from the agents' transcripts at list prices. Subscription plans are not billed per token, so this is not a bill."

// Row is one line of a report.
type Row struct {
	Key   string `json:"key"`   // the job id, the orchestrator, the model or the day
	Label string `json:"label"` // the job's title, "Orchestrator", the model, the day
	Summary
}

// Report is the breakdown for a range.
type Report struct {
	From    string `json:"from"` // YYYY-MM-DD, inclusive
	To      string `json:"to"`
	Group   string `json:"group"`
	Rows    []Row  `json:"rows"`
	Total   Row    `json:"total"`
	Checked string `json:"prices_checked"`
	Source  string `json:"prices_source"`
	// PricesFile is the override in use, if any.
	PricesFile string `json:"prices_file,omitempty"`
	Note       string `json:"note"`
}

// LabelFunc names a subject (a job's title); "" leaves the subject id as the label.
type LabelFunc func(ctx context.Context, subjects []string) map[string]string

// Report sums the days from..to (inclusive) by group.
func (r *Reader) Report(ctx context.Context, from, to, group string, labels LabelFunc) (Report, error) {
	if group == "" {
		group = GroupJob
	}
	if group != GroupJob && group != GroupModel && group != GroupDay {
		return Report{}, fmt.Errorf("group must be job, model or day, not %q", group)
	}
	rows, err := r.Store.SpendDailyRange(ctx, from, to)
	if err != nil {
		return Report{}, err
	}
	report := Report{From: from, To: to, Group: group, Rows: []Row{}, Checked: r.Prices.Checked(), Source: r.Prices.Source(),
		PricesFile: r.Prices.Override, Note: Note}
	type bucket struct {
		counts store.SpendCounts
		cost   costAccumulator
	}
	buckets := map[string]*bucket{}
	var total bucket
	for _, row := range rows {
		key := row.Subject
		switch group {
		case GroupModel:
			key = ModelOf(row.Model)
		case GroupDay:
			key = row.Day
		}
		if buckets[key] == nil {
			buckets[key] = &bucket{}
		}
		buckets[key].counts = buckets[key].counts.Add(row.SpendCounts)
		buckets[key].cost.add(r.Prices, row.Model, row.SpendCounts)
		total.counts = total.counts.Add(row.SpendCounts)
		total.cost.add(r.Prices, row.Model, row.SpendCounts)
	}
	names := map[string]string{}
	if group == GroupJob && labels != nil {
		keys := make([]string, 0, len(buckets))
		for key := range buckets {
			keys = append(keys, key)
		}
		names = labels(ctx, keys)
	}
	for key, b := range buckets {
		row := Row{Key: key, Label: key, Summary: Summary{Measured: true}}
		if name := names[key]; name != "" {
			row.Label = name
		}
		if key == OrchestratorSubject && group == GroupJob {
			row.Label = "Orchestrator"
		}
		fillCounts(&row.Summary, b.counts)
		b.cost.apply(&row.Summary)
		report.Rows = append(report.Rows, row)
	}
	sortRows(report.Rows, group)
	report.Total = Row{Key: "total", Label: "Total", Summary: Summary{Measured: true}}
	fillCounts(&report.Total.Summary, total.counts)
	total.cost.apply(&report.Total.Summary)
	return report, nil
}

// sortRows orders days by date and everything else by cost, then tokens, largest first.
func sortRows(rows []Row, group string) {
	sort.Slice(rows, func(i, j int) bool {
		if group == GroupDay {
			return rows[i].Key < rows[j].Key
		}
		ci, cj := costOf(rows[i].Summary), costOf(rows[j].Summary)
		if ci != cj {
			return ci > cj
		}
		if rows[i].TotalTokens != rows[j].TotalTokens {
			return rows[i].TotalTokens > rows[j].TotalTokens
		}
		return rows[i].Key < rows[j].Key
	})
}

func costOf(s Summary) float64 {
	if s.CostUSD == nil {
		return -1
	}
	return *s.CostUSD
}

// Period is the header's quick view: today, this week, and the orchestrator's share of today.
type Period struct {
	Today        Row    `json:"today"`
	Week         Row    `json:"week"`
	Orchestrator Row    `json:"orchestrator_today"`
	TodayDate    string `json:"today_date"`
	WeekStart    string `json:"week_start"` // the Monday this week began on, local
	Checked      string `json:"prices_checked"`
	Note         string `json:"note"`
}

// Period returns today's and this week's totals in loc (weeks start on Monday).
func (r *Reader) Period(ctx context.Context, loc *time.Location) (Period, error) {
	now := r.now().In(loc)
	today := now.Format(time.DateOnly)
	weekStart := WeekStart(now).Format(time.DateOnly)
	week, err := r.Report(ctx, weekStart, today, GroupJob, nil)
	if err != nil {
		return Period{}, err
	}
	day, err := r.Report(ctx, today, today, GroupJob, nil)
	if err != nil {
		return Period{}, err
	}
	period := Period{Today: day.Total, Week: week.Total, TodayDate: today, WeekStart: weekStart, Checked: r.Prices.Checked(), Note: Note}
	period.Today.Label, period.Week.Label = "Today", "This week"
	period.Orchestrator = Row{Key: OrchestratorSubject, Label: "Orchestrator", Summary: Summary{Measured: true, Models: []string{}}}
	for _, row := range day.Rows {
		if row.Key == OrchestratorSubject {
			period.Orchestrator = row
		}
	}
	return period, nil
}

// WeekStart is the Monday (00:00) of t's week, in t's location.
func WeekStart(t time.Time) time.Time {
	offset := (int(t.Weekday()) + 6) % 7 // Monday is 0
	day := t.AddDate(0, 0, -offset)
	return time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, t.Location())
}
