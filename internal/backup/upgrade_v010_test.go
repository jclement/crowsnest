// The upgrade from the released v0.1.0 (R9 Codex's "static compatibility is not a green upgrade
// test"). testdata/v0.1.0.sql is a database made by v0.1.0's own schema: the nine migration files
// read from the v0.1.0 tag (`git show v0.1.0:internal/store/migrations/<file>`, byte for byte
// what main still carries for 0001-0009), with a day's rows in every table that existed then. It
// was built with sqlite3 (the tag's migrations, then the rows, then `.dump`); to regenerate it,
// do the same. The test opens it the way serve does (the pre-upgrade copy first, then today's
// store, which runs 0010 onwards), reads the old rows through today's services, checks that the
// new columns and holds came in at their safe values, and round-trips a backup of the result.

package backup_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/jclement/crowsnest/internal/auth"
	"github.com/jclement/crowsnest/internal/backup"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/store"
)

// The fixture's login, as v0.1.0 took it (--username and CROWSNEST_PASSWORD), and the token of
// the browser session it had issued.
const (
	v010User    = "jeff"
	v010Pass    = "a-long-password"
	v010Token   = "v010-session-token-abcdef"
	v010Host    = "127.0.0.1:2769"
	v010Schema  = 9
	v010Session = "crowsnest"
)

// loadV010 writes the fixture into a fresh database file, exactly as v0.1.0 left it.
func loadV010(t *testing.T, path string) {
	t.Helper()
	dump, err := os.ReadFile("testdata/v0.1.0.sql")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(string(dump)); err != nil {
		t.Fatalf("loading the v0.1.0 fixture: %v", err)
	}
}

func TestUpgradeFromTheReleasedV010Database(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "data", "crowsnest.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatal(err)
	}
	loadV010(t, dbPath)

	// 1. The pre-upgrade copy, as README "Upgrades" says: before a start that changes the schema,
	// <data-dir>/backups/pre-migration-<session>-<time>-s<schema>-<version>.db.
	backups := filepath.Join(dir, "data", "backups")
	check, err := backup.PrepareUpgrade(ctx, dbPath, backups, v010Session, "v0.2.0", time.Now())
	if err != nil {
		t.Fatalf("preparing the upgrade: %v", err)
	}
	if check.SchemaVersion != v010Schema || check.KnownVersion != store.KnownSchemaVersion() || check.BackupPath == "" {
		t.Fatalf("upgrade check %+v", check)
	}
	if name := filepath.Base(check.BackupPath); !strings.HasPrefix(name, "pre-migration-"+v010Session+"-") || !strings.Contains(name, "-s9-") || !strings.HasSuffix(name, ".db") {
		t.Errorf("the copy is named %s", name)
	}
	copied, err := store.Inspect(ctx, check.BackupPath)
	if err != nil || copied.SchemaVersion != v010Schema {
		t.Fatalf("the pre-upgrade copy is not the v0.1.0 database: %+v %v", copied, err)
	}

	// 2. Today's store runs every later migration.
	db, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("opening the v0.1.0 database with today's store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if version, _ := db.SchemaVersion(ctx); version != store.KnownSchemaVersion() {
		t.Fatalf("schema %d after the upgrade, want %d", version, store.KnownSchemaVersion())
	}

	assertJobsSurvived(ctx, t, db)
	assertQuestionsSurvived(ctx, t, db)
	assertTheOwnerSignsIn(ctx, t, db)
	assertRoundTrip(ctx, t, db, dir)
}

