// Tests for the attachment routes (DESIGN.md 8.6): authentication and the cross-site guards,
// uploads (raw and multipart), limits, the quota, what is served inline (never HTML or SVG), and
// files travelling with a Chat prompt, a job message and an answer.

package httpd

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jclement/crowsnest/internal/attach"
	"github.com/jclement/crowsnest/internal/fleet"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/store"
)

func newAttachService(t *testing.T, limits attach.Limits) *attach.Service {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service, err := attach.New(attach.Options{Store: db, Dir: filepath.Join(t.TempDir(), "attachments"), Limits: limits})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func attachHarness(t *testing.T, limits attach.Limits) (*harness, *attach.Service, *http.Cookie) {
	t.Helper()
	service := newAttachService(t, limits)
	h := newHarness(t, harnessOptions{attachments: service, attachLayout: attach.Layout{StateDir: t.TempDir()}})
	return h, service, h.login()
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for x := range 40 {
		picture.Set(x, 10, color.Black)
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, picture); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// rawUpload is how the web app uploads: the bytes as the body, the name in X-Filename.
func rawUpload(name string, body []byte, contentType string) *http.Request {
	r := request(http.MethodPost, "/api/v1/attachments", "")
	r.Body = ioNopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Set("Content-Type", contentType)
	r.Header.Set(HeaderFilename, name)
	return r
}

func uploaded(t *testing.T, h *harness, cookie *http.Cookie, name string, body []byte, contentType string) attach.Attachment {
	t.Helper()
	response := h.do(withCookie(rawUpload(name, body, contentType), cookie))
	if response.Code != http.StatusCreated {
		t.Fatalf("upload %s: %d %s", name, response.Code, response.Body)
	}
	return decodeData[attach.Attachment](t, response.Body.Bytes())
}

func TestAttachmentRoutesNeedASession(t *testing.T) {
	h, service, _ := attachHarness(t, attach.Limits{})
	stored, _ := service.Upload(context.Background(), "a.txt", strings.NewReader("words"))
	for _, r := range []*http.Request{
		rawUpload("a.txt", []byte("hi"), "text/plain"),
		request(http.MethodGet, "/api/v1/attachments/"+stored.ID, ""),
		request(http.MethodGet, "/api/v1/attachments/"+stored.ID+"/thumb", ""),
		request(http.MethodGet, "/api/v1/attachments/"+stored.ID+"/meta", ""),
		request(http.MethodGet, "/api/v1/attachments?owner=orchestrator:", ""),
		request(http.MethodDelete, "/api/v1/attachments/"+stored.ID, ""),
	} {
		if response := h.do(r); response.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session: %d", r.Method, r.URL.Path, response.Code)
		}
	}
}

func TestAttachmentUploadIsGuardedAgainstCrossSiteRequests(t *testing.T) {
	h, service, cookie := attachHarness(t, attach.Limits{})
	noHeader := withCookie(rawUpload("a.txt", []byte("hi"), "text/plain"), cookie)
	noHeader.Header.Del("X-Crowsnest")
	foreign := withCookie(rawUpload("a.txt", []byte("hi"), "text/plain"), cookie)
	foreign.Header.Set("Origin", "https://evil.example")
	noOrigin := withCookie(rawUpload("a.txt", []byte("hi"), "text/plain"), cookie)
	noOrigin.Header.Del("Origin")
	stored, _ := service.Upload(context.Background(), "keep.txt", strings.NewReader("keep"))
	discard := withCookie(request(http.MethodDelete, "/api/v1/attachments/"+stored.ID, ""), cookie)
	discard.Header.Set("Origin", "https://evil.example")
	for _, r := range []*http.Request{noHeader, foreign, noOrigin, discard} {
		if response := h.do(r); response.Code != http.StatusForbidden {
			t.Errorf("cross-site %s %s: %d", r.Method, r.URL.Path, response.Code)
		}
	}
	if _, err := service.Get(context.Background(), stored.ID); err != nil {
		t.Fatalf("a refused DELETE removed the upload: %v", err)
	}
}

func TestUploadSniffsTheTypeRawAndMultipart(t *testing.T) {
	h, _, cookie := attachHarness(t, attach.Limits{})
	pngData := testPNG(t)
	// The browser says text/html; the bytes are a PNG. The bytes win.
	raw := uploaded(t, h, cookie, "Screen%20Shot.png", pngData, "text/html")
	if raw.Name != "Screen Shot.png" || raw.Type != "image/png" || !raw.Thumb || !raw.Inline || raw.OwnerKind != "" {
		t.Fatalf("raw upload %+v", raw)
	}

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="evil.png"`)
	header.Set("Content-Type", "image/png")
	part, _ := form.CreatePart(header)
	_, _ = part.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(document.cookie)"/>`))
	_ = form.Close()
	r := withCookie(request(http.MethodPost, "/api/v1/attachments", ""), cookie)
	r.Body = ioNopCloser(bytes.NewReader(body.Bytes()))
	r.ContentLength = int64(body.Len())
	r.Header.Set("Content-Type", form.FormDataContentType())
	response := h.do(r)
	multi := decodeData[attach.Attachment](t, response.Body.Bytes())
	if response.Code != http.StatusCreated || multi.Type != "image/svg+xml" || multi.Inline || multi.Image {
		t.Fatalf("multipart SVG labelled PNG: %d %+v", response.Code, multi)
	}
}

