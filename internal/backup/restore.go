// Restoring a backup (DESIGN.md 4.17). A restore is staged in full first: every entry is
// extracted into a private staging directory under the data directory and checked against the
// manifest (name allowed, size and SHA-256 equal, nothing missing, nothing extra), and the
// database's schema is checked against this binary, before a single file is moved into place.
//
// It refuses a data directory that already holds a database with anything in it, unless forced;
// a forced restore keeps the replaced database beside it (`<db>.before-restore-<stamp>`), so even
// that is undoable by hand. The staged database is then opened once, which runs any newer
// migrations, and marked PAUSED (kv `pause.state`, the pause switch's own record) with a
// `restore.pending` row that tells the next start to take stock of the agents herdr really has
// before anything is dispatched, all before it is installed. Installing is all or nothing
// (install, R5-C04): a failure at any point leaves the data as it was.

package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

// Keys a restore writes, read by serve at the next start.
const (
	// PauseKey is the pause switch's state (internal/pause on the landing branch keeps the same
	// row and shape), so the restored instance starts with its automation paused.
	PauseKey = "pause.state"
	// PendingKey marks a restore whose agents have not been reconciled with herdr yet.
	PendingKey = "restore.pending"
)

// Pending is the PendingKey row.
type Pending struct {
	At        time.Time `json:"at"`
	File      string    `json:"file"`
	CreatedAt time.Time `json:"created_at"` // when the backup was made
	Version   string    `json:"crowsnest_version"`
	Name      string    `json:"instance_name"`
}

// pauseState is the pause switch's stored shape.
type pauseState struct {
	Paused bool      `json:"paused"`
	By     string    `json:"by,omitempty"`
	At     time.Time `json:"at"`
	Until  time.Time `json:"until"`
	Reason string    `json:"reason,omitempty"`
}

// Target is where a restore writes.
type Target struct {
	DatabasePath    string // <data-dir>/<session>.db
	AttachmentsDir  string // <data-dir>/attachments
	ConfigDir       string // where config/roles, config/tasks and config/risk.yaml go (~/.config/crowsnest)
	OrchestratorDir string // where orchestrator/tasks goes (--dir)
	Force           bool
	Now             func() time.Time
}

// Result says what a restore did.
type Result struct {
	Manifest Manifest
	Replaced string   // the previous database, kept, when forced over one
	Written  []string // files written outside the database
	Skipped  []string // config files left alone because they exist (pass Force to replace)
	Migrated int      // the schema version after opening (higher than the backup's when migrations ran)
	// RevokedRules is how many approval rules the backup carried, revoked (R6-S5); ForgotSeen how
	// many role files' "I've read it" marks were dropped, so changed role files show as unread.
	RevokedRules int
	ForgotSeen   int
	// CheckCommands are the projects' custom check commands set back to automatic (R7-S4), kept
	// aside until the human uses them again; RestoredLayers how many instruction layers are
	// withheld from agents until the human applies them.
	CheckCommands  []store.RestoredCheck
	RestoredLayers int
}

// ErrNotEmpty is returned when the target already holds data and Force is off.
var ErrNotEmpty = errors.New("the data directory already holds a crowsnest database")

// ---- Reading ----------------------------------------------------------------

// Verify reads the whole backup at path and checks it against its manifest without writing
// anything. It returns the manifest.
func Verify(path string) (Manifest, error) {
	return walk(path, func(string, io.Reader) error { return nil })
}