// assertJobsSurvived reads the v0.1.0 jobs through today's service: their state and words as they
// were, and every gate added since at its safe value (nothing accepted, landed, approved or held
// by the migration; a running job is not moved).
func assertJobsSurvived(ctx context.Context, t *testing.T, db *store.Store) {
	t.Helper()
	service := jobs.New(jobs.Options{Store: db})
	want := map[string]jobs.State{"j_1": jobs.StateRunning, "j_2": jobs.StateQueued, "j_3": jobs.StateDone, "j_4": jobs.StateNeedsYou,
		"j_5": jobs.StateFailed, "j_6": jobs.StateBlocked, "j_7": jobs.StateRunning, "j_8": jobs.StateQueued}
	for id, state := range want {
		detail, err := service.Get(ctx, id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		job := detail.Job
		if job.State != state {
			t.Errorf("%s: state %s, want %s", id, job.State, state)
		}
		if job.AcceptState != "" || job.LandState != "" || job.LandRequest != "" || job.ReviewVerdict != "" || job.ChangesState != "" || job.FixJob != "" {
			t.Errorf("%s: a gate came in set: accept %q land %q request %q review %q changes %q fix %q",
				id, job.AcceptState, job.LandState, job.LandRequest, job.ReviewVerdict, job.ChangesState, job.FixJob)
		}
		if len(job.Holds) != 0 {
			t.Errorf("%s: the upgrade put holds on it: %+v", id, job.Holds)
		}
	}
	running, _ := service.Get(ctx, "j_1")
	if running.Title != "Add the CSV export" || running.PaneID != "w2:p1" || running.WorktreeBranch != "job/1-add-the-csv-export" ||
		running.WorkDir != "/Users/jeff/.herdr/worktrees/app/job-1" || running.Progress != "writing the exporter" || running.Model != "sonnet" {
		t.Errorf("the running job read back wrong: %+v", running.Job)
	}
	if len(running.Timeline) < 2 {
		t.Errorf("the running job's timeline: %+v", running.Timeline)
	}
	done, _ := service.Get(ctx, "j_3")
	if done.ResultMD != "Renamed the flag; old name kept as an alias." || !slices.Equal(done.Verified, []string{"go test ./... (ok)"}) {
		t.Errorf("the done job's evidence: %q %v", done.ResultMD, done.Verified)
	}
	failed, _ := service.Get(ctx, "j_5")
	if failed.Engine != "codex" || !strings.Contains(failed.FailReason, "Node 24") {
		t.Errorf("the failed job: %+v", failed.Job)
	}
	child, _ := service.Get(ctx, "j_8")
	if child.ParentID != "j_1" {
		t.Errorf("the child job's parent: %q", child.ParentID)
	}
	// Land: a running job is not landable, and a v0.1.0 done job has no recorded base branch, so a
	// merge is refused before anything touches git.
	if _, err := service.Land(ctx, "j_1", jobs.LandMerge, "test"); err == nil || !strings.Contains(err.Error(), "finished") {
		t.Errorf("landing the running job: %v", err)
	}
	if _, err := service.Land(ctx, "j_3", jobs.LandMerge, "test"); err == nil || !strings.Contains(err.Error(), "does not know the branch") {
		t.Errorf("merging the v0.1.0 done job: %v", err)
	}
}

// assertQuestionsSurvived reads the v0.1.0 questions through today's service.
func assertQuestionsSurvived(ctx context.Context, t *testing.T, db *store.Store) {
	t.Helper()
	service := questions.New(questions.Options{Store: db})
	open, err := service.Get(ctx, "q_1")
	if err != nil {
		t.Fatal(err)
	}
	if open.Status != questions.StatusOpen || open.Title != "Which chart library?" || open.Urgency != questions.UrgencyBlocking ||
		len(open.Options) != 2 || open.Options[1].Key != "uplot" || open.Recommend != "uplot" || open.JobID != "j_4" {
		t.Errorf("the open question read back wrong: %+v", open)
	}
	answered, err := service.Get(ctx, "q_2")
	if err != nil || answered.Status != questions.StatusAnswered || answered.AnswerNote != "as an alias" {
		t.Errorf("the answered question: %+v %v", answered, err)
	}
}

// assertTheOwnerSignsIn: v0.1.0's login (the same flags) still signs in, and the browser session
// v0.1.0 issued is still honoured (the credential digest and salt are unchanged).
func assertTheOwnerSignsIn(ctx context.Context, t *testing.T, db *store.Store) {
	t.Helper()
	service, err := auth.New(ctx, auth.Options{Username: v010User, Password: v010Pass}, db)
	if err != nil {
		t.Fatalf("auth over the upgraded database: %v", err)
	}
	if session, err := service.Authenticate(ctx, v010Token, v010Host); err != nil || session.Username != v010User {
		t.Errorf("v0.1.0's browser session was not honoured: %+v %v", session, err)
	}
	if _, session, err := service.Login(ctx, "127.0.0.1", v010User, v010Pass, "test", v010Host); err != nil || session.Username != v010User {
		t.Errorf("the owner cannot sign in: %+v %v", session, err)
	}
	if _, _, err := service.Login(ctx, "127.0.0.1", v010User, "wrong-password", "test", v010Host); err == nil {
		t.Error("a wrong password signed in")
	}
}

// assertRoundTrip backs the upgraded database up and restores it into a fresh place: it restores,
// at today's schema, with the work in it.
func assertRoundTrip(ctx context.Context, t *testing.T, db *store.Store, dir string) {
	t.Helper()
	archive := filepath.Join(dir, "upgraded.tar.gz")
	if _, err := backup.Create(ctx, backup.Source{DB: db, Version: "v0.2.0", Session: v010Session, TempDir: dir}, archive); err != nil {
		t.Fatalf("backing up the upgraded database: %v", err)
	}
	restored := filepath.Join(t.TempDir(), "data", "crowsnest.db")
	result, err := backup.Restore(ctx, archive, backup.Target{DatabasePath: restored})
	if err != nil {
		t.Fatalf("restoring the upgraded database: %v", err)
	}
	if result.Migrated != store.KnownSchemaVersion() {
		t.Errorf("restored at schema %d", result.Migrated)
	}
	again, err := store.Open(ctx, restored)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = again.Close() }()
	detail, err := jobs.New(jobs.Options{Store: again}).Get(ctx, "j_1")
	if err != nil || detail.Title != "Add the CSV export" || detail.State != jobs.StateRunning {
		t.Errorf("the restored copy lost the work: %+v %v", detail.Job, err)
	}
	if q, err := questions.New(questions.Options{Store: again}).Get(ctx, "q_1"); err != nil || q.Status != questions.StatusOpen {
		t.Errorf("the restored copy lost the open question: %+v %v", q, err)
	}
}
