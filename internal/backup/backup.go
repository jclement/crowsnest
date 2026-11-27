// Package backup writes and restores crowsnest backups (DESIGN.md 4.17): one gzipped tar file
// with a manifest first, then a consistent, credential-free copy of the database, the attachment
// blobs, and the files beside the config that steer agents (role prompts, task files, the risk
// rules). Every entry's size and SHA-256 are in the manifest, and a restore checks all of them
// before anything is written into place.
//
// What a backup deliberately leaves out is written into the manifest too (NotIncluded), so the
// file says it: Claude's transcripts (they belong to Claude Code, under ~/.claude, and can be
// large), herdr's state (sessions, panes and their scrollback live in herdr), the repositories
// and worktrees jobs worked in, the config file itself (it may hold the password), and every
// credential (store.ScrubSnapshot). A restored instance therefore asks devices to subscribe to
// notifications again and every browser to sign in again.
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
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

// FormatVersion is the archive layout's version. A restore refuses a newer one.
const FormatVersion = 1

// Entry names inside the archive.
const (
	manifestName = "manifest.json"
	databaseName = "crowsnest.db"
	attachPrefix = "attachments/"
)

// maxEntryBytes bounds one entry a restore will extract: past it the file is not a crowsnest
// backup (attachments are capped at --attachment-max-mb, far below this).
const maxEntryBytes = 8 << 30

// NotIncluded is what every backup states it does not carry.
var NotIncluded = []string{
	"Claude Code transcripts (~/.claude/projects): the conversations themselves",
	"herdr state: sessions, workspaces, panes and their scrollback",
	"repositories and worktrees jobs worked in",
	"the config file (it may hold the password) and any password in clear",
	"credentials: login sessions, agent tokens, API tokens, tokens for other machines, notification action tokens, the Web Push key and subscriptions, the notify URL",
}

// File is one manifest entry.
type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Manifest describes a backup. It is the archive's first entry.
type Manifest struct {
	Format        int       `json:"format"`
	CreatedAt     time.Time `json:"created_at"`
	Version       string    `json:"crowsnest_version"`
	SchemaVersion int       `json:"schema_version"`
	Name          string    `json:"instance_name"`
	Session       string    `json:"session"`
	Reason        string    `json:"reason"` // manual, scheduled, download
	Files         []File    `json:"files"`
	NotIncluded   []string  `json:"not_included"`
}

// Extra is a file from outside the data directory to carry, under Archive (a relative name in
// the archive's config/ or orchestrator/ tree). A missing Source is skipped.
type Extra struct {
	Source  string
	Archive string
}

// Source is what a backup is made from.
type Source struct {
	DB             *store.Store
	AttachmentsDir string  // <data-dir>/attachments
	Extras         []Extra // role prompts, task files, risk rules
	Version        string
	Name           string
	Session        string
	Reason         string
	// TempDir is where the database copy is staged (it must be private: the data directory).
	TempDir string
	Now     func() time.Time
}

// ---- Writing ----------------------------------------------------------------

// Create writes a backup of src to path (created 0600; an existing file is replaced only once
// the new one is complete) and returns its manifest.
func Create(ctx context.Context, src Source, path string) (Manifest, error) {
	partial := path + ".partial"
	out, err := os.OpenFile(partial, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // the path is the user's choice or the backup directory
	if err != nil {
		return Manifest{}, fmt.Errorf("creating %s: %w", partial, err)
	}
	manifest, err := Write(ctx, src, out)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(partial)
		return Manifest{}, err
	}
	if err := os.Rename(partial, path); err != nil {
		_ = os.Remove(partial)
		return Manifest{}, fmt.Errorf("moving the backup into place at %s: %w", path, err)
	}
	return manifest, nil
}

// Write streams a backup of src to w (the browser's download uses this directly).
func Write(ctx context.Context, src Source, w io.Writer) (Manifest, error) {
	now := time.Now
	if src.Now != nil {
		now = src.Now
	}
	staging, err := os.MkdirTemp(src.TempDir, ".backup-")
	if err != nil {
		return Manifest{}, fmt.Errorf("making a staging directory for the backup: %w", err)
	}
	defer func() { _ = os.RemoveAll(staging) }()
	snapshot := filepath.Join(staging, databaseName)
	if err := src.DB.VacuumInto(ctx, snapshot); err != nil {
		return Manifest{}, err
	}
	if err := store.ScrubSnapshot(ctx, snapshot); err != nil {
		return Manifest{}, err
	}
	schema, err := store.SnapshotSchemaVersion(ctx, snapshot)
	if err != nil {
		return Manifest{}, err
	}
	entries, err := collect(snapshot, src)
	if err != nil {
		return Manifest{}, err
	}
	manifest := Manifest{
		Format: FormatVersion, CreatedAt: now().UTC(), Version: src.Version, SchemaVersion: schema,
		Name: src.Name, Session: src.Session, Reason: src.Reason, NotIncluded: NotIncluded,
	}
	for i := range entries {
		file, err := hashFile(entries[i].source)
		if err != nil {
			return Manifest{}, err
		}
		file.Path = entries[i].archive
		manifest.Files = append(manifest.Files, file)
	}
	return manifest, writeArchive(ctx, w, manifest, entries)
}

// entry pairs a file on disk with its name in the archive.
type entry struct{ source, archive string }

