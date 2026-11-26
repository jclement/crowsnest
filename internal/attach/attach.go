// Package attach is crowsnest's attachments (DESIGN.md 8.6): files the human pastes, drops or
// picks in the web app (a screenshot, a log, a photo from a phone), and files agents attach to a
// question or a finished job (a report, a screenshot of what they built).
//
// Agents read files from disk, so an attachment sent to an agent becomes a file it can read: a
// copy in that agent's inbox directory under the state directory (<state>/jobs/j_N/attachments,
// <state>/orchestrator/attachments), named <attachment id>-<cleaned name>, and one line typed
// with the message: "Attached: <path> (image/png, 240 KB)". The id in the name is how the web
// app finds the attachment again from a line in a transcript, without the server ever serving a
// path.
//
// Storage: each file's bytes are kept once, under <data>/attachments/<sha256> (0600, in a 0700
// directory; never a name a client chose), and the attachments table says what each upload is
// and who it belongs to. Limits: a size per file, a count per message, and a total quota; when
// an upload would pass the quota, the oldest files nobody needs any more (released ones, then
// uploads abandoned in a composer) are deleted to make room, else the upload is refused.
package attach

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

// Defaults for Limits (DESIGN.md 8.6).
const (
	DefaultMaxFileBytes  = 25 << 20 // 25 MB: a long screen recording is a file share's job
	DefaultQuotaBytes    = 1 << 30  // 1 GB across every attachment
	DefaultMaxPerMessage = 10
)

// Retention: when unneeded files become candidates for cleanup.
const (
	// pendingGrace is how long an upload may sit in a composer before a full quota may delete it.
	pendingGrace = time.Hour
	// pendingMaxAge deletes an upload that was never sent, whatever the quota.
	pendingMaxAge = 24 * time.Hour
	// conversationKeep is how long a file sent to the orchestrator or a plain pane stays live
	// (they have no settling event); afterwards it is released like a finished job's.
	conversationKeep = 7 * 24 * time.Hour
	// sweepInterval is how often Run sweeps.
	sweepInterval = 10 * time.Minute
	// cleanupPage is how many candidates one cleanup pass considers.
	cleanupPage = 200
)

// Origin says who supplied a file.
type Origin string

// Origins.
const (
	OriginHuman Origin = "human"
	OriginAgent Origin = "agent"
)

// Owner kinds (the attachments table's owner_kind).
const (
	OwnerJob          = "job"
	OwnerOrchestrator = "orchestrator"
	OwnerPane         = "pane"
	OwnerQuestion     = "question"
)

// Roles: what a file was for.
const (
	RoleBrief   = "brief"   // given with a new job
	RoleMessage = "message" // sent with a job message
	RolePrompt  = "prompt"  // sent with a Chat prompt
	RoleAnswer  = "answer"  // sent with an answer (ref: the question)
	RoleAsk     = "ask"     // an agent's, on its question
	RoleResult  = "result"  // an agent's, on its finished job
)

// Errors a caller maps to a response.
var (
	ErrNotFound   = errors.New("no such attachment")
	ErrNotPending = errors.New("that attachment was already sent; upload it again to send it again")
	ErrQuota      = errors.New("attachment storage is full")
	ErrTooMany    = errors.New("too many attachments")
	ErrEmpty      = errors.New("the file is empty")
)

// TooLargeError refuses a file over the per-file limit.
type TooLargeError struct{ Limit int64 }

func (e *TooLargeError) Error() string {
	return fmt.Sprintf("the file is larger than %s, the most crowsnest takes", FormatSize(e.Limit))
}

// Limits bound what is stored.
type Limits struct {
	MaxFileBytes  int64 // per file
	QuotaBytes    int64 // all blobs together
	MaxPerMessage int   // files on one message, job, answer, question or result
}

// withDefaults fills zero fields.
func (l Limits) withDefaults() Limits {
	if l.MaxFileBytes <= 0 {
		l.MaxFileBytes = DefaultMaxFileBytes
	}
	if l.QuotaBytes <= 0 {
		l.QuotaBytes = DefaultQuotaBytes
	}
	if l.MaxPerMessage <= 0 {
		l.MaxPerMessage = DefaultMaxPerMessage
	}
	return l
}

