// Where copies for each agent go, under the state directory. One place decides it, so the
// launchers' Read allow rules (agentcfg.AllowRead) and the copies always name the same
// directory.
//
//	<state>/orchestrator/attachments/   the orchestrator: Chat, and jobs it holds (no directory)
//	<state>/jobs/j_N/attachments/       a job agent: its brief, messages and answers
//	<state>/panes/<pane>/attachments/   an agent crowsnest did not start (Fleet's Chat)

package attach

import (
	"path/filepath"
	"strings"
)

// Layout maps owners to inbox directories.
type Layout struct {
	StateDir string
}

const inboxDir = "attachments"

// Orchestrator is the orchestrator's inbox.
func (l Layout) Orchestrator() Inbox {
	return Inbox{Kind: OwnerOrchestrator, Base: l.StateDir, Dir: filepath.Join(l.StateDir, "orchestrator", inboxDir)}
}

// Job is a job's inbox. A job with no directory is held by the orchestrator, so its files go
// where the orchestrator already reads; they still belong to the job, and are released with it.
func (l Layout) Job(jobID string, heldByOrchestrator bool) Inbox {
	if heldByOrchestrator {
		inbox := l.Orchestrator()
		inbox.Kind, inbox.ID = OwnerJob, jobID
		return inbox
	}
	return Inbox{Kind: OwnerJob, ID: jobID, Base: l.StateDir, Dir: l.JobDir(jobID)}
}

// JobDir is a job agent's inbox directory: what its settings allow it to read.
func (l Layout) JobDir(jobID string) string {
	return filepath.Join(l.StateDir, "jobs", jobID, inboxDir)
}

// Pane is the inbox of an agent crowsnest did not start (or an asker it cannot place).
func (l Layout) Pane(paneID string) Inbox {
	name := safeRun(paneID)
	if strings.Trim(name, "-.") == "" {
		name = "unknown"
	}
	return Inbox{Kind: OwnerPane, ID: paneID, Base: l.StateDir, Dir: filepath.Join(l.StateDir, "panes", name, inboxDir)}
}
