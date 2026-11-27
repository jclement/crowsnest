package attention

import (
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/approvals"
	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/projects"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/risk"
)

var testNow = time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)

func ago(d time.Duration) time.Time { return testNow.Add(-d) }

func agoPtr(d time.Duration) *time.Time { t := ago(d); return &t }

var crow = projects.Project{ID: 1, Slug: "crowsnest", Name: "Crowsnest"}
var site = projects.Project{ID: 2, Slug: "website", Name: "Website"}

func approval(id, pane, job string, level risk.Level, at time.Duration) approvals.Approval {
	return approvals.Approval{ID: id, PaneID: pane, JobID: job, Risk: level, Status: approvals.StatusPending, CreatedAt: ago(at), Summary: "Bash: " + id}
}

func question(id, pane, job string, urgency questions.Urgency, at time.Duration) questions.Question {
	return questions.Question{ID: id, Title: "Q " + id, Kind: questions.KindChoice, Status: questions.StatusOpen, SourcePane: pane,
		JobID: job, Urgency: urgency, CreatedAt: ago(at), Recommend: "a", Options: []questions.Option{{Key: "a", Label: "Apply it"}}}
}

func dialogAgent(pane, name string) fleet.AgentEntry {
	return fleet.AgentEntry{Agent: fleet.Agent{Name: name}, PaneID: pane, NeedsYou: true}
}

func job(id string, project int64, pane string, state jobs.State, stuck bool, cause jobs.Cause) jobs.Job {
	return jobs.Job{ID: id, Title: "Job " + id, ProjectID: project, PaneID: pane, AgentName: "job-" + id[2:], State: state,
		Stuck: stuck, Cause: cause, CreatedAt: ago(time.Hour), LastActivityAt: agoPtr(20 * time.Minute)}
}