// Options configure a Service. Store and Dir are required.
type Options struct {
	Store *store.Store
	// Dir holds the blobs: <data-dir>/attachments. Created 0700.
	Dir    string
	Limits Limits
	Logger *slog.Logger
	Now    func() time.Time
	// Changed, when set, is told an owner's files changed ("job:j_3", "question:q_7",
	// "orchestrator:", "pending:"), so the web app can refetch. It must not block.
	Changed func(owner string)
}

// Service stores and hands out attachments. Safe for concurrent use.
type Service struct {
	opts Options
	// mu serialises the quota's arithmetic: committing an upload, cleaning up, deleting.
	mu sync.Mutex
}

// Attachment is one file as the APIs show it.
type Attachment struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	// Image is true when the web app may show it as a picture; Thumb when a thumbnail exists.
	Image bool `json:"image"`
	Thumb bool `json:"thumb"`
	// Inline is true when GET serves it for display rather than as a download.
	Inline     bool       `json:"inline"`
	Origin     string     `json:"origin"`
	OwnerKind  string     `json:"owner_kind,omitempty"`
	OwnerID    string     `json:"owner_id,omitempty"`
	Role       string     `json:"role,omitempty"`
	Ref        string     `json:"ref,omitempty"`
	Path       string     `json:"path,omitempty"` // the agent's copy, when one was made
	CreatedAt  time.Time  `json:"created_at"`
	ReleasedAt *time.Time `json:"released_at,omitempty"`
}

// Inbox is where copies for one agent go: a directory under the state directory. Base is the
// directory crowsnest owns (the state directory); no component from Base down to Dir may be a
// symbolic link, so an agent cannot redirect crowsnest's copies by planting one.
type Inbox struct {
	Kind string // OwnerJob, OwnerOrchestrator or OwnerPane
	ID   string // j_N, "", or the pane id
	Base string
	Dir  string
}

