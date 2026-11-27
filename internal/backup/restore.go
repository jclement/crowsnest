// Restoring a backup (DESIGN.md 4.17). A restore is staged in full first: every entry is
// extracted into a private staging directory under the data directory and checked against the
// manifest (name allowed, size and SHA-256 equal, nothing missing, nothing extra), and the
// database's schema is checked against this binary, before a single file is moved into place.
//
// It refuses a data directory that already holds a database with anything in it, unless forced;
// a forced restore keeps the replaced database beside it (`<db>.before-restore-<stamp>`), so even
// that is undoable by hand. The restored database is then opened once, which runs any newer
// migrations, and left PAUSED (kv `pause.state`, the pause switch's own record) with a
// `restore.pending` row that tells the next start to take stock of the agents herdr really has
// before anything is dispatched.

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
	if err := place(staging, manifest, target, now(), &result); err != nil {
		return result, err
	}
	if err := markRestored(ctx, target.DatabasePath, path, manifest, now(), &result); err != nil {
		return result, err
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

// place moves the staged files into the target: the database (keeping a replaced one), the
// attachment blobs (content-addressed, so an existing one is already right), and the config
// files (an existing one is left alone unless forced).
func place(staging string, manifest Manifest, target Target, at time.Time, result *Result) error {
	if _, err := os.Stat(target.DatabasePath); err == nil {
		result.Replaced = target.DatabasePath + ".before-restore-" + Stamp(at)
		if err := os.Rename(target.DatabasePath, result.Replaced); err != nil {
			return fmt.Errorf("keeping the current database: %w", err)
		}
		for _, suffix := range []string{"-wal", "-shm"} {
			_ = os.Rename(target.DatabasePath+suffix, result.Replaced+suffix)
		}
	}
	if err := os.Rename(filepath.Join(staging, databaseName), target.DatabasePath); err != nil {
		return fmt.Errorf("moving the database into place: %w", err)
	}
	for _, item := range manifest.Files {
		destination := destinationOf(item.Path, target)
		if destination == "" {
			continue
		}
		written, err := placeFile(filepath.Join(staging, filepath.FromSlash(item.Path)), destination, target.Force || strings.HasPrefix(item.Path, attachPrefix))
		if err != nil {
			return err
		}
		if written {
			result.Written = append(result.Written, destination)
		} else {
			result.Skipped = append(result.Skipped, destination)
		}
	}
	return nil
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

// placeFile moves staged to destination. An existing destination is replaced only when replace
// is set; it reports whether it wrote.
func placeFile(staged, destination string, replace bool) (bool, error) {
	if _, err := os.Lstat(destination); err == nil && !replace {
		return false, nil
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("checking %s: %w", destination, err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return false, fmt.Errorf("creating %s: %w", filepath.Dir(destination), err)
	}
	if err := os.Rename(staged, destination); err != nil {
		return false, fmt.Errorf("restoring %s: %w", destination, err)
	}
	return true, nil
}

// markRestored opens the restored database (running newer migrations), pauses its automation
// and records the pending reconciliation.
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
	return nil
}