func TestUploadLimitsAndQuota(t *testing.T) {
	h, _, cookie := attachHarness(t, attach.Limits{MaxFileBytes: 1000, QuotaBytes: 1500})
	response := h.do(withCookie(rawUpload("big.bin", bytes.Repeat([]byte{1}, 1001), "application/octet-stream"), cookie))
	if response.Code != http.StatusRequestEntityTooLarge || decodeError(t, response).Code != codeTooLarge || !strings.Contains(decodeError(t, response).Message, "1000 B") {
		t.Fatalf("one byte over: %d %s", response.Code, response.Body)
	}
	// A declared length far past the limit is refused before reading.
	declared := withCookie(rawUpload("big.bin", []byte("x"), "application/octet-stream"), cookie)
	declared.ContentLength = 1 << 30
	if response := h.do(declared); response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("declared 1 GB: %d", response.Code)
	}
	if response := h.do(withCookie(rawUpload("empty.txt", nil, "text/plain"), cookie)); response.Code != http.StatusBadRequest {
		t.Fatalf("empty: %d", response.Code)
	}
	uploaded(t, h, cookie, "a.bin", bytes.Repeat([]byte{2}, 1000), "application/octet-stream")
	response = h.do(withCookie(rawUpload("b.bin", bytes.Repeat([]byte{3}, 1000), "application/octet-stream"), cookie))
	if response.Code != http.StatusInsufficientStorage || decodeError(t, response).Code != codeQuota {
		t.Fatalf("over quota: %d %s", response.Code, response.Body)
	}
	if !strings.Contains(decodeError(t, response).Message, "full") {
		t.Fatalf("quota message: %s", response.Body)
	}
}