func ids(items []Item) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = item.ID
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The de-duplication table: each row is a situation and the one list it must give.
func TestBuildDeduplicates(t *testing.T) {
	cases := []struct {
		name string
		src  Sources
		want []string
	}{
		{
			name: "a job at a dialog that also has an approval is one item, the approval",
			src: Sources{
				Approvals: []approvals.Approval{approval("ap_1", "w1:p1", "j_1", risk.Low, time.Minute)},
				Agents:    []fleet.AgentEntry{dialogAgent("w1:p1", "job-1")},
				Jobs:      []jobs.Job{job("j_1", 1, "w1:p1", jobs.StateNeedsYou, false, jobs.CauseApprovalTool)},
			},
			want: []string{"approval:ap_1"},
		},
		{
			name: "a job at a dialog with no approval is one item, the dialog",
			src: Sources{
				Agents: []fleet.AgentEntry{dialogAgent("w1:p1", "job-1")},
				Jobs:   []jobs.Job{job("j_1", 1, "w1:p1", jobs.StateNeedsYou, false, jobs.CauseDialog)},
			},
			want: []string{"dialog:w1:p1"},
		},
		{
			name: "a job waiting on its questions is listed through them only",
			src: Sources{
				Questions: []questions.Question{question("q_1", "w1:p1", "j_1", questions.UrgencyNormal, time.Minute)},
				Jobs:      []jobs.Job{job("j_1", 1, "w1:p1", jobs.StateNeedsYou, false, jobs.CauseQuestions)},
			},
			want: []string{"question:q_1"},
		},
		{
			name: "a stuck job with an open question is one item, the question",
			src: Sources{
				Questions: []questions.Question{question("q_1", "w1:p1", "j_1", questions.UrgencyNormal, time.Minute)},
				Jobs:      []jobs.Job{job("j_1", 1, "w1:p1", jobs.StateRunning, true, jobs.CauseUnresponsive)},
			},
			want: []string{"question:q_1"},
		},
		{
			name: "notices never count",
			src: Sources{Questions: []questions.Question{func() questions.Question {
				q := question("q_9", "w1:p1", "", questions.UrgencyNormal, time.Minute)
				q.Kind = questions.KindNotice
				return q
			}()}},
			want: nil,
		},
		{
			name: "settled approvals and questions are not items",
			src: Sources{
				Approvals: []approvals.Approval{func() approvals.Approval {
					a := approval("ap_2", "w1:p1", "", risk.Low, time.Minute)
					a.Status = approvals.StatusAllowed
					return a
				}()},
				Questions: []questions.Question{func() questions.Question {
					q := question("q_2", "w1:p1", "", questions.UrgencyNormal, time.Minute)
					q.Status = questions.StatusAnswered
					return q
				}()},
			},
			want: nil,
		},
		{
			name: "a failed job counts until dismissed or past the window",
			src: Sources{Jobs: []jobs.Job{
				func() jobs.Job {
					j := job("j_2", 2, "", jobs.StateFailed, false, "")
					j.SettledAt = agoPtr(time.Hour)
					return j
				}(),
				func() jobs.Job {
					j := job("j_3", 2, "", jobs.StateFailed, false, "")
					j.SettledAt = agoPtr(100 * time.Hour)
					return j
				}(),
				func() jobs.Job {
					j := job("j_4", 2, "", jobs.StateFailed, false, "")
					j.SettledAt = agoPtr(time.Hour)
					return j
				}(),
			}, Dismissed: map[string]bool{"job:j_4": true}},
			want: []string{"job:j_2"},
		},
		{
			name: "done and cancelled jobs are not items; running healthy ones neither",
			src: Sources{Jobs: []jobs.Job{
				job("j_5", 1, "", jobs.StateDone, false, ""),
				job("j_6", 1, "", jobs.StateCancelled, false, ""),
				job("j_7", 1, "w1:p7", jobs.StateRunning, false, ""),
				job("j_8", 1, "", jobs.StateBlocked, false, ""),
			}},
			want: nil,
		},
		{
			name: "a job held for approval to start is listed through its question when there is one",
			src: Sources{
				Questions: []questions.Question{question("q_3", "w1:p1", "j_9", questions.UrgencyBlocking, time.Minute)},
				Jobs:      []jobs.Job{job("j_9", 1, "", jobs.StateQueued, false, jobs.CauseApproval)},
			},
			want: []string{"question:q_3"},
		},
		{
			name: "a standing agent at a dialog is listed once, as the dialog",
			src: Sources{
				Agents:   []fleet.AgentEntry{dialogAgent("w0:p0", "orchestrator")},
				Standing: []StandingAgent{{Name: "orchestrator", Label: "The orchestrator", PaneID: "w0:p0", State: "blocked"}},
			},
			want: []string{"dialog:w0:p0"},
		},
		{
			name: "a lead that needs its terminal is an item",
			src: Sources{
				Projects: []projects.Project{crow},
				Standing: []StandingAgent{{Name: "lead-crowsnest", Label: "The lead of Crowsnest", ProjectID: 1, PaneID: "w5:p1", State: "needs_terminal"}},
			},
			want: []string{"agent:lead-crowsnest"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ids(Build(tc.src, testNow)); !equal(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// Every item carries its project and job, found by job id or by the pane that holds the job.
func TestBuildLabelsProjectAndJob(t *testing.T) {
	src := Sources{
		Projects:  []projects.Project{crow, site},
		Approvals: []approvals.Approval{approval("ap_1", "w1:p1", "", risk.Dangerous, time.Minute)}, // found by pane
		Questions: []questions.Question{question("q_1", "w2:p1", "j_2", questions.UrgencyBlocking, time.Minute)},
		Agents:    []fleet.AgentEntry{dialogAgent("w3:p1", "job-3")},
		Jobs: []jobs.Job{
			job("j_1", 1, "w1:p1", jobs.StateNeedsYou, false, jobs.CauseApprovalTool),
			job("j_2", 2, "w2:p1", jobs.StateNeedsYou, false, jobs.CauseQuestions),
			job("j_3", 2, "w3:p1", jobs.StateNeedsYou, false, jobs.CauseDialog),
			job("j_4", 0, "w4:p1", jobs.StateRunning, true, jobs.CauseLimit),
		},
	}
	items := Build(src, testNow)
	want := map[string]struct {
		project string
		job     string
	}{
		"approval:ap_1": {"crowsnest", "j_1"},
		"question:q_1":  {"website", "j_2"},
		"dialog:w3:p1":  {"website", "j_3"},
		"job:j_4":       {"", "j_4"},
	}
	if len(items) != len(want) {
		t.Fatalf("got %v", ids(items))
	}
	for _, item := range items {
		expect := want[item.ID]
		slug := ""
		if item.Project != nil {
			slug = item.Project.Slug
		}
		if slug != expect.project || item.JobID != expect.job || item.JobTitle == "" {
			t.Errorf("%s: project %q job %q title %q, want %q %q", item.ID, slug, item.JobID, item.JobTitle, expect.project, expect.job)
		}
	}
}

// Blocking before can-wait; within a band by rank, then the longest wait first.
func TestBuildOrdersByUrgencyRankAndWait(t *testing.T) {
	src := Sources{
		Approvals: []approvals.Approval{
			approval("ap_low", "w1:p1", "", risk.Low, 5*time.Minute),
			approval("ap_danger", "w1:p2", "", risk.Dangerous, time.Minute),
		},
		Questions: []questions.Question{
			question("q_new", "w2:p1", "", questions.UrgencyNormal, time.Minute),
			question("q_old", "w2:p2", "", questions.UrgencyNormal, time.Hour),
			question("q_block", "w2:p3", "", questions.UrgencyBlocking, 2*time.Minute),
		},
		Jobs: []jobs.Job{
			job("j_1", 0, "w9:p1", jobs.StateRunning, true, jobs.CauseLimit),
			job("j_2", 0, "w9:p2", jobs.StateRunning, true, jobs.CauseUnresponsive),
		},
	}
	got := ids(Build(src, testNow))
	want := []string{"approval:ap_danger", "question:q_block", "approval:ap_low", "job:j_2", "question:q_old", "question:q_new", "job:j_1"}
	if !equal(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
}

// The counts split blocking from can-wait, leave snoozed items out, and group by project.
func TestCount(t *testing.T) {
	until := testNow.Add(time.Minute)
	items := []Item{
		{ID: "a", Urgency: Blocking, Project: &ProjectRef{ID: 1}},
		{ID: "b", Urgency: CanWait},
		{ID: "c", Urgency: Blocking, SnoozedUntil: &until},
	}
	counts := Count(items)
	if counts.Total != 2 || counts.Blocking != 1 || counts.CanWait != 1 || counts.Snoozed != 1 {
		t.Fatalf("counts %+v", counts)
	}
	if counts.ByProject["1"] != 1 || counts.ByProject["0"] != 1 {
		t.Fatalf("by project %+v", counts.ByProject)
	}
}

// A question is blocking when its asker said so, waits on it, or its job declared it is waiting.
func TestQuestionUrgency(t *testing.T) {
	waiting := job("j_1", 1, "p", jobs.StateNeedsYou, false, jobs.CauseQuestions)
	running := job("j_2", 1, "p", jobs.StateRunning, false, "")
	cases := []struct {
		name string
		q    questions.Question
		job  *jobs.Job
		want Urgency
	}{
		{"blocking urgency", question("q", "", "", questions.UrgencyBlocking, 0), nil, Blocking},
		{"asker waits", func() questions.Question {
			q := question("q", "", "", questions.UrgencyNormal, 0)
			q.Wait = true
			return q
		}(), nil, Blocking},
		{"job declared waiting", question("q", "", "", questions.UrgencyNormal, 0), &waiting, Blocking},
		{"agent carries on", question("q", "", "", questions.UrgencyNormal, 0), &running, CanWait},
		{"low", question("q", "", "", questions.UrgencyLow, 0), nil, CanWait},
	}
	for _, tc := range cases {
		if got, _ := questionUrgency(tc.q, tc.job); got != tc.want {
			t.Errorf("%s: got %s want %s", tc.name, got, tc.want)
		}
	}
}