// walk opens the archive, reads the manifest (which must come first), and calls each for every
// other entry with a reader that checks the entry's size and digest; the manifest's promises
// are then checked for anything missing.
func walk(path string, each func(name string, body io.Reader) error) (Manifest, error) {
	file, err := os.Open(path) //nolint:gosec // the user names the backup to read
	if err != nil {
		return Manifest{}, fmt.Errorf("opening backup %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	zipped, err := gzip.NewReader(file)
	if err != nil {
		return Manifest{}, fmt.Errorf("%s is not a crowsnest backup (not gzip): %w", path, err)
	}
	archive := tar.NewReader(zipped)
	manifest, err := readManifest(archive)
	if err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := checkTotals(manifest); err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", path, err)
	}
	want := map[string]File{}
	for _, item := range manifest.Files {
		if !validName(item.Path) || item.Path == manifestName {
			return Manifest{}, fmt.Errorf("%s: the manifest names %q, which a backup never holds", path, item.Path)
		}
		want[item.Path] = item
	}
	if _, ok := want[databaseName]; !ok {
		return Manifest{}, fmt.Errorf("%s holds no database", path)
	}
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Manifest{}, fmt.Errorf("reading %s: %w", path, err)
		}
		if err := checkEntry(archive, header, want, each); err != nil {
			return Manifest{}, fmt.Errorf("%s: %w", path, err)
		}
		delete(want, header.Name)
	}
	for name := range want {
		return Manifest{}, fmt.Errorf("%s: %s is in the manifest but not in the archive (truncated?)", path, name)
	}
	return manifest, nil
}

// Bounds on a whole archive (R5-S9), beside maxEntryBytes per entry: a backup from a shared
// drive or a chat may be crafted, and a gzip bomb must not fill the disk while it is staged.
// Every entry is also checked against the size the manifest promised as it is read, so the
// manifest's total is the most a restore ever writes.
const (
	maxTotalBytes = 32 << 30 // 32 GiB: the database and every attachment of a busy instance
	maxEntries    = 200_000
)

// checkTotals refuses a manifest that promises more than a restore will write.
func checkTotals(manifest Manifest) error {
	if len(manifest.Files) > maxEntries {
		return fmt.Errorf("the manifest lists %d files; a crowsnest backup has at most %d", len(manifest.Files), maxEntries)
	}
	var total int64
	for _, item := range manifest.Files {
		if item.Size < 0 || item.Size > maxEntryBytes {
			return fmt.Errorf("the manifest says %s is %d bytes, more than a restore extracts", item.Path, item.Size)
		}
		if total += item.Size; total > maxTotalBytes {
			return fmt.Errorf("the manifest adds up to more than %d GiB, more than a restore extracts", maxTotalBytes>>30)
		}
	}
	return nil
}

func readManifest(archive *tar.Reader) (Manifest, error) {
	header, err := archive.Next()
	if err != nil || header.Name != manifestName {
		return Manifest{}, errors.New("not a crowsnest backup (no manifest first)")
	}
	var manifest Manifest
	if err := json.NewDecoder(io.LimitReader(archive, 16<<20)).Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("reading the manifest: %w", err)
	}
	if manifest.Format < 1 || manifest.Format > FormatVersion {
		return Manifest{}, fmt.Errorf("backup format %d is not one this crowsnest reads (it reads up to %d); use a newer crowsnest", manifest.Format, FormatVersion)
	}
	return manifest, nil
}

func checkEntry(archive *tar.Reader, header *tar.Header, want map[string]File, each func(string, io.Reader) error) error {
	expected, ok := want[header.Name]
	if !ok || header.Typeflag != tar.TypeReg {
		return fmt.Errorf("unexpected entry %q (not in the manifest, a duplicate, or not a plain file)", header.Name)
	}
	if header.Size != expected.Size || header.Size > maxEntryBytes {
		return fmt.Errorf("%s is %d bytes, the manifest says %d", header.Name, header.Size, expected.Size)
	}
	digest := sha256.New()
	reader := io.TeeReader(archive, digest)
	if err := each(header.Name, reader); err != nil {
		return err
	}
	if _, err := io.Copy(io.Discard, reader); err != nil {
		return fmt.Errorf("reading %s: %w", header.Name, err)
	}
	if hex.EncodeToString(digest.Sum(nil)) != expected.SHA256 {
		return fmt.Errorf("%s does not match its checksum: the backup is damaged", header.Name)
	}
	return nil
}