// TestServedInlineOnlyWhenSafe is the XSS rule: HTML and SVG (whatever they are called) are
// never rendered in this origin; every file carries nosniff, and all but a PDF a sandbox CSP.
func TestServedInlineOnlyWhenSafe(t *testing.T) {
	h, _, cookie := attachHarness(t, attach.Limits{})
	cases := []struct {
		name, file string
		body       []byte
		wantType   string
		inline     bool
		sandbox    bool
	}{
		{"png", "shot.png", testPNG(t), "image/png", true, true},
		{"text", "log.txt", []byte("line one\n"), "text/plain; charset=utf-8", true, true},
		{"markdown", "notes.md", []byte("# Notes\n"), "text/plain; charset=utf-8", true, true},
		{"pdf", "report.pdf", []byte("%PDF-1.7\n1 0 obj\n"), "application/pdf", true, false},
		{"html", "page.html", []byte("<!doctype html><script>alert(document.cookie)</script>"), "text/html; charset=utf-8", false, true},
		{"html named png", "innocent.png", []byte("<html><body onload=alert(1)>"), "text/html; charset=utf-8", false, true},
		{"svg", "logo.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), "image/svg+xml", false, true},
		{"svg named png", "logo.png", []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`), "image/svg+xml", false, true},
		{"text named html", "page.html", []byte("hello <script>alert(1)</script>"), "text/plain; charset=utf-8", false, true},
		{"binary", "tool.bin", []byte{0x7f, 'E', 'L', 'F', 0, 1, 2}, "application/octet-stream", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stored := uploaded(t, h, cookie, tc.file, tc.body, "image/png")
			response := h.do(withCookie(request(http.MethodGet, "/api/v1/attachments/"+stored.ID, ""), cookie))
			header := response.Header()
			disposition := header.Get("Content-Disposition")
			if response.Code != http.StatusOK || header.Get("Content-Type") != tc.wantType || header.Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("%d type %q nosniff %q", response.Code, header.Get("Content-Type"), header.Get("X-Content-Type-Options"))
			}
			if strings.HasPrefix(disposition, "inline") != tc.inline || (!tc.inline && !strings.HasPrefix(disposition, "attachment")) {
				t.Fatalf("disposition %q, want inline=%v", disposition, tc.inline)
			}
			if strings.HasPrefix(header.Get("Content-Security-Policy"), "sandbox") != tc.sandbox {
				t.Fatalf("CSP %q, want sandbox=%v", header.Get("Content-Security-Policy"), tc.sandbox)
			}
			if !bytes.Equal(response.Body.Bytes(), tc.body) {
				t.Fatal("body changed")
			}
			forced := h.do(withCookie(request(http.MethodGet, "/api/v1/attachments/"+stored.ID+"?download=1", ""), cookie))
			if !strings.HasPrefix(forced.Header().Get("Content-Disposition"), "attachment") {
				t.Fatalf("?download=1: %q", forced.Header().Get("Content-Disposition"))
			}
		})
	}
}

func TestDispositionCannotBeForgedByTheName(t *testing.T) {
	for _, name := range []string{`a"; filename="evil.html`, "a\r\nSet-Cookie: x=1", "naïve résumé.pdf"} {
		clean := attach.CleanName(name)
		value := disposition(false, clean)
		kind, params, err := mime.ParseMediaType(value)
		if err != nil || strings.ContainsAny(value, "\r\n") || kind != "attachment" || len(params) != 1 || params["filename"] != clean {
			t.Errorf("disposition(%q) = %q (parsed %q %v %v)", name, value, kind, params, err)
		}
	}
}

func TestThumbMetaListAndDiscard(t *testing.T) {
	h, _, cookie := attachHarness(t, attach.Limits{})
	shot := uploaded(t, h, cookie, "shot.png", testPNG(t), "image/png")
	text := uploaded(t, h, cookie, "a.txt", []byte("words"), "text/plain")
	thumb := h.do(withCookie(request(http.MethodGet, "/api/v1/attachments/"+shot.ID+"/thumb", ""), cookie))
	if thumb.Code != http.StatusOK || thumb.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("thumb %d %q", thumb.Code, thumb.Header().Get("Content-Type"))
	}
	if none := h.do(withCookie(request(http.MethodGet, "/api/v1/attachments/"+text.ID+"/thumb", ""), cookie)); none.Code != http.StatusNotFound {
		t.Fatalf("thumb of a text file: %d", none.Code)
	}
	meta := h.do(withCookie(request(http.MethodGet, "/api/v1/attachments/"+shot.ID+"/meta", ""), cookie))
	if got := decodeData[attach.Attachment](t, meta.Body.Bytes()); got.ID != shot.ID || got.Type != "image/png" {
		t.Fatalf("meta %+v", got)
	}
	for _, path := range []string{"/api/v1/attachments/a_000000000000000000000000", "/api/v1/attachments/..%2F..%2Fetc%2Fpasswd", "/api/v1/attachments/a_ZZZ"} {
		if response := h.do(withCookie(request(http.MethodGet, path, ""), cookie)); response.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d", path, response.Code)
		}
	}
	if response := h.do(withCookie(request(http.MethodGet, "/api/v1/attachments", ""), cookie)); response.Code != http.StatusBadRequest {
		t.Fatalf("list with no owner: %d", response.Code)
	}
	if response := h.do(withCookie(request(http.MethodDelete, "/api/v1/attachments/"+text.ID, ""), cookie)); response.Code != http.StatusNoContent {
		t.Fatalf("discard: %d", response.Code)
	}
	if response := h.do(withCookie(request(http.MethodGet, "/api/v1/attachments/"+text.ID, ""), cookie)); response.Code != http.StatusNotFound {
		t.Fatalf("after discard: %d", response.Code)
	}
}

func TestMeReportsTheLimits(t *testing.T) {
	h, _, cookie := attachHarness(t, attach.Limits{MaxFileBytes: 5 << 20, MaxPerMessage: 4})
	me := decodeData[MeResponse](t, h.do(withCookie(request(http.MethodGet, "/api/v1/auth/me", ""), cookie)).Body.Bytes())
	if me.Features.Attachments == nil || me.Features.Attachments.MaxBytes != 5<<20 || me.Features.Attachments.MaxFiles != 4 {
		t.Fatalf("features %+v", me.Features)
	}
	off := newHarness(t, harnessOptions{noAuth: true})
	if got := decodeData[MeResponse](t, off.do(request(http.MethodGet, "/api/v1/auth/me", "")).Body.Bytes()); got.Features.Attachments != nil {
		t.Fatal("limits reported with attachments off")
	}
	if response := off.do(request(http.MethodGet, "/api/v1/attachments/a_000000000000000000000000", "")); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("without a service: %d", response.Code)
	}
}

// ---- Files travelling with messages ----------------------------------------------------------

func TestPromptCarriesAttachmentsAsLinesAndCopies(t *testing.T) {
	c := newConversationHarness(t)
	service := newAttachService(t, attach.Limits{})
	stateDir := t.TempDir()
	c.harness = newHarness(t, harnessOptions{noAuth: true, herdr: c.fake, orchestrator: c.orch, attachments: service, attachLayout: attach.Layout{StateDir: stateDir}})
	shot, _ := service.Upload(context.Background(), "Screen Shot.png", bytes.NewReader(testPNG(t)))

	response := prompt(c, "orchestrator", `{"text":"what word is in this?","attachments":["`+shot.ID+`"]}`)
	if response.code != http.StatusOK {
		t.Fatalf("prompt %d %s", response.code, response.body)
	}
	copyPath := filepath.Join(stateDir, "orchestrator", "attachments", shot.ID+"-Screen-Shot.png")
	prompts := c.fake.Prompts()
	want := "what word is in this?\nAttached: " + copyPath + " (image/png, " + attach.FormatSize(shot.Size) + ")"
	if len(prompts) != 1 || prompts[0].Text != want {
		t.Fatalf("typed %q\nwant  %q", prompts, want)
	}
	if body, err := os.ReadFile(copyPath); err != nil || !bytes.Equal(body, testPNG(t)) {
		t.Fatalf("the agent's copy: %v", err)
	}
	// Sent once: a second send naming it is refused, and nothing is typed.
	if again := prompt(c, "orchestrator", `{"text":"again","attachments":["`+shot.ID+`"]}`); again.code != http.StatusConflict {
		t.Fatalf("second send of one upload: %d %s", again.code, again.body)
	}
	if len(c.fake.Prompts()) != 1 {
		t.Fatal("a refused send typed something")
	}
	// A file alone (no words) is a send.
	alone, _ := service.Upload(context.Background(), "log.txt", strings.NewReader("error: x\n"))
	if response := prompt(c, "orchestrator", `{"text":"","attachments":["`+alone.ID+`"]}`); response.code != http.StatusOK {
		t.Fatalf("attachment alone: %d %s", response.code, response.body)
	}
}

func TestPromptRefusedAtADraftGivesTheAttachmentBack(t *testing.T) {
	c := newConversationHarness(t)
	service := newAttachService(t, attach.Limits{})
	stateDir := t.TempDir()
	c.harness = newHarness(t, harnessOptions{noAuth: true, herdr: c.fake, orchestrator: c.orch, attachments: service, attachLayout: attach.Layout{StateDir: stateDir}})
	pane := c.orch.Status().PaneID
	_ = c.fake.SetScreen(pane, fleet.StripANSI(testDraftInput), testDraftInput)
	shot, _ := service.Upload(context.Background(), "a.png", bytes.NewReader(testPNG(t)))
	if response := prompt(c, "orchestrator", `{"text":"look","attachments":["`+shot.ID+`"]}`); response.code != http.StatusConflict {
		t.Fatalf("draft: %d", response.code)
	}
	if got, _ := service.Get(context.Background(), shot.ID); got.OwnerKind != "" || got.Path != "" {
		t.Fatalf("a send that typed nothing kept the attachment: %+v", got)
	}
	if entries, _ := os.ReadDir(filepath.Join(stateDir, "orchestrator", "attachments")); len(entries) != 0 {
		t.Fatalf("its copy stayed: %v", entries)
	}
	// "Send anyway" can name it again.
	if response := prompt(c, "orchestrator", `{"text":"look","force_over_draft":true,"attachments":["`+shot.ID+`"]}`); response.code != http.StatusOK {
		t.Fatalf("send anyway: %d %s", response.code, response.body)
	}
}

func TestAnswerCarriesAttachmentsIntoTheAskersInbox(t *testing.T) {
	db, _ := store.OpenMemory(context.Background())
	t.Cleanup(func() { _ = db.Close() })
	service := newAttachService(t, attach.Limits{})
	inbox := questions.New(questions.Options{Store: db, Attachments: service})
	question, err := inbox.Ask(context.Background(), questions.AskInput{Title: "Which layout?", Kind: questions.KindManual}, questions.Caller{PaneID: "w3:p1"})
	if err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	h := newHarness(t, harnessOptions{noAuth: true, questions: inbox, attachments: service, attachLayout: attach.Layout{StateDir: stateDir}, herdr: &herdrUnavailable{}})
	sketch, _ := service.Upload(context.Background(), "sketch.png", bytes.NewReader(testPNG(t)))
	response := h.do(request(http.MethodPost, "/api/v1/questions/"+question.ID+"/answer", `{"answer_note":"like this","attachments":["`+sketch.ID+`"]}`))
	if response.Code != http.StatusOK {
		t.Fatalf("answer %d %s", response.Code, response.Body)
	}
	answered, _ := inbox.Get(context.Background(), question.ID)
	if len(answered.AnswerAttachments) != 1 || !strings.HasPrefix(answered.AnswerAttachments[0].Path, filepath.Join(stateDir, "panes", "w3-p1", "attachments")) {
		t.Fatalf("answer attachments %+v", answered.AnswerAttachments)
	}
	line := questions.AnswerLine(answered)
	if !strings.Contains(line, "(attached: "+answered.AnswerAttachments[0].Path+" (image/png,") {
		t.Fatalf("the typed line does not name the file: %q", line)
	}
	listed := h.do(request(http.MethodGet, "/api/v1/attachments?answer="+question.ID, ""))
	var list struct{ Data []attach.Attachment }
	if json.Unmarshal(listed.Body.Bytes(), &list) != nil || len(list.Data) != 1 || list.Data[0].ID != sketch.ID {
		t.Fatalf("list by answer: %s", listed.Body)
	}
}

// herdrUnavailable is a herdr.Client for routes that must not reach herdr.
type herdrUnavailable struct{ herdr.Client }

func ioNopCloser(reader *bytes.Reader) nopCloser { return nopCloser{reader} }

type nopCloser struct{ *bytes.Reader }

func (nopCloser) Close() error { return nil }
