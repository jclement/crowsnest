// R10 (R9-S2 residual, new-H1): the database a restore brings in is scrubbed as a snapshot is,
// whoever made the backup.

package backup

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

// TestRestoredDatabaseIsScrubbedWhoeverMadeIt: a backup crowsnest did not make (another
// instance's, an older one's, a crafted one) can carry what a snapshot never does. markRestored
// removes it from the staged database: browser sessions, action and API tokens, push
// subscriptions and keys, machine and agent tokens, the .codex/ "seen" marks, and every isolated
// check directory a row names. The owner's record and the work stay.
func TestRestoredDatabaseIsScrubbedWhoeverMadeIt(t *testing.T) {
	ctx := context.Background()
	src, dir := populated(t) // sessions, a push subscription and key, an agent token, a notify URL
	db := src.DB
	now := time.Now().UTC()
	_, err := db.InsertAPIToken(ctx, store.APIToken{Name: "theirs", Scope: "admin", Hint: "abcd", CreatedAt: now}, "token-hash")
	mustDo(t, err)
	_, err = db.InsertMachine(ctx, store.Machine{Name: "theirs", URL: "https://theirs.example", Token: "MACHINE-TOKEN", CreatedAt: now})
	mustDo(t, err)
	mustDo(t, db.InsertActionToken(ctx, store.ActionTokenRecord{Hash: "action-hash", Item: "ap_1", Actions: `["allow"]`, ExpiresAt: now.Add(time.Hour), CreatedAt: now}))
	mustDo(t, db.SetKV(ctx, store.CodexConfigSeenPrefix+"fingerprint", "/repo 2026-11-28T00:00:00Z"))
	jobs, err := db.ListJobs(ctx, nil, 10)
	mustDo(t, err)
	checkID, err := db.InsertCheck(ctx, store.CheckRecord{JobID: jobs[0].ID, Commit: "0123456789abcdef0123456789abcdef01234567", Command: "true", Status: "running"})
	mustDo(t, err)
	mustDo(t, db.SetCheckWorkspace(ctx, checkID, "/Users/someone"))
	staged := filepath.Join(dir, "staged.db")
	mustDo(t, store.VacuumFileInto(ctx, filepath.Join(dir, "crowsnest.db"), staged)) // unscrubbed

	if err := markRestored(ctx, staged, filepath.Join(dir, "theirs.tar.gz"), Manifest{CreatedAt: now}, now, &Result{}); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Open(ctx, staged)
	mustDo(t, err)
	defer func() { _ = restored.Close() }()
	if _, err := restored.GetSession(ctx, "sessionhash"); err == nil {
		t.Error("a browser session came back")
	}
	if subs, _ := restored.ListPushSubscriptions(ctx); len(subs) != 0 {
		t.Errorf("push subscriptions came back: %v", subs)
	}
	if tokens, _ := restored.ListAPITokens(ctx); len(tokens) != 0 {
		t.Errorf("API tokens came back: %v", tokens)
	}
	if machines, _ := restored.ListMachines(ctx); len(machines) != 1 || machines[0].Token != "" {
		t.Errorf("a machine token came back: %+v", machines)
	}
	if _, err := restored.UseActionToken(ctx, "action-hash", now); err == nil {
		t.Error("an action token came back")
	}
	for _, key := range []string{"agent_token:deadbeef", "push.vapid", store.CodexConfigSeenPrefix + "fingerprint"} {
		if value, _ := restored.GetKV(ctx, key); value != "" {
			t.Errorf("%s came back: %q", key, value)
		}
	}
	if dirs, _ := restored.CheckWorkspaces(ctx); len(dirs) != 0 {
		t.Errorf("a check directory is still recorded: %v", dirs)
	}
	if owner, _ := restored.GetKV(ctx, "auth.owner"); owner == "" {
		t.Error("the owner's record was removed: the human could not sign in to their restore")
	}
	if jobs, _ := restored.ListJobs(ctx, nil, 10); len(jobs) != 1 {
		t.Errorf("the work did not survive: %d jobs", len(jobs))
	}
}