// ---- Restoring --------------------------------------------------------------

// Restore puts the backup at path into target (see the file comment).
func Restore(ctx context.Context, path string, target Target) (Result, error) {
	now := time.Now
	if target.Now != nil {
		now = target.Now
	}
	if err := checkEmpty(ctx, target); err != nil {
		return Result{}, err
	}
	dataDir := filepath.Dir(target.DatabasePath)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return Result{}, fmt.Errorf("creating the data directory: %w", err)
	}
	staging, err := os.MkdirTemp(dataDir, ".restore-")
	if err != nil {
		return Result{}, fmt.Errorf("making a staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(staging) }()
	manifest, err := walk(path, func(name string, body io.Reader) error { return stage(staging, name, body) })
	if err != nil {
		return Result{}, err
	}
	schema, err := store.SnapshotSchemaVersion(ctx, filepath.Join(staging, databaseName))
	if err != nil {
		return Result{}, err
	}
	if known := store.KnownSchemaVersion(); schema > known {
		return Result{}, fmt.Errorf("the backup's database is schema version %d, newer than this crowsnest knows (%d): it was made by crowsnest %s; restore it with that version or newer", schema, known, manifest.Version)
	}
	result := Result{Manifest: manifest}
	// The pause, the pending reconciliation and the cleared task approvals go into the staged
	// database before it is installed (R5-C04): whatever happens next, a database that reaches
	// its place is a paused one that knows it was restored.
	stagedDB := filepath.Join(staging, databaseName)
	if err := markRestored(ctx, stagedDB, path, manifest, now(), &result); err != nil {
		return result, err
	}
	if err := install(ctx, staging, manifest, target, now(), &result); err != nil {
		return Result{Manifest: manifest}, err
	}
	return result, nil
}

// checkEmpty refuses a target that already holds a database with any table rows, unless forced.
// A database file that exists but was never migrated (or is empty) does not count.
func checkEmpty(ctx context.Context, target Target) error {
	if target.Force {
		return nil
	}
	inspection, err := store.Inspect(ctx, target.DatabasePath)
	if err != nil {
		return fmt.Errorf("looking at the existing database: %w", err)
	}
	if inspection.Exists && inspection.SchemaVersion > 0 {
		return fmt.Errorf("%w (%s). Restoring would replace it: stop crowsnest and pass --force (the current database is kept beside it as .before-restore-<time>)", ErrNotEmpty, target.DatabasePath)
	}
	return nil
}

// stage extracts one checked entry into the staging directory.
func stage(staging, name string, body io.Reader) error {
	path := filepath.Join(staging, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("staging %s: %w", name, err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // validName vetted the name
	if err != nil {
		return fmt.Errorf("staging %s: %w", name, err)
	}
	if _, err := io.Copy(file, io.LimitReader(body, maxEntryBytes)); err != nil {
		_ = file.Close()
		return fmt.Errorf("staging %s: %w", name, err)
	}
	return file.Close()
}

// install puts the staged restore in place, all or nothing (R5-C04). First every file outside
// the database is copied to a temporary file beside its destination, on the destination's own
// filesystem (a rename across filesystems fails, and the config and orchestrator directories are
// often on another volume than the data); any failure there removes the temporaries and leaves
// everything as it was. Then each one is renamed over its destination (a replaced file is kept as
// <name>.before-restore-<stamp>), and last the database (the replaced one kept likewise; the
// staged database is in the data directory, so that rename stays on one filesystem). A failure
// during those renames undoes the ones already made, in reverse.
func install(ctx context.Context, staging string, manifest Manifest, target Target, at time.Time, result *Result) error {
	var moves []move
	defer func() {
		for _, m := range moves {
			if m.temp != "" {
				_ = os.Remove(m.temp)
			}
		}
	}()
	for _, item := range manifest.Files {
		destination := destinationOf(item.Path, target)
		if destination == "" {
			continue
		}
		replace := target.Force || strings.HasPrefix(item.Path, attachPrefix)
		m, err := prepareFile(filepath.Join(staging, filepath.FromSlash(item.Path)), destination, replace, at)
		if err != nil {
			return err
		}
		if m.temp == "" {
			result.Skipped = append(result.Skipped, destination)
			continue
		}
		moves = append(moves, m)
	}
	database := move{temp: filepath.Join(staging, databaseName), destination: target.DatabasePath, keep: target.DatabasePath + ".before-restore-" + Stamp(at), sidecars: true}
	moves = append(moves, database)
	var done []move
	for i := range moves {
		if err := moves[i].commit(); err != nil {
			for j := len(done) - 1; j >= 0; j-- {
				done[j].undo()
			}
			return fmt.Errorf("restoring %s: %w (nothing was changed)", moves[i].destination, err)
		}
		done = append(done, moves[i])
		moves[i].temp = "" // in place: not a temporary to remove any more
	}
	for _, m := range done {
		if m.destination == target.DatabasePath {
			if m.kept {
				result.Replaced = m.keep
				// The replaced database is kept to roll back to, but scrubbed like a backup (R6-S5):
				// its sessions, token hashes, push key and notify URL are not left lying in the
				// data directory. Rolling back then means signing in again.
				if err := store.ScrubSnapshot(ctx, m.keep); err != nil {
					return fmt.Errorf("the restore is done, but scrubbing the replaced database %s failed (delete it if you do not need it): %w", m.keep, err)
				}
			}
			continue
		}
		result.Written = append(result.Written, m.destination)
	}
	return nil
}

// move is one file of the restore: a temporary beside its destination, renamed over it.
type move struct {
	temp, destination string
	keep              string // where a replaced destination is kept
	kept              bool   // a destination existed and was moved to keep
	sidecars          bool   // the database: its -wal and -shm move with it
}

// prepareFile copies staged into a temporary file in destination's directory. An existing
// destination is left alone unless replace is set (the move then has no temp).
func prepareFile(staged, destination string, replace bool, at time.Time) (move, error) {
	if _, err := os.Lstat(destination); err == nil && !replace {
		return move{}, nil
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return move{}, fmt.Errorf("checking %s: %w", destination, err)
	}
	dir := filepath.Dir(destination)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return move{}, fmt.Errorf("creating %s: %w", dir, err)
	}
	temp, err := os.CreateTemp(dir, "."+filepath.Base(destination)+".restore-")
	if err != nil {
		return move{}, fmt.Errorf("preparing %s: %w", destination, err)
	}
	m := move{temp: temp.Name(), destination: destination, keep: destination + ".before-restore-" + Stamp(at)}
	if err := copyInto(temp, staged); err != nil {
		_ = os.Remove(temp.Name())
		return move{}, fmt.Errorf("preparing %s: %w", destination, err)
	}
	return m, nil
}

// copyInto copies the file at from into to, syncs it and closes to.
func copyInto(to *os.File, from string) error {
	source, err := os.Open(from) //nolint:gosec // a staged file this restore wrote
	if err != nil {
		_ = to.Close()
		return err
	}
	defer func() { _ = source.Close() }()
	if _, err := io.Copy(to, source); err != nil {
		_ = to.Close()
		return err
	}
	if err := to.Sync(); err != nil {
		_ = to.Close()
		return err
	}
	return to.Close()
}

// commit keeps an existing destination aside and renames the temporary over it.
func (m *move) commit() error {
	if _, err := os.Lstat(m.destination); err == nil {
		if err := os.Rename(m.destination, m.keep); err != nil {
			return fmt.Errorf("keeping the current file: %w", err)
		}
		m.kept = true
		if m.sidecars {
			for _, suffix := range []string{"-wal", "-shm"} {
				_ = os.Rename(m.destination+suffix, m.keep+suffix)
			}
		}
	}
	if err := os.Rename(m.temp, m.destination); err != nil {
		if m.kept {
			_ = os.Rename(m.keep, m.destination)
			m.kept = false
		}
		return err
	}
	return nil
}

// undo reverses a committed move: the restored file goes, the kept one comes back.
func (m *move) undo() {
	_ = os.Remove(m.destination)
	if m.kept {
		_ = os.Rename(m.keep, m.destination)
		if m.sidecars {
			for _, suffix := range []string{"-wal", "-shm"} {
				_ = os.Rename(m.keep+suffix, m.destination+suffix)
			}
		}
	}
}

// destinationOf maps an archive name to its place on disk ("" for the database and manifest,
// or a tree with no target directory).
func destinationOf(name string, target Target) string {
	switch {
	case strings.HasPrefix(name, attachPrefix):
		return filepath.Join(target.AttachmentsDir, strings.TrimPrefix(name, attachPrefix))
	case strings.HasPrefix(name, "config/") && target.ConfigDir != "":
		return filepath.Join(target.ConfigDir, filepath.FromSlash(strings.TrimPrefix(name, "config/")))
	case strings.HasPrefix(name, "orchestrator/") && target.OrchestratorDir != "":
		return filepath.Join(target.OrchestratorDir, filepath.FromSlash(strings.TrimPrefix(name, "orchestrator/")))
	}
	return ""
}

// markRestored opens the staged database (running newer migrations), pauses its automation,
// records the pending reconciliation and forgets any task file approvals (R5-S3: an approval is
// about a file on this machine, and a crafted backup must not bring its own). It runs before the
// database is installed, and closes it fully, so nothing of it is left in a -wal file.
func markRestored(ctx context.Context, dbPath, file string, manifest Manifest, at time.Time, result *Result) error {
	db, err := store.Open(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("opening the restored database: %w", err)
	}
	defer func() { _ = db.Close() }()
	if result.Migrated, err = db.SchemaVersion(ctx); err != nil {
		return err
	}
	pause, _ := json.Marshal(pauseState{Paused: true, By: "restore", At: at.UTC(),
		Reason: "restored from a backup made " + manifest.CreatedAt.Format("2006-01-02 15:04 MST") + "; check the agents, then resume"})
	if err := db.SetKV(ctx, PauseKey, string(pause)); err != nil {
		return fmt.Errorf("pausing the restored instance: %w", err)
	}
	abs, _ := filepath.Abs(file)
	pending, _ := json.Marshal(Pending{At: at.UTC(), File: abs, CreatedAt: manifest.CreatedAt, Version: manifest.Version, Name: manifest.Name})
	if err := db.SetKV(ctx, PendingKey, string(pending)); err != nil {
		return fmt.Errorf("recording the restore: %w", err)
	}
	if err := db.ClearTaskReviews(ctx); err != nil {
		return err
	}
	// R6-S5: a backup is not the human's word on this machine. Its approval rules would allow
	// calls without asking once resumed, and its "already read" marks would hide its role files
	// from review: the rules are kept but revoked (listed, to add again), the marks dropped.
	if result.RevokedRules, err = db.RevokeAllApprovalRules(ctx, at, "(revoked by a restore: add it again if you want it)"); err != nil {
		return err
	}
	if result.ForgotSeen, err = db.DeleteKVPrefix(ctx, "instructions.file-seen."); err != nil {
		return err
	}
	if _, err := db.DeleteKVSuffix(ctx, "remote_control"); err != nil {
		return err
	}
	// R7-S4: the same for what crowsnest acts on by itself. A custom check command is a shell
	// command crowsnest runs on every done: it goes back to automatic, the backup's command kept
	// aside for the human to use again. The instruction layers become agents' system prompts:
	// each is withheld until the human has read it and applied it.
	if result.CheckCommands, err = db.SetAsideCheckCommands(ctx); err != nil {
		return err
	}
	if result.RestoredLayers, err = db.MarkLayersRestored(ctx); err != nil {
		return err
	}
	return db.Close()
}