// New returns a Service, creating its directory.
func New(opts Options) (*Service, error) {
	if opts.Store == nil || opts.Dir == "" {
		return nil, errors.New("attach: Store and Dir are required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	opts.Limits = opts.Limits.withDefaults()
	if err := privateDir(opts.Dir); err != nil {
		return nil, err
	}
	return &Service{opts: opts}, nil
}

// Limits returns the limits in force.
func (s *Service) Limits() Limits { return s.opts.Limits }

// ---- Upload ------------------------------------------------------------------------------

// Upload stores a file from the browser (origin human) as a pending attachment: it has no owner
// until a send names it. name is only a label.
func (s *Service) Upload(ctx context.Context, name string, body io.Reader) (Attachment, error) {
	return s.store(ctx, name, body, OriginHuman, "")
}

// store streams body to a temporary file while hashing it, sniffs its type, makes room under the
// quota and commits the blob and its row.
func (s *Service) store(ctx context.Context, name string, body io.Reader, origin Origin, pane string) (Attachment, error) {
	temp, err := os.CreateTemp(s.opts.Dir, ".upload-*")
	if err != nil {
		return Attachment{}, fmt.Errorf("creating an upload file: %w", err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }() // a no-op once renamed
	hash := sha256.New()
	head := &headBuffer{limit: sniffBytes}
	written, err := io.Copy(io.MultiWriter(temp, hash, head), io.LimitReader(body, s.opts.Limits.MaxFileBytes+1))
	closeErr := temp.Close()
	switch {
	case err != nil:
		return Attachment{}, fmt.Errorf("reading the upload: %w", err)
	case closeErr != nil:
		return Attachment{}, fmt.Errorf("writing the upload: %w", closeErr)
	case written > s.opts.Limits.MaxFileBytes:
		return Attachment{}, &TooLargeError{Limit: s.opts.Limits.MaxFileBytes}
	case written == 0:
		return Attachment{}, ErrEmpty
	}
	name = CleanName(name)
	record := store.AttachmentRecord{
		ID: newID(), Name: name, Type: Sniff(head.bytes, name), Size: written, SHA256: hex.EncodeToString(hash.Sum(nil)),
		Origin: string(origin), OriginPane: pane, CreatedAt: s.opts.Now().UTC(),
	}
	if err := s.commit(ctx, &record, tempPath); err != nil {
		return Attachment{}, err
	}
	s.changed("pending:")
	return view(record), nil
}

// commit makes room, moves the blob into place (unless an identical one is already there), makes
// its thumbnail and inserts the row, under the quota lock.
func (s *Service) commit(ctx context.Context, record *store.AttachmentRecord, tempPath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	known, thumb, err := s.opts.Store.BlobKnown(ctx, record.SHA256)
	if err != nil {
		return err
	}
	blob := s.blobPath(record.SHA256)
	if _, statErr := os.Stat(blob); statErr != nil {
		known = false // the row outlived its file somehow: write it again
	}
	if !known {
		if err := s.makeRoomLocked(ctx, record.Size); err != nil {
			return err
		}
		if err := os.Rename(tempPath, blob); err != nil {
			return fmt.Errorf("storing the upload: %w", err)
		}
		if thumbTypes[record.Type] {
			made, err := makeThumb(blob, s.thumbPath(record.SHA256))
			if err != nil {
				s.opts.Logger.Warn("making a thumbnail failed; the attachment has none", "attachment", record.ID, "err", err)
			}
			thumb = made
		}
	}
	record.HasThumb = thumb
	return s.opts.Store.InsertAttachment(ctx, *record)
}

// headBuffer keeps the first limit bytes written to it, for sniffing.
type headBuffer struct {
	bytes []byte
	limit int
}

func (h *headBuffer) Write(p []byte) (int, error) {
	if room := h.limit - len(h.bytes); room > 0 {
		h.bytes = append(h.bytes, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

// ---- Reading -----------------------------------------------------------------------------

// Get returns one attachment.
func (s *Service) Get(ctx context.Context, id string) (Attachment, error) {
	record, err := s.record(ctx, id)
	if err != nil {
		return Attachment{}, err
	}
	return view(record), nil
}

// Open returns an attachment and its bytes. The caller closes the file.
func (s *Service) Open(ctx context.Context, id string) (Attachment, *os.File, error) {
	record, err := s.record(ctx, id)
	if err != nil {
		return Attachment{}, nil, err
	}
	file, err := os.Open(s.blobPath(record.SHA256)) //nolint:gosec // built from a hash in the database
	if errors.Is(err, fs.ErrNotExist) {
		return Attachment{}, nil, ErrNotFound
	}
	if err != nil {
		return Attachment{}, nil, fmt.Errorf("opening attachment %s: %w", id, err)
	}
	return view(record), file, nil
}

// OpenThumb returns an image attachment's thumbnail (a JPEG), or ErrNotFound when it has none.
func (s *Service) OpenThumb(ctx context.Context, id string) (*os.File, error) {
	record, err := s.record(ctx, id)
	if err != nil {
		return nil, err
	}
	if !record.HasThumb {
		return nil, ErrNotFound
	}
	file, err := os.Open(s.thumbPath(record.SHA256)) //nolint:gosec // built from a hash in the database
	if err != nil {
		return nil, ErrNotFound
	}
	return file, nil
}

// List returns an owner's attachments (role "" for all), oldest first.
func (s *Service) List(ctx context.Context, kind, ownerID, role string) ([]Attachment, error) {
	records, err := s.opts.Store.ListAttachmentsByOwner(ctx, kind, ownerID, role)
	if err != nil {
		return nil, err
	}
	return views(records), nil
}

// ListByRef returns the attachments of a role that point at ref (the files sent with the answer
// to a question, wherever they were copied).
func (s *Service) ListByRef(ctx context.Context, role, ref string) ([]Attachment, error) {
	records, err := s.opts.Store.ListAttachmentsByRef(ctx, role, ref)
	if err != nil {
		return nil, err
	}
	return views(records), nil
}

func (s *Service) record(ctx context.Context, id string) (store.AttachmentRecord, error) {
	if !ValidID(id) {
		return store.AttachmentRecord{}, ErrNotFound
	}
	record, err := s.opts.Store.GetAttachment(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return store.AttachmentRecord{}, ErrNotFound
	}
	return record, err
}

// ---- Sending -----------------------------------------------------------------------------

// CheckPending verifies that ids name uploads still waiting to be sent, and not too many, before
// a caller commits to a send that names them.
func (s *Service) CheckPending(ctx context.Context, ids []string) error {
	if len(ids) > s.opts.Limits.MaxPerMessage {
		return fmt.Errorf("%w: at most %d files at once", ErrTooMany, s.opts.Limits.MaxPerMessage)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return fmt.Errorf("attachment %s is named twice", id)
		}
		seen[id] = true
		record, err := s.record(ctx, id)
		if err != nil {
			return fmt.Errorf("attachment %s: %w", id, err)
		}
		if record.OwnerKind != "" {
			return fmt.Errorf("attachment %s: %w", id, ErrNotPending)
		}
	}
	return nil
}

// Deliver copies pending uploads into an agent's inbox and gives them their owner. It returns the
// attachments with their paths, in the order given; Lines turns them into what is typed. A file
// that fails part-way leaves the ones already delivered delivered (their copies are real).
func (s *Service) Deliver(ctx context.Context, ids []string, inbox Inbox, role, ref string) ([]Attachment, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if err := s.CheckPending(ctx, ids); err != nil {
		return nil, err
	}
	if err := safeInbox(inbox); err != nil {
		return nil, err
	}
	out := make([]Attachment, 0, len(ids))
	for _, id := range ids {
		record, err := s.record(ctx, id)
		if err != nil {
			return out, err
		}
		path := filepath.Join(inbox.Dir, CopyName(record.ID, record.Name))
		if err := s.copyBlob(record.SHA256, path); err != nil {
			return out, err
		}
		owner := store.AttachmentRecord{OwnerKind: inbox.Kind, OwnerID: inbox.ID, Role: role, Ref: ref, Path: path}
		if err := s.opts.Store.AssignAttachment(ctx, id, owner, s.opts.Now()); err != nil {
			_ = os.Remove(path)
			if errors.Is(err, store.ErrNotFound) {
				err = fmt.Errorf("attachment %s: %w", id, ErrNotPending)
			}
			return out, err
		}
		record.OwnerKind, record.OwnerID, record.Role, record.Ref, record.Path = inbox.Kind, inbox.ID, role, ref, path
		out = append(out, view(record))
	}
	s.changed(inbox.Kind + ":" + inbox.ID)
	return out, nil
}

// Undeliver takes back a delivery whose message provably never reached the agent (a dialog, a
// draft, an agent not found): the copies go and the uploads are pending again, so the composer's
// retry or "Send anyway" can name them once more.
func (s *Service) Undeliver(ctx context.Context, attachments []Attachment) {
	for _, attachment := range attachments {
		if err := s.opts.Store.UnassignAttachment(ctx, attachment.ID); err != nil {
			s.opts.Logger.Warn("taking back an attachment after a send that typed nothing", "attachment", attachment.ID, "err", err)
			continue
		}
		removeCopy(store.AttachmentRecord{ID: attachment.ID, Path: attachment.Path})
	}
}

// Adopt gives pending attachments an owner without copying them anywhere: an agent's files on
// its question or its finished job, which the human reads in the browser.
func (s *Service) Adopt(ctx context.Context, ids []string, kind, ownerID, role string) error {
	for _, id := range ids {
		owner := store.AttachmentRecord{OwnerKind: kind, OwnerID: ownerID, Role: role}
		if err := s.opts.Store.AssignAttachment(ctx, id, owner, s.opts.Now()); err != nil {
			return fmt.Errorf("attaching %s to %s %s: %w", id, kind, ownerID, err)
		}
	}
	if len(ids) > 0 {
		s.changed(kind + ":" + ownerID)
	}
	return nil
}

// ImportAgentFiles stores files an agent named (origin agent, pending until Adopt), each read
// only if it lies inside one of roots (its working directory, its job directory). paths must be
// absolute. Nothing is stored unless every path passes, so a refusal never leaves half a set.
func (s *Service) ImportAgentFiles(ctx context.Context, paths, roots []string, pane string) ([]Attachment, error) {
	if len(paths) > s.opts.Limits.MaxPerMessage {
		return nil, fmt.Errorf("%w: at most %d files", ErrTooMany, s.opts.Limits.MaxPerMessage)
	}
	if len(roots) == 0 {
		return nil, errors.New("crowsnest does not know this agent's directories, so it cannot attach files for it")
	}
	type opened struct {
		path string
		file *os.File
	}
	files := make([]opened, 0, len(paths))
	defer func() {
		for _, item := range files {
			_ = item.file.Close()
		}
	}()
	for _, path := range paths {
		file, _, err := openConfined(path, roots, s.opts.Limits.MaxFileBytes)
		if err != nil {
			return nil, err
		}
		files = append(files, opened{path: path, file: file})
	}
	out := make([]Attachment, 0, len(files))
	for _, item := range files {
		attachment, err := s.store(ctx, filepath.Base(item.path), item.file, OriginAgent, pane)
		if err != nil {
			s.discardAll(ctx, out)
			return nil, fmt.Errorf("%s: %w", item.path, err)
		}
		out = append(out, attachment)
	}
	return out, nil
}

// Discard deletes a pending upload (the composer's remove button, or a send that failed after
// importing). An attachment that already has an owner is not touched.
func (s *Service) Discard(ctx context.Context, id string) error {
	record, err := s.record(ctx, id)
	if err != nil {
		return err
	}
	if record.OwnerKind != "" {
		return ErrNotPending
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteLocked(ctx, record)
}

func (s *Service) discardAll(ctx context.Context, attachments []Attachment) {
	for _, attachment := range attachments {
		_ = s.Discard(ctx, attachment.ID)
	}
}

// ---- Releasing and cleanup ---------------------------------------------------------------

// Release marks an owner's files as no longer needed (its job or question settled) and removes
// the copies made for its agent. The blobs stay, viewable, until the quota needs the room.
func (s *Service) Release(ctx context.Context, kind, ownerID string) error {
	records, err := s.opts.Store.ReleaseAttachments(ctx, kind, ownerID, s.opts.Now())
	if err != nil {
		return err
	}
	for _, record := range records {
		removeCopy(record)
	}
	if len(records) > 0 {
		s.changed(kind + ":" + ownerID)
	}
	return nil
}

// Sweep runs the periodic upkeep: uploads never sent for a day are deleted, files sent to the
// orchestrator or a plain pane a week ago are released, and a quota passed (the limit lowered
// across a restart) is brought back under.
func (s *Service) Sweep(ctx context.Context) {
	now := s.opts.Now()
	stale, err := s.opts.Store.StalePendingAttachments(ctx, now.Add(-pendingMaxAge))
	if err != nil {
		s.opts.Logger.Warn("listing abandoned uploads", "err", err)
	}
	s.mu.Lock()
	for _, record := range stale {
		if err := s.deleteLocked(ctx, record); err != nil {
			s.opts.Logger.Warn("deleting an abandoned upload", "attachment", record.ID, "err", err)
		}
	}
	s.mu.Unlock()
	released, err := s.opts.Store.ReleaseAttachmentsOlderThan(ctx, []string{OwnerOrchestrator, OwnerPane}, now.Add(-conversationKeep), now)
	if err != nil {
		s.opts.Logger.Warn("releasing old conversation attachments", "err", err)
	}
	for _, record := range released {
		removeCopy(record)
	}
	s.mu.Lock()
	if err := s.makeRoomLocked(ctx, 0); err != nil && !errors.Is(err, ErrQuota) {
		s.opts.Logger.Warn("bringing attachments under the quota", "err", err)
	}
	s.mu.Unlock()
}

// Run sweeps every sweepInterval until ctx ends.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	s.Sweep(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Sweep(ctx)
		}
	}
}

// makeRoomLocked deletes cleanup candidates, best first, until incoming more bytes fit under the
// quota, or reports ErrQuota. s.mu is held.
func (s *Service) makeRoomLocked(ctx context.Context, incoming int64) error {
	quota := s.opts.Limits.QuotaBytes
	used, err := s.opts.Store.BlobBytes(ctx)
	if err != nil {
		return err
	}
	if used+incoming <= quota {
		return nil
	}
	candidates, err := s.opts.Store.CleanupCandidates(ctx, s.opts.Now().Add(-pendingGrace), cleanupPage)
	if err != nil {
		return err
	}
	for _, record := range candidates {
		if err := s.deleteLocked(ctx, record); err != nil {
			return err
		}
		if used, err = s.opts.Store.BlobBytes(ctx); err != nil {
			return err
		}
		if used+incoming <= quota {
			s.opts.Logger.Info("deleted old attachments to make room", "quota", FormatSize(quota))
			return nil
		}
	}
	return fmt.Errorf("%w: %s of %s is in use by files agents still hold; finish or cancel some jobs, or raise --attachment-quota-mb",
		ErrQuota, FormatSize(used), FormatSize(quota))
}

// deleteLocked removes a row, its agent copy and, when no other row names it, its blob and
// thumbnail. s.mu is held.
func (s *Service) deleteLocked(ctx context.Context, record store.AttachmentRecord) error {
	if err := s.opts.Store.DeleteAttachment(ctx, record.ID); err != nil {
		return err
	}
	removeCopy(record)
	inUse, err := s.opts.Store.BlobInUse(ctx, record.SHA256)
	if err != nil {
		return err
	}
	if !inUse {
		_ = os.Remove(s.blobPath(record.SHA256))
		_ = os.Remove(s.thumbPath(record.SHA256))
	}
	s.changed(record.OwnerKind + ":" + record.OwnerID)
	return nil
}

// removeCopy deletes the copy made for an agent, if the recorded path really is one of ours (its
// name starts with the attachment's id).
func removeCopy(record store.AttachmentRecord) {
	if record.Path == "" || !strings.HasPrefix(filepath.Base(record.Path), record.ID+"-") {
		return
	}
	_ = os.Remove(record.Path)
}

func (s *Service) changed(owner string) {
	if s.opts.Changed != nil {
		s.opts.Changed(owner)
	}
}

// ---- Files on disk -----------------------------------------------------------------------

func (s *Service) blobPath(sha string) string  { return filepath.Join(s.opts.Dir, sha) }
func (s *Service) thumbPath(sha string) string { return filepath.Join(s.opts.Dir, sha+".thumb.jpg") }

// copyBlob writes a fresh 0600 copy of a blob at target, created exclusively and without
// following a link, after removing whatever was there.
func (s *Service) copyBlob(sha, target string) error {
	source, err := os.Open(s.blobPath(sha)) //nolint:gosec // built from a hash in the database
	if err != nil {
		return fmt.Errorf("reading an attachment: %w", err)
	}
	defer func() { _ = source.Close() }()
	if err := os.Remove(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("replacing %s: %w", target, err)
	}
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600) //nolint:gosec // under crowsnest's state directory, checked by safeInbox
	if err != nil {
		return fmt.Errorf("creating %s: %w", target, err)
	}
	if _, err := io.Copy(out, source); err != nil {
		_ = out.Close()
		return fmt.Errorf("writing %s: %w", target, err)
	}
	return out.Close()
}

// safeInbox creates an inbox directory (0700) under its base, refusing any component from the
// base down that is a symbolic link: crowsnest writes there, and an agent must not be able to
// point that write somewhere else.
func safeInbox(inbox Inbox) error {
	base, dir := filepath.Clean(inbox.Base), filepath.Clean(inbox.Dir)
	rel, err := filepath.Rel(base, dir)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("the inbox %s is not under %s", dir, base)
	}
	path := base
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
				return fmt.Errorf("creating %s: %w", path, err)
			}
		case err != nil:
			return fmt.Errorf("checking %s: %w", path, err)
		case info.Mode()&os.ModeSymlink != 0:
			return fmt.Errorf("%s is a symbolic link; crowsnest copies attachments only into its own directories", path)
		case !info.IsDir():
			return fmt.Errorf("%s is not a directory", path)
		}
	}
	return os.Chmod(dir, 0o700) //nolint:gosec // a directory: 0700 is owner-only
}