// collect lists what goes into the archive: the database copy, every attachment blob (and
// thumbnail), then the extras that exist.
func collect(snapshot string, src Source) ([]entry, error) {
	entries := []entry{{source: snapshot, archive: databaseName}}
	if src.AttachmentsDir != "" {
		names, err := os.ReadDir(src.AttachmentsDir)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("listing attachments: %w", err)
		}
		for _, name := range names {
			if name.Type().IsRegular() && blobName.MatchString(name.Name()) {
				entries = append(entries, entry{source: filepath.Join(src.AttachmentsDir, name.Name()), archive: attachPrefix + name.Name()})
			}
		}
	}
	for _, extra := range src.Extras {
		info, err := os.Lstat(extra.Source)
		if err != nil || !info.Mode().IsRegular() || !extraName.MatchString(extra.Archive) {
			continue // missing, a link or a directory: not ours to follow
		}
		entries = append(entries, entry{source: extra.Source, archive: extra.Archive})
	}
	return entries, nil
}

func hashFile(path string) (File, error) {
	file, err := os.Open(path) //nolint:gosec // paths come from collect
	if err != nil {
		return File{}, fmt.Errorf("reading %s for the backup: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	digest := sha256.New()
	size, err := io.Copy(digest, file)
	if err != nil {
		return File{}, fmt.Errorf("reading %s for the backup: %w", path, err)
	}
	return File{Size: size, SHA256: hex.EncodeToString(digest.Sum(nil))}, nil
}

func writeArchive(ctx context.Context, w io.Writer, manifest Manifest, entries []entry) error {
	zipped := gzip.NewWriter(w)
	archive := tar.NewWriter(zipped)
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the manifest: %w", err)
	}
	if err := writeEntry(archive, manifestName, int64(len(body)), manifest.CreatedAt, strings.NewReader(string(body))); err != nil {
		return err
	}
	for i, item := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := copyEntry(archive, item, manifest.Files[i], manifest.CreatedAt); err != nil {
			return err
		}
	}
	if err := archive.Close(); err != nil {
		return fmt.Errorf("finishing the backup: %w", err)
	}
	if err := zipped.Close(); err != nil {
		return fmt.Errorf("finishing the backup: %w", err)
	}
	return nil
}

// copyEntry writes one file, refusing it if it changed since it was hashed: attachment blobs
// are content-addressed and never rewritten, so a change means something is wrong.
func copyEntry(archive *tar.Writer, item entry, want File, at time.Time) error {
	file, err := os.Open(item.source)
	if err != nil {
		return fmt.Errorf("reading %s for the backup: %w", item.source, err)
	}
	defer func() { _ = file.Close() }()
	digest := sha256.New()
	if err := writeEntry(archive, item.archive, want.Size, at, io.TeeReader(io.LimitReader(file, want.Size), digest)); err != nil {
		return err
	}
	if hex.EncodeToString(digest.Sum(nil)) != want.SHA256 {
		return fmt.Errorf("%s changed while the backup was being written; try again", item.source)
	}
	return nil
}

func writeEntry(archive *tar.Writer, name string, size int64, at time.Time, body io.Reader) error {
	header := &tar.Header{Name: name, Mode: 0o600, Size: size, ModTime: at, Typeflag: tar.TypeReg, Format: tar.FormatPAX}
	if err := archive.WriteHeader(header); err != nil {
		return fmt.Errorf("writing %s into the backup: %w", name, err)
	}
	written, err := io.Copy(archive, body)
	if err != nil {
		return fmt.Errorf("writing %s into the backup: %w", name, err)
	}
	if written != size {
		return fmt.Errorf("%s changed size while the backup was being written; try again", name)
	}
	return nil
}

// ---- Names ------------------------------------------------------------------

// blobName is an attachment blob or its thumbnail, as internal/attach names them.
var blobName = regexp.MustCompile(`^[0-9a-f]{64}(\.thumb\.jpg)?$`)

// extraName is what an extra may be called in the archive: a markdown file in config/roles or
// config/tasks or orchestrator/tasks, or config/risk.yaml. Nothing else is written by a restore.
var extraName = regexp.MustCompile(`^(config/(roles|tasks)/[A-Za-z0-9][A-Za-z0-9._-]{0,99}\.md|orchestrator/tasks/[A-Za-z0-9][A-Za-z0-9._-]{0,99}\.md|config/risk\.yaml)$`)

// validName reports whether an archive entry is one a restore may write.
func validName(name string) bool {
	switch {
	case name == manifestName, name == databaseName:
		return true
	case strings.HasPrefix(name, attachPrefix):
		return blobName.MatchString(strings.TrimPrefix(name, attachPrefix))
	default:
		return extraName.MatchString(name)
	}
}

// ---- Rotation ---------------------------------------------------------------

// Prune deletes the oldest files in dir whose names start with prefix and end with suffix,
// keeping the newest keep. Names carry a sortable UTC timestamp, so name order is age order.
// It returns the paths removed.
func Prune(dir, prefix, suffix string, keep int) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", dir, err)
	}
	var names []string
	for _, item := range entries {
		if item.Type().IsRegular() && strings.HasPrefix(item.Name(), prefix) && strings.HasSuffix(item.Name(), suffix) {
			names = append(names, item.Name())
		}
	}
	sort.Strings(names)
	var removed []string
	for len(names) > keep {
		path := filepath.Join(dir, names[0])
		if err := os.Remove(path); err != nil {
			return removed, fmt.Errorf("removing old backup %s: %w", path, err)
		}
		removed, names = append(removed, path), names[1:]
	}
	return removed, nil
}

// Stamp is the timestamp backups put in their names: sortable, UTC, no colons.
func Stamp(at time.Time) string { return at.UTC().Format("20060102T150405Z") }
