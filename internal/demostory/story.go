// Package demostory is the one place the `crowsnest --demo` story's names live (DESIGN.md 21.5):
// the five projects, the made-up directories their agents work in, and the agents that hold the
// seeded jobs. The fake fleet (internal/herdr/fake), the seeded jobs (internal/jobs), questions
// (internal/questions) and approvals (internal/approvals) all take their names from here, so a
// job card's agent chip, its Fleet row and the approval or question its pane raised agree. The
// R4 product review (section 6) found them disagreeing when each package kept its own; the
// invariants are now checked end to end by cmd/crowsnest's TestDemoStory.
package demostory

// The demo projects' slugs.
const (
	Crowsnest = "crowsnest"
	Website   = "website"
	Mobile    = "mobile"
	Infra     = "infra"
	Handbook  = "handbook"
)

// Slugs lists the demo projects in the order their workspaces are created in the fake fleet.
var Slugs = []string{Crowsnest, Website, Mobile, Infra, Handbook}

// Dir is the made-up directory a demo project's agents work in, as the fake fleet shows it
// ("~/src/<slug>"). The seeded jobs name it too; cmd/crowsnest maps it to the project's real
// throwaway directory when it files them.
func Dir(slug string) string { return "~/src/" + slug }

// The agents that hold the seeded jobs. A job agent is named job-<n> after its job's id, as a real
// launch names it, and the seeded jobs are inserted in id order into an empty board
// (internal/jobs/demo.go), so these follow that order. Jobs without a pane (blocked, done,
// failed, queued) need no agent.
const (
	Orchestrator    = "orchestrator"
	WebhookBuilder  = "job-1"  // Crowsnest: retry back-off for the webhook sender, in a worktree
	InstallGuide    = "job-2"  // Website: the install guide, nudged
	FormKit         = "job-3"  // Mobile: the settings page, stuck on the usage limit
	HandbookPublish = "job-5"  // Handbook: rebuilding the site, at a terminal dialog
	CacheProfiler   = "job-6"  // Crowsnest: profiling the fleet cache, unresponsive
	CacheBuilder    = "job-11" // Crowsnest: the lead's job, waiting on a blocking question
	CacheReviewer   = "job-12" // Crowsnest: CacheBuilder's child, with an open multi-select
	LimiterReviewer = "job-13" // Crowsnest: the dangerous force-push approval
	LinkFixer       = "job-14" // Website: a low Bash approval
	HeroWriter      = "job-15" // Website: waiting on a blocking question
	RefreshBuilder  = "job-16" // Mobile: an Edit approval
	DevboxBuilder   = "job-17" // Infra: running, nothing waiting
)

// Asker is a demo agent that raises a question or an approval: the pane it sits in, the job it
// holds there, and that job's directory (the real one the job records). The seeders of
// internal/questions and internal/approvals look one up by agent name; cmd/crowsnest resolves
// it from the seeded jobs, so what is asked always names the job whose pane asked it.
type Asker struct {
	AgentName string
	PaneID    string
	JobID     string
	Dir       string
}

// Askers resolves a demo agent by name; the zero Asker when no live job holds it.
type Askers func(agentName string) Asker

// projectOf files each job agent under its project.
var projectOf = map[string]string{
	WebhookBuilder: Crowsnest, CacheProfiler: Crowsnest, CacheBuilder: Crowsnest, CacheReviewer: Crowsnest, LimiterReviewer: Crowsnest,
	InstallGuide: Website, LinkFixer: Website, HeroWriter: Website,
	FormKit: Mobile, RefreshBuilder: Mobile,
	DevboxBuilder:   Infra,
	HandbookPublish: Handbook,
}

// ProjectOf returns the slug of the project a demo job agent works in ("" for the orchestrator,
// whose jobs are Unfiled, and for an agent that holds no job).
func ProjectOf(agentName string) string { return projectOf[agentName] }
