// Tests for lead-first routing (DESIGN.md 7.4, 21.2): the pure route table, then hub events
// through handlePokeEvent into real pokers on the fake herdr (a project with a running lead,
// one without, Unfiled work, a job the orchestrator handed over), the orchestrator's
// "agent settled" reasons skipping panes that hold a lead's job, the 30-minute escalation, the
// timer lists, and a lead's spend subject.

package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/herdr/fake"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/projects"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/spend"
	"github.com/jclement/crowsnest/internal/store"
)

func TestRouteJob(t *testing.T) {
	up := leadView{Project: "Alpha", Up: true}
	down := leadView{Project: "Beta"}
	cases := []struct {
		name      string
		projectID int64
		origin    string
		settled   bool
		lead      leadView
		want      jobRoute
	}{
		{"unfiled", 0, "", true, leadView{}, jobRoute{Orchestrator: true}},
		{"unfiled the orchestrator made", 0, jobs.OriginOrchestrator, true, leadView{}, jobRoute{Orchestrator: true}},
		{"project job, lead up", 1, "", true, up, jobRoute{Lead: true}},
		{"project job stuck, lead up", 1, "", false, up, jobRoute{Lead: true}},
		{"handed over and settled", 1, jobs.OriginOrchestrator, true, up, jobRoute{Lead: true, Orchestrator: true, HandedOver: true, Prefix: `project "Alpha": `}},
		{"handed over, only stuck", 1, jobs.OriginOrchestrator, false, up, jobRoute{Lead: true}},
		{"project job, no lead", 2, "", true, down, jobRoute{Orchestrator: true, Prefix: `project "Beta" has no lead running: `}},
		{"handed over, no lead", 2, jobs.OriginOrchestrator, true, down, jobRoute{Orchestrator: true, Prefix: `project "Beta" has no lead running: `}},
	}
	for _, tc := range cases {
		if got := routeJob(tc.projectID, tc.origin, tc.settled, tc.lead); got != tc.want {
			t.Errorf("%s: %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// ---- The rig: real services on an in-memory store, real pokers on the fake herdr ----------

var (
	testRule       = "\x1b[38;5;244m" + strings.Repeat("─", 40) + "\x1b[39m"
	testEmptyInput = testRule + "\n❯ \x1b[7m \x1b[27m\n" + testRule + "\n"
	routeEpoch     = time.Date(2026, 11, 26, 3, 10, 0, 0, time.UTC)
)

type routeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *routeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }

func (c *routeClock) Advance(d time.Duration) { c.mu.Lock(); c.now = c.now.Add(d); c.mu.Unlock() }

type routeRig struct {
	t            *testing.T
	ctx          context.Context
	f            *fake.Fake
	db           *store.Store
	clock        *routeClock
	jobs         *jobs.Service
	questions    *questions.Service
	alpha, beta  projects.Project // alpha has a running lead, beta none
	leads        *projects.Leads
	orchPane     string
	workerPane   string
	orchestrator *orch.Poker
	services     pokeServices
}

func newRouteRig(t *testing.T) *routeRig {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel) // runs before the store closes: the leads stop first
	r := &routeRig{t: t, ctx: ctx, f: fake.New(), db: db, clock: &routeClock{now: routeEpoch}}
	r.jobs = jobs.New(jobs.Options{Store: db})
	r.questions = questions.New(questions.Options{Store: db})
	home := t.TempDir()
	service := projects.New(projects.Options{Store: db, Home: home, Roles: func(string) bool { return true }})
	r.alpha = r.project(service, home, "Alpha")
	r.beta = r.project(service, home, "Beta")
	r.leads = projects.NewLeads(ctx, service, db, r.leadFactory(t.TempDir()), slog.New(slog.DiscardHandler), nil)
	_, _, r.orchPane = r.f.AddWorkspace("orchestrator", "/o")
	_ = r.f.AddAgent(r.orchPane, orch.Name, "claude", herdr.StatusIdle, "s-orch")
	_, _, r.workerPane = r.f.AddWorkspace("work", r.alpha.Dir)
	_ = r.f.AddAgent(r.workerPane, "builder", "claude", herdr.StatusWorking, "s-b")
	r.screen(r.orchPane)
	r.services = pokeServices{jobs: r.jobs, questions: r.questions, leads: func() *projects.Leads { return r.leads }, db: db, leadWatch: newLeadWatch()}
	r.orchestrator = orch.NewPoker(orch.PokerOptions{
		Client: r.f, KV: db, Logger: slog.New(slog.DiscardHandler), Now: r.clock.Now, Status: r.orchStatus,
		OwnedElsewhere: r.services.ownedElsewhere(ctx), Escalations: r.services.escalations,
	})
	r.startLead()
	return r
}

func (r *routeRig) project(service *projects.Service, home, name string) projects.Project {
	r.t.Helper()
	dir := filepath.Join(home, strings.ToLower(name))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		r.t.Fatal(err)
	}
	project, err := service.Create(r.ctx, projects.Input{Name: &name, Dir: &dir})
	if err != nil {
		r.t.Fatal(err)
	}
	return project
}

// leadFactory builds a lead on the fake herdr with a poker that checks only when a reason
// arrives (CheckInterval an hour), on the rig's clock.
func (r *routeRig) leadFactory(state string) projects.LeadFactory {
	return func(p projects.Project) (*orch.Orchestrator, *orch.Poker) {
		agent := orch.New(orch.Options{
			Client: r.f, KV: r.db, Session: "crowsnest-test", Dir: p.Dir, StateDir: filepath.Join(state, p.Slug),
			AgentName: projects.AgentName(p.Slug), Workspace: "lead " + p.Slug, KVPrefix: "lead." + p.Slug,
			ShellWait: 200 * time.Millisecond, WatchInterval: 50 * time.Millisecond, Logger: slog.New(slog.DiscardHandler),
		})
		poker := orch.NewPoker(orch.PokerOptions{
			Client: r.f, KV: r.db, Logger: slog.New(slog.DiscardHandler), Now: r.clock.Now, Status: agent.Status,
			NoAgentReasons: true, StateKey: "lead." + p.Slug + ".poke.state", CheckInterval: time.Hour,
		})
		return agent, poker
	}
}

// startLead starts Alpha's lead and waits until it is idle with an empty input box.
func (r *routeRig) startLead() {
	r.t.Helper()
	if _, err := r.leads.Start(r.ctx, r.alpha.ID); err != nil {
		r.t.Fatal(err)
	}
	waitUntil(r.t, func() bool { return r.leads.Get(r.alpha.ID).Agent.Status().State == orch.StateIdle })
	r.screen(r.leadPane())
}

func (r *routeRig) leadPane() string { return r.leads.Get(r.alpha.ID).Agent.Status().PaneID }

func (r *routeRig) screen(pane string) {
	r.t.Helper()
	if err := r.f.SetScreen(pane, fleet.StripANSI(testEmptyInput), testEmptyInput); err != nil {
		r.t.Fatal(err)
	}
}

func (r *routeRig) orchStatus() orch.Status {
	agent, err := r.f.AgentGet(context.Background(), orch.Name)
	if err != nil {
		return orch.Status{State: orch.StateStarting}
	}
	state := orch.StateIdle
	switch agent.AgentStatus {
	case herdr.StatusWorking:
		state = orch.StateWorking
	case herdr.StatusBlocked:
		state = orch.StateBlocked
	}
	return orch.Status{State: state, PaneID: agent.PaneID, Name: orch.Name}
}

// job stores a job and returns its id.
func (r *routeRig) job(record store.JobRecord) string {
	r.t.Helper()
	if record.Title == "" {
		record.Title = "Build the thing"
	}
	record.BriefMD, record.Engine, record.CreatedAt = "brief", jobs.EngineClaude, r.clock.Now()
	id, err := r.db.InsertJob(r.ctx, record)
	if err != nil {
		r.t.Fatal(err)
	}
	return jobs.FormatID(id)
}

// settle publishes the job's settled (or stuck) event the way the hub carries it.
func (r *routeRig) settle(id string) {
	r.t.Helper()
	job, err := r.jobs.Get(r.ctx, id)
	if err != nil {
		r.t.Fatal(err)
	}
	event := hub.Event{Type: "job", Data: jobs.HubEvent{ID: id, State: job.State, Stuck: job.Stuck, Title: job.Title, Change: "state"}}
	handlePokeEvent(r.ctx, event, r.orchestrator, r.services, slog.New(slog.DiscardHandler))
}

// prompts are the texts typed into pane.
func (r *routeRig) prompts(pane string) []string {
	var out []string
	for _, prompt := range r.f.Prompts() {
		if prompt.PaneID == pane {
			out = append(out, prompt.Text)
		}
	}
	return out
}

func waitUntil(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// ---- Routing through real pokers ------------------------------------------------------------

func TestLeadFirstRouting(t *testing.T) {
	cases := []struct {
		name     string
		record   func(r *routeRig) store.JobRecord
		leadPoke string // a substring of the one poke the lead gets; "" for none
		orchPoke string // the orchestrator's one reason; "" for none
	}{
		{
			name:     "a worker completion in a project with a running lead goes to the lead alone",
			record:   func(r *routeRig) store.JobRecord { return store.JobRecord{ProjectID: r.alpha.ID, State: "done"} },
			leadPoke: `is done: "Build the thing"`,
		},
		{
			name:     "a project with no lead running: the orchestrator, prefixed",
			record:   func(r *routeRig) store.JobRecord { return store.JobRecord{ProjectID: r.beta.ID, State: "done"} },
			orchPoke: `project "Beta" has no lead running: job j_1 is done: "Build the thing"`,
		},
		{
			name: "a job the orchestrator handed over settles: the lead, and the orchestrator hears the outcome",
			record: func(r *routeRig) store.JobRecord {
				return store.JobRecord{ProjectID: r.alpha.ID, State: "done", Origin: jobs.OriginOrchestrator}
			},
			leadPoke: `is done: "Build the thing"`,
			orchPoke: `project "Alpha": job j_1 you handed it is done: "Build the thing"`,
		},
		{
			name:     "unfiled work stays the orchestrator's",
			record:   func(*routeRig) store.JobRecord { return store.JobRecord{State: "failed"} },
			orchPoke: `job j_1 is failed: "Build the thing"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRouteRig(t)
			record := tc.record(r)
			record.PaneID = r.workerPane
			r.settle(r.job(record))
			if tc.leadPoke != "" {
				waitUntil(t, func() bool { return len(r.prompts(r.leadPane())) > 0 })
			}
			got := r.orchestrator.Check(r.ctx, false)
			time.Sleep(100 * time.Millisecond) // a second lead poke would have been typed by now
			leadPokes := r.prompts(r.leadPane())
			switch {
			case tc.leadPoke == "" && len(leadPokes) != 0:
				t.Fatalf("the lead was poked: %q", leadPokes)
			case tc.leadPoke != "" && (len(leadPokes) != 1 || !strings.Contains(leadPokes[0], tc.leadPoke)):
				t.Fatalf("lead pokes %q, want one with %q", leadPokes, tc.leadPoke)
			}
			orchPokes := r.prompts(r.orchPane)
			if tc.orchPoke == "" {
				if got.Outcome != orch.OutcomeNothing || len(orchPokes) != 0 {
					t.Fatalf("the orchestrator was poked: %+v %q", got, orchPokes)
				}
				return
			}
			if got.Outcome != orch.OutcomeSent || !slices.Equal(got.Reasons, []string{tc.orchPoke}) || len(orchPokes) != 1 {
				t.Fatalf("orchestrator: %+v %q, want %q", got, orchPokes, tc.orchPoke)
			}
		})
	}
}

func TestQuestionOfALeadsJobGoesToTheLead(t *testing.T) {
	r := newRouteRig(t)
	jobID := r.job(store.JobRecord{ProjectID: r.alpha.ID, State: "running", PaneID: r.workerPane})
	id, err := r.db.InsertQuestion(r.ctx, store.QuestionRecord{Title: "Which port?", Kind: "confirm", SourcePane: r.workerPane, JobID: jobID, CreatedAt: r.clock.Now(), ExpiresAt: r.clock.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.db.SettleQuestion(r.ctx, id, "answered", []byte(`"yes"`), "", "", r.clock.Now()); err != nil {
		t.Fatal(err)
	}
	handlePokeEvent(r.ctx, hub.Event{Type: "question", Data: questions.Event{ID: questions.FormatID(id), Change: "answered"}}, r.orchestrator, r.services, slog.New(slog.DiscardHandler))
	waitUntil(t, func() bool { return len(r.prompts(r.leadPane())) == 1 })
	if !strings.Contains(r.prompts(r.leadPane())[0], `was answered: "Which port?"`) {
		t.Fatalf("lead poke %q", r.prompts(r.leadPane()))
	}
	if got := r.orchestrator.Check(r.ctx, false); got.Outcome != orch.OutcomeNothing {
		t.Fatalf("the orchestrator heard a lead's question: %+v", got)
	}
}

// ---- Ownership of "agent settled" reasons -----------------------------------------------------

func TestOwnedElsewhere(t *testing.T) {
	r := newRouteRig(t)
	_, _, betaPane := r.f.AddWorkspace("beta work", r.beta.Dir)
	_, _, unfiledPane := r.f.AddWorkspace("loose", "/tmp")
	r.job(store.JobRecord{ProjectID: r.alpha.ID, State: "running", PaneID: r.workerPane})
	r.job(store.JobRecord{ProjectID: r.beta.ID, State: "running", PaneID: betaPane})
	r.job(store.JobRecord{State: "running", PaneID: unfiledPane})
	owned := r.services.ownedElsewhere(r.ctx)
	for pane, want := range map[string]bool{r.workerPane: true, r.leadPane(): true, betaPane: false, unfiledPane: false, r.orchPane: false, "": false} {
		if got := owned(pane); got != want {
			t.Errorf("pane %q: %v, want %v", pane, got, want)
		}
	}
}

func TestOrchestratorIsNotToldAboutALeadsWorkerSettling(t *testing.T) {
	r := newRouteRig(t)
	r.job(store.JobRecord{ProjectID: r.alpha.ID, State: "running", PaneID: r.workerPane})
	if err := r.f.SetStatus(r.workerPane, herdr.StatusDone); err != nil {
		t.Fatal(err)
	}
	r.orchestrator.Check(r.ctx, false)
	r.clock.Advance(orch.DoneHold + time.Second)
	if got := r.orchestrator.Check(r.ctx, false); got.Outcome != orch.OutcomeNothing {
		t.Fatalf("the orchestrator heard a lead's worker settle: %+v", got)
	}
}

// ---- Escalation ---------------------------------------------------------------------------

func TestStuckProjectJobEscalatesOnceAfterThirtyMinutes(t *testing.T) {
	r := newRouteRig(t)
	stuckSince := r.clock.Now().Add(-10 * time.Minute)
	r.job(store.JobRecord{ProjectID: r.alpha.ID, State: "running", Stuck: true, Cause: string(jobs.CauseUnresponsive), PaneID: r.workerPane, LastActivityAt: stuckSince})
	// Beta has no lead: its stuck job reached the orchestrator with the prefix, not as an escalation.
	r.job(store.JobRecord{ProjectID: r.beta.ID, State: "running", Stuck: true, Cause: string(jobs.CauseUnresponsive), LastActivityAt: stuckSince})
	if got := r.orchestrator.Check(r.ctx, false); got.Outcome != orch.OutcomeNothing {
		t.Fatalf("escalated at 10 minutes: %+v", got)
	}
	r.clock.Advance(19 * time.Minute)
	if got := r.orchestrator.Check(r.ctx, false); got.Outcome != orch.OutcomeNothing {
		t.Fatalf("escalated at 29 minutes: %+v", got)
	}
	r.clock.Advance(2 * time.Minute)
	got := r.orchestrator.Check(r.ctx, false)
	if got.Outcome != orch.OutcomeSent || len(got.Reasons) != 1 || !strings.HasPrefix(got.Reasons[0], `escalation: project "Alpha": job j_1 has been stuck`) {
		t.Fatalf("escalation: %+v", got)
	}
	_ = r.f.SetStatus(r.orchPane, herdr.StatusIdle)
	for range 3 {
		r.clock.Advance(5 * time.Minute)
		if got := r.orchestrator.Check(r.ctx, false); got.Outcome != orch.OutcomeNothing {
			t.Fatalf("escalated again: %+v", got)
		}
	}
	if len(r.prompts(r.leadPane())) != 0 {
		t.Fatal("the lead was poked by the escalation")
	}
}

// ---- Timer lists ------------------------------------------------------------------------------

func TestStandingListsSplitByOwner(t *testing.T) {
	r := newRouteRig(t)
	alphaJob := r.job(store.JobRecord{Title: "Alpha work", ProjectID: r.alpha.ID, State: "needs_you", PaneID: r.workerPane})
	r.job(store.JobRecord{Title: "Beta work", ProjectID: r.beta.ID, State: "blocked"})
	r.job(store.JobRecord{Title: "Loose work", State: "running", Stuck: true, Cause: string(jobs.CauseUnresponsive)})
	if _, err := r.db.InsertQuestion(r.ctx, store.QuestionRecord{Title: "Which port?", Kind: "confirm", SourcePane: r.workerPane, JobID: alphaJob, CreatedAt: r.clock.Now(), ExpiresAt: r.clock.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	lead := strings.Join(leadStandingWork(r.jobs, r.questions, r.alpha.ID)(r.ctx), "\n")
	if !strings.Contains(lead, `needs the human: "Alpha work"`) || !strings.Contains(lead, `(job `+alphaJob+`) is open for the human: "Which port?"`) || strings.Contains(lead, "Beta") || strings.Contains(lead, "Loose") {
		t.Fatalf("lead's list:\n%s", lead)
	}
	orchestrator := strings.Join(standingWork(r.services)(r.ctx), "\n")
	if strings.Contains(orchestrator, "Alpha work") || !strings.Contains(orchestrator, "Beta work") || !strings.Contains(orchestrator, "Loose work") {
		t.Fatalf("orchestrator's list:\n%s", orchestrator)
	}
}

// ---- Spend ------------------------------------------------------------------------------------

func TestLeadSpendSubject(t *testing.T) {
	r := newRouteRig(t)
	wiring := &projectWiring{}
	wiring.leads.Store(r.leads)
	sources := leadSpendSources(wiring)
	status := r.leads.Get(r.alpha.ID).Agent.Status()
	if len(sources) != 1 || sources[0].Subject != "lead:"+r.alpha.Slug || sources[0].Subject != projects.Subject(r.alpha.Slug) ||
		len(sources[0].Sessions) != 1 || sources[0].Sessions[0].ID != status.SessionID || sources[0].Sessions[0].Cwd != r.alpha.Dir || status.SessionID == "" {
		t.Fatalf("sources %+v (session %q)", sources, status.SessionID)
	}
	// A job the lead holds is counted on the lead's line.
	meter := spend.NewMeter(spend.Options{Store: r.db, Prices: spend.Embedded()})
	service := jobs.New(jobs.Options{Store: r.db, Spend: meter})
	held := r.job(store.JobRecord{ProjectID: r.alpha.ID, State: "running", PaneID: r.leadPane(), AgentName: projects.AgentName(r.alpha.Slug)})
	job, err := service.Get(r.ctx, held)
	if err != nil || job.Spend == nil || job.Spend.Reason != spend.ReasonLead {
		t.Fatalf("held job spend %+v err %v", job.Spend, err)
	}
	if text := spend.Describe(*job.Spend); !strings.Contains(text, "on the lead's line") {
		t.Fatalf("describe %q", text)
	}
}
