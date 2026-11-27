package attach

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

// ---- Helpers -------------------------------------------------------------------------------

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

func newService(t *testing.T, limits Limits) (*Service, *store.Store, *clock) {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := &clock{now: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)}
	service, err := New(Options{Store: db, Dir: filepath.Join(t.TempDir(), "attachments"), Limits: limits, Now: now.Now})
	if err != nil {
		t.Fatal(err)
	}
	return service, db, now
}

// pngBytes is a real PNG of the given size, so thumbnails have something to decode.
func pngBytes(t *testing.T, width, height int) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			picture.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 200, A: 255}) //nolint:gosec // test pattern
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, picture); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func upload(t *testing.T, service *Service, name string, body []byte) Attachment {
	t.Helper()
	attachment, err := service.Upload(context.Background(), name, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("upload %s: %v", name, err)
	}
	return attachment
}

// ---- Sniffing and what is served inline ----------------------------------------------------

func TestSniffIgnoresTheNameExceptForMarkdown(t *testing.T) {
	pngData := pngBytes(t, 4, 4)
	cases := []struct {
		name, file string
		head       []byte
		want       string
	}{
		{"png", "shot.png", pngData, "image/png"},
		{"png named html", "shot.html", pngData, "image/png"},
		{"jpeg", "photo.jpg", []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00"), "image/jpeg"},
		{"gif", "a.gif", []byte("GIF89a......"), "image/gif"},
		{"pdf", "report.pdf", []byte("%PDF-1.7\n%..."), "application/pdf"},
		{"html", "page.png", []byte("<!DOCTYPE html><html><script>alert(1)</script>"), "text/html"},
		{"svg with declaration", "x.png", []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), typeSVG},
		{"bare svg", "x.txt", []byte(`<svg onload="alert(1)"></svg>`), typeSVG},
		{"text", "notes.txt", []byte("plain words\n"), typePlain},
		{"markdown by name", "notes.md", []byte("# Title\n\nwords\n"), typeMarkdown},
		{"heic", "IMG_1.HEIC", append([]byte{0, 0, 0, 24}, []byte("ftypheic\x00\x00\x00\x00mif1heic")...), typeHEIC},
		{"binary", "blob.bin", []byte{0, 1, 2, 3, 0xfe, 0xff}, typeOctet},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Sniff(tc.head, tc.file); got != tc.want {
				t.Fatalf("Sniff = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInlineNeverForHTMLOrSVG(t *testing.T) {
	cases := []struct {
		mediaType, name string
		want            bool
	}{
		{"image/png", "a.png", true},
		{"image/jpeg", "a.jpeg", true},
		{"application/pdf", "a.pdf", true},
		{typePlain, "log.txt", true},
		{typeMarkdown, "notes.md", true},
		{"text/html", "page.html", false},
		{typeSVG, "logo.svg", false},
		{typeSVG, "logo.png", false},
		{typePlain, "page.html", false}, // HTML that sniffs as text is still not shown
		{"image/png", "trick.svg", false},
		{typeHEIC, "IMG.heic", false},
		{typeOctet, "blob.bin", false},
		{"application/zip", "a.zip", false},
		{"text/xml", "a.xml", false},
	}
	for _, tc := range cases {
		if got := Inline(tc.mediaType, tc.name); got != tc.want {
			t.Errorf("Inline(%q, %q) = %v, want %v", tc.mediaType, tc.name, got, tc.want)
		}
	}
	if ServedType(typeMarkdown) != "text/plain; charset=utf-8" || ServedType("text/html") != "text/html; charset=utf-8" || ServedType("image/png") != "image/png" {
		t.Fatal("ServedType")
	}
	if IsImage(typeSVG) || !IsImage("image/webp") {
		t.Fatal("IsImage: SVG must never count as a picture")
	}
}

// ---- Upload, limits, thumbnails ------------------------------------------------------------

func TestUploadSniffsStoresPrivatelyAndThumbnails(t *testing.T) {
	service, _, _ := newService(t, Limits{})
	attachment := upload(t, service, `C:\Users\me\Screen Shot.png`, pngBytes(t, 800, 400))
	if attachment.Name != "Screen Shot.png" || attachment.Type != "image/png" || !attachment.Image || !attachment.Thumb || !attachment.Inline {
		t.Fatalf("attachment = %+v", attachment)
	}
	if !ValidID(attachment.ID) || attachment.Origin != "human" || attachment.OwnerKind != "" {
		t.Fatalf("id/origin/owner = %+v", attachment)
	}
	info, err := os.Stat(service.blobPath(attachment.SHA256))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("blob mode %v err %v", info, err)
	}
	dirInfo, _ := os.Stat(service.opts.Dir)
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", dirInfo.Mode())
	}
	thumb, err := service.OpenThumb(context.Background(), attachment.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = thumb.Close() }()
	config, format, err := image.DecodeConfig(thumb)
	if err != nil || format != "jpeg" || config.Width != thumbEdge || config.Height != thumbEdge/2 {
		t.Fatalf("thumb %v %s %v", config, format, err)
	}
}

func TestUploadLimits(t *testing.T) {
	service, _, _ := newService(t, Limits{MaxFileBytes: 100})
	if _, err := service.Upload(context.Background(), "ok.txt", bytes.NewReader(bytes.Repeat([]byte("a"), 100))); err != nil {
		t.Fatalf("exactly the limit: %v", err)
	}
	_, err := service.Upload(context.Background(), "big.txt", bytes.NewReader(bytes.Repeat([]byte("a"), 101)))
	var tooLarge *TooLargeError
	if !errors.As(err, &tooLarge) || !strings.Contains(err.Error(), "100 B") {
		t.Fatalf("one byte over: %v", err)
	}
	if _, err := service.Upload(context.Background(), "empty.txt", bytes.NewReader(nil)); !errors.Is(err, ErrEmpty) {
		t.Fatalf("empty: %v", err)
	}
	leftovers, _ := filepath.Glob(filepath.Join(service.opts.Dir, ".upload-*"))
	if len(leftovers) != 0 {
		t.Fatalf("temporary files left behind: %v", leftovers)
	}
}

func TestIdenticalUploadsShareOneBlob(t *testing.T) {
	service, db, _ := newService(t, Limits{})
	body := pngBytes(t, 10, 10)
	first := upload(t, service, "a.png", body)
	second := upload(t, service, "b.png", body)
	if first.ID == second.ID || first.SHA256 != second.SHA256 || !second.Thumb {
		t.Fatalf("first %+v second %+v", first, second)
	}
	used, _ := db.BlobBytes(context.Background())
	if used != int64(len(body)) {
		t.Fatalf("counted %d bytes, want one blob of %d", used, len(body))
	}
	if err := service.Discard(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(service.blobPath(first.SHA256)); err != nil {
		t.Fatalf("a blob still named by another row was deleted: %v", err)
	}
	if err := service.Discard(context.Background(), second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(service.blobPath(first.SHA256)); !os.IsNotExist(err) {
		t.Fatalf("the last row's blob is still there: %v", err)
	}
}

func TestNoThumbnailForUndecodableOrHugeImages(t *testing.T) {
	service, _, _ := newService(t, Limits{})
	webp := upload(t, service, "a.webp", []byte("RIFF\x00\x00\x00\x00WEBPVP8 junk junk junk"))
	if webp.Type != "image/webp" || webp.Thumb || !webp.Image {
		t.Fatalf("webp %+v", webp)
	}
	// A PNG header claiming 100000×100000 pixels: refused before decoding.
	header := pngBytes(t, 1, 1)
	huge := append([]byte(nil), header...)
	huge[16], huge[17], huge[18], huge[19] = 0, 1, 0x86, 0xa0 // width 100000
	huge[20], huge[21], huge[22], huge[23] = 0, 1, 0x86, 0xa0 // height 100000
	bomb := upload(t, service, "bomb.png", huge)
	if bomb.Thumb {
		t.Fatal("a 10-gigapixel PNG got a thumbnail")
	}
	// R5-S12: 20 megapixels (5000×4000) is past the 16-megapixel cap; the header alone says so.
	big := append([]byte(nil), header...)
	big[16], big[17], big[18], big[19] = 0, 0, 0x13, 0x88 // width 5000
	big[20], big[21], big[22], big[23] = 0, 0, 0x0f, 0xa0 // height 4000
	if over := upload(t, service, "big.png", big); over.Thumb {
		t.Fatal("a 20-megapixel PNG got a thumbnail")
	}
}

// ---- Quota and cleanup ---------------------------------------------------------------------

func TestQuotaDeletesReleasedThenStalePendingThenRefuses(t *testing.T) {
	service, db, now := newService(t, Limits{QuotaBytes: 250})
	ctx := context.Background()
	inbox := Inbox{Kind: OwnerJob, ID: "j_1", Base: t.TempDir()}
	inbox.Dir = filepath.Join(inbox.Base, "jobs", "j_1", "attachments")

	old := upload(t, service, "old.txt", bytes.Repeat([]byte("o"), 100))
	delivered, err := service.Deliver(ctx, []string{old.ID}, inbox, RoleMessage, "")
	if err != nil {
		t.Fatal(err)
	}
	held := upload(t, service, "held.txt", bytes.Repeat([]byte("h"), 100))
	if _, err := service.Deliver(ctx, []string{held.ID}, Inbox{Kind: OwnerJob, ID: "j_2", Base: inbox.Base, Dir: filepath.Join(inbox.Base, "jobs", "j_2", "attachments")}, RoleMessage, ""); err != nil {
		t.Fatal(err)
	}
	// Full: 200 of 250 used, both owned by live jobs. A 100-byte upload has nowhere to go.
	if _, err := service.Upload(ctx, "x.txt", bytes.NewReader(bytes.Repeat([]byte("x"), 100))); !errors.Is(err, ErrQuota) {
		t.Fatalf("over quota with nothing releasable: %v", err)
	}
	// j_1 finishes: its file is released, its copy removed, and the next upload takes its room.
	if err := service.Release(ctx, OwnerJob, "j_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(delivered[0].Path); !os.IsNotExist(err) {
		t.Fatalf("the released copy is still there: %v", err)
	}
	pending := upload(t, service, "new.txt", bytes.Repeat([]byte("n"), 100))
	if _, err := db.GetAttachment(ctx, old.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the released attachment was not cleaned up: %v", err)
	}
	// A pending upload is spared for an hour, then it is a candidate too.
	if _, err := service.Upload(ctx, "y.txt", bytes.NewReader(bytes.Repeat([]byte("y"), 100))); !errors.Is(err, ErrQuota) {
		t.Fatalf("a fresh pending upload was deleted to make room: %v", err)
	}
	now.now = now.now.Add(2 * time.Hour)
	upload(t, service, "z.txt", bytes.Repeat([]byte("z"), 100))
	if _, err := db.GetAttachment(ctx, pending.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an hour-old pending upload survived a full quota: %v", err)
	}
	if _, err := db.GetAttachment(ctx, held.ID); err != nil {
		t.Fatalf("a live job's attachment was deleted: %v", err)
	}
}

func TestSweepDeletesAbandonedUploadsAndReleasesOldConversationFiles(t *testing.T) {
	service, db, now := newService(t, Limits{})
	ctx := context.Background()
	base := t.TempDir()
	abandoned := upload(t, service, "a.txt", []byte("abandoned"))
	chat := upload(t, service, "c.txt", []byte("chat"))
	delivered, err := service.Deliver(ctx, []string{chat.ID}, Inbox{Kind: OwnerOrchestrator, Base: base, Dir: filepath.Join(base, "orchestrator", "attachments")}, RolePrompt, "")
	if err != nil {
		t.Fatal(err)
	}
	now.now = now.now.Add(25 * time.Hour)
	service.Sweep(ctx)
	if _, err := db.GetAttachment(ctx, abandoned.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an upload never sent for a day survived: %v", err)
	}
	if record, _ := db.GetAttachment(ctx, chat.ID); !record.ReleasedAt.IsZero() {
		t.Fatal("a day-old chat attachment was released")
	}
	now.now = now.now.Add(7 * 24 * time.Hour)
	service.Sweep(ctx)
	if record, _ := db.GetAttachment(ctx, chat.ID); record.ReleasedAt.IsZero() {
		t.Fatal("a week-old chat attachment was not released")
	}
	if _, err := os.Stat(delivered[0].Path); !os.IsNotExist(err) {
		t.Fatalf("the released copy is still there: %v", err)
	}
}

// ---- Delivering to an agent ----------------------------------------------------------------

func TestDeliverCopiesIntoAPrivateInboxOnce(t *testing.T) {
	service, _, _ := newService(t, Limits{MaxPerMessage: 2})
	ctx := context.Background()
	base := t.TempDir()
	inbox := Inbox{Kind: OwnerJob, ID: "j_7", Base: base, Dir: filepath.Join(base, "jobs", "j_7", "attachments")}
	shot := upload(t, service, "Screen Shot 2026-11-26 at 10.02.png", pngBytes(t, 20, 20))
	delivered, err := service.Deliver(ctx, []string{shot.ID}, inbox, RoleBrief, "")
	if err != nil {
		t.Fatal(err)
	}
	got := delivered[0]
	wantName := shot.ID + "-Screen-Shot-2026-11-26-at-10.02.png"
	if filepath.Base(got.Path) != wantName || got.OwnerKind != OwnerJob || got.OwnerID != "j_7" || got.Role != RoleBrief {
		t.Fatalf("delivered %+v", got)
	}
	info, err := os.Stat(got.Path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("copy mode %v err %v", info, err)
	}
	dir, _ := os.Stat(inbox.Dir)
	if dir.Mode().Perm() != 0o700 {
		t.Fatalf("inbox mode %v", dir.Mode())
	}
	line := Line(got)
	if line != "Attached: "+got.Path+" (image/png, "+FormatSize(shot.Size)+")" {
		t.Fatalf("line %q", line)
	}
	if _, err := service.Deliver(ctx, []string{shot.ID}, inbox, RoleMessage, ""); !errors.Is(err, ErrNotPending) {
		t.Fatalf("delivering twice: %v", err)
	}
	a, b, c := upload(t, service, "a", []byte("a")), upload(t, service, "b", []byte("b")), upload(t, service, "c", []byte("c"))
	if _, err := service.Deliver(ctx, []string{a.ID, b.ID, c.ID}, inbox, RoleMessage, ""); !errors.Is(err, ErrTooMany) {
		t.Fatalf("three files with a limit of two: %v", err)
	}
	if err := service.CheckPending(ctx, []string{a.ID, a.ID}); err == nil {
		t.Fatal("the same attachment twice was accepted")
	}
	if err := service.CheckPending(ctx, []string{"a_000000000000000000000000"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}

func TestDeliverRefusesAnInboxThroughALink(t *testing.T) {
	service, _, _ := newService(t, Limits{})
	base, elsewhere := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "jobs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(base, "jobs", "j_9")); err != nil {
		t.Fatal(err)
	}
	shot := upload(t, service, "a.txt", []byte("words"))
	_, err := service.Deliver(context.Background(), []string{shot.ID}, Inbox{Kind: OwnerJob, ID: "j_9", Base: base, Dir: filepath.Join(base, "jobs", "j_9", "attachments")}, RoleMessage, "")
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("err = %v", err)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Fatalf("crowsnest wrote through the link: %v", entries)
	}
}

// ---- Files agents name ---------------------------------------------------------------------

func TestImportAgentFilesIsConfinedToItsDirectories(t *testing.T) {
	service, _, _ := newService(t, Limits{MaxFileBytes: 1000})
	ctx := context.Background()
	work, jobDir, outside := t.TempDir(), t.TempDir(), t.TempDir()
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(work, "out", "report.md"), "# Report\n")
	write(filepath.Join(jobDir, "shot.txt"), "screenshot stand-in")
	write(filepath.Join(outside, "secret.txt"), "secret")
	write(filepath.Join(work, "big.txt"), strings.Repeat("b", 1001))
	mustLink := func(target, link string) {
		t.Helper()
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	mustLink(filepath.Join(outside, "secret.txt"), filepath.Join(work, "innocent.txt"))
	mustLink(outside, filepath.Join(work, "linked-dir"))
	mustLink(filepath.Join(work, "out", "report.md"), filepath.Join(work, "alias.md"))
	roots := []string{work, jobDir}

	ok, err := service.ImportAgentFiles(ctx, []string{filepath.Join(work, "out", "report.md"), filepath.Join(jobDir, "shot.txt"), filepath.Join(work, "alias.md")}, roots, "w1:p2")
	if err != nil {
		t.Fatalf("files inside the roots: %v", err)
	}
	if len(ok) != 3 || ok[0].Origin != "agent" || ok[0].Type != typeMarkdown || ok[0].OwnerKind != "" {
		t.Fatalf("imported %+v", ok)
	}

	refused := []struct{ name, path, want string }{
		{"absolute path outside", filepath.Join(outside, "secret.txt"), "outside"},
		{"dot-dot out of the root", work + "/../" + filepath.Base(outside) + "/secret.txt", ".."},
		{"dot-dot that stays inside", work + "/out/../out/report.md", ".."},
		{"dot-dot cleaned away by the client", filepath.Join(work, "..", filepath.Base(outside), "secret.txt"), "outside"},
		{"a link to a file outside", filepath.Join(work, "innocent.txt"), "outside"},
		{"a file through a linked directory", filepath.Join(work, "linked-dir", "secret.txt"), "outside"},
		{"relative path", "out/report.md", "absolute"},
		{"a directory", filepath.Join(work, "out"), "regular file"},
		{"too large", filepath.Join(work, "big.txt"), "larger than"},
		{"missing", filepath.Join(work, "nope.txt"), "no such file"},
		{"the home directory's secrets", "/etc/passwd", "outside"},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			_, err := service.ImportAgentFiles(ctx, []string{tc.path}, roots, "w1:p2")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
	// One bad path refuses the set and stores nothing.
	before, _ := service.opts.Store.StalePendingAttachments(ctx, time.Now().Add(time.Hour*1e4))
	if _, err := service.ImportAgentFiles(ctx, []string{filepath.Join(work, "out", "report.md"), filepath.Join(outside, "secret.txt")}, roots, "w1:p2"); err == nil {
		t.Fatal("a set with one outside file was accepted")
	}
	after, _ := service.opts.Store.StalePendingAttachments(ctx, time.Now().Add(time.Hour*1e4))
	if len(after) != len(before) {
		t.Fatalf("a refused set left %d rows behind", len(after)-len(before))
	}
	if _, err := service.ImportAgentFiles(ctx, []string{filepath.Join(work, "out", "report.md")}, nil, "w1:p2"); err == nil {
		t.Fatal("no roots: accepted")
	}
}

func TestImportFollowsARootGivenThroughALink(t *testing.T) {
	service, _, _ := newService(t, Limits{})
	real := t.TempDir()
	linkParent := t.TempDir()
	link := filepath.Join(linkParent, "work")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "a.txt"), []byte("words"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The root recorded through the link, the path written resolved, and the reverse (the /tmp
	// and /private/tmp of macOS).
	for _, tc := range []struct{ root, path string }{
		{link, filepath.Join(real, "a.txt")},
		{real, filepath.Join(link, "a.txt")},
	} {
		if _, err := service.ImportAgentFiles(context.Background(), []string{tc.path}, []string{tc.root}, ""); err != nil {
			t.Fatalf("root %s path %s: %v", tc.root, tc.path, err)
		}
	}
}

// ---- Names ---------------------------------------------------------------------------------

func TestNames(t *testing.T) {
	cases := map[string]string{
		"shot.png":                "shot.png",
		"/etc/passwd":             "passwd",
		`C:\Users\x\a b.png`:      "a b.png",
		"..":                      "file",
		"":                        "file",
		"bell\x07\x1b[31mred.txt": "bellred.txt",
		"\u202eevil\u202dtxt.exe": "eviltxt.exe",
	}
	for in, want := range cases {
		if got := CleanName(in); got != want {
			t.Errorf("CleanName(%q) = %q, want %q", in, got, want)
		}
	}
	copies := map[string]string{
		"Screen Shot 10.02.png":           "a_x-Screen-Shot-10.02.png",
		"../../etc/passwd":                "a_x-etc-passwd",
		"-rf":                             "a_x-rf",
		".hidden":                         "a_x-hidden",
		"a.verylongextension12345":        "a_x-a",
		"日本語.txt":                         "a_x-file.txt",
		"$(rm -rf ~).sh":                  "a_x-rm--rf.sh",
		strings.Repeat("a", 100) + ".png": "a_x-" + strings.Repeat("a", 56) + ".png",
	}
	for in, want := range copies {
		if got := CopyName("a_x", in); got != want {
			t.Errorf("CopyName(%q) = %q, want %q", in, got, want)
		}
	}
	sizes := map[int64]string{0: "0 B", 812: "812 B", 1024: "1 KB", 240 * 1024: "240 KB", 3_500_000: "3.3 MB", 1 << 20: "1 MB", 25 << 20: "25 MB", 1 << 30: "1 GB"}
	for in, want := range sizes {
		if got := FormatSize(in); got != want {
			t.Errorf("FormatSize(%d) = %q, want %q", in, got, want)
		}
	}
	attachment := Attachment{Path: "/s/jobs/j_1/attachments/a_x-a.png\nrm -rf ~", Type: "image/png", Size: 2048}
	if got := Line(attachment); strings.ContainsAny(got, "\n\r\x1b") {
		t.Fatalf("Line kept a control character: %q", got)
	}
	if got := WithLines("look at this\n", []Attachment{{Path: "/p/a_x-a.png", Type: "image/png", Size: 10}}); got != "look at this\nAttached: /p/a_x-a.png (image/png, 10 B)" {
		t.Fatalf("WithLines %q", got)
	}
	if got := WithLines("", []Attachment{{Path: "/p/a", Type: "text/plain", Size: 1}}); got != "Attached: /p/a (text/plain, 1 B)" {
		t.Fatalf("WithLines alone %q", got)
	}
	if !ValidID("a_0123456789abcdef01234567") || ValidID("a_0123456789ABCDEF01234567") || ValidID("../etc/passwd") || ValidID("a_xyz") {
		t.Fatal("ValidID")
	}
}