// privateDir creates dir 0700, tightening an existing one.
func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // a directory: 0700 is owner-only
		return fmt.Errorf("making %s private: %w", dir, err)
	}
	return nil
}

// ---- Views and names ---------------------------------------------------------------------

func view(record store.AttachmentRecord) Attachment {
	out := Attachment{
		ID: record.ID, Name: record.Name, Type: record.Type, Size: record.Size, SHA256: record.SHA256,
		Image: IsImage(record.Type), Thumb: record.HasThumb, Inline: Inline(record.Type, record.Name),
		Origin: record.Origin, OwnerKind: record.OwnerKind, OwnerID: record.OwnerID, Role: record.Role, Ref: record.Ref,
		Path: record.Path, CreatedAt: record.CreatedAt,
	}
	if !record.ReleasedAt.IsZero() {
		released := record.ReleasedAt
		out.ReleasedAt = &released
	}
	return out
}

func views(records []store.AttachmentRecord) []Attachment {
	out := make([]Attachment, 0, len(records))
	for _, record := range records {
		out = append(out, view(record))
	}
	return out
}

// idPrefix starts every attachment id.
const idPrefix = "a_"

// newID is a_ and 24 random hex characters.
func newID() string {
	var raw [12]byte
	_, _ = rand.Read(raw[:])
	return idPrefix + hex.EncodeToString(raw[:])
}

// ValidID reports whether text is shaped like an attachment id.
func ValidID(text string) bool {
	if len(text) != len(idPrefix)+24 || !strings.HasPrefix(text, idPrefix) {
		return false
	}
	_, err := hex.DecodeString(text[len(idPrefix):])
	return err == nil && strings.ToLower(text) == text
}
