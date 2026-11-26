// The attachment routes (DESIGN.md 8.6 and 10): upload, download, thumbnails, the list for an
// owner, and removing an upload before it is sent; plus the helpers the send routes (Chat,
// job messages, answers) use to copy a message's files into the right agent's inbox.
//
// Serving files from this origin is an XSS surface: a file a browser renders here runs with the
// session. So the type is sniffed (never the uploader's word), only raster images, PDF and plain
// text are ever inline, everything else (SVG and HTML above all) is Content-Disposition:
// attachment, and every response but a PDF's carries a sandboxing CSP, so even a browser that
// rendered a download anyway would run no script in this origin. nosniff comes from the
// middleware.

package httpd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/attach"
	"github.com/jclement/crowsnest/internal/orch"
)

const (
	// uploadReadTimeout bounds how long one upload may take to arrive: 25 MB from a phone on a
	// poor link. The server has no overall read deadline (the event stream would die of it).
	uploadReadTimeout = 5 * time.Minute
	// multipartSlack is room for the multipart framing around the file.
	multipartSlack = 64 << 10
	// attachmentCSP is the policy on every served file but a PDF: a sandbox (no script, a unique
	// origin) and nothing loaded.
	attachmentCSP = "sandbox; default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; frame-ancestors 'none'"
	// codeTooLarge and codeQuota are the upload refusals the web app explains.
	codeTooLarge = "TOO_LARGE"
	codeQuota    = "QUOTA_FULL"
	// HeaderFilename names a raw-body upload (URL-encoded).
	HeaderFilename = "X-Filename"
)

// AttachmentLimits are the limits the web app checks before it uploads (features.attachments).
type AttachmentLimits struct {
	MaxBytes int64 `json:"max_bytes"`
	MaxFiles int   `json:"max_files"`
}

func (s *Server) registerAttachmentRoutes(mux *http.ServeMux) {
	mux.Handle("POST /api/v1/attachments", s.requireAuth(http.HandlerFunc(s.handleAttachmentUpload)))
	mux.Handle("GET /api/v1/attachments", s.requireAuth(http.HandlerFunc(s.handleAttachmentList)))
	mux.Handle("GET /api/v1/attachments/{id}", s.requireAuth(http.HandlerFunc(s.handleAttachmentGet)))
	mux.Handle("GET /api/v1/attachments/{id}/thumb", s.requireAuth(http.HandlerFunc(s.handleAttachmentThumb)))
	mux.Handle("GET /api/v1/attachments/{id}/meta", s.requireAuth(http.HandlerFunc(s.handleAttachmentMeta)))
	mux.Handle("DELETE /api/v1/attachments/{id}", s.requireAuth(http.HandlerFunc(s.handleAttachmentDiscard)))
}

func (s *Server) attachments(w http.ResponseWriter) *attach.Service {
	if s.deps.Attachments == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "Attachments are not available on this server.")
	}
	return s.deps.Attachments
}

// attachmentLimits is features.attachments: nil when attachments are off.
func (s *Server) attachmentLimits() *AttachmentLimits {
	if s.deps.Attachments == nil {
		return nil
	}
	limits := s.deps.Attachments.Limits()
	return &AttachmentLimits{MaxBytes: limits.MaxFileBytes, MaxFiles: limits.MaxPerMessage}
}

// ---- Upload ------------------------------------------------------------------------------

// handleAttachmentUpload takes one file: multipart/form-data with a "file" part (a browser
// form), or the raw bytes as the body with the name in X-Filename (what the web app sends, so
// it can report progress per file). The answer is the pending attachment.
func (s *Server) handleAttachmentUpload(w http.ResponseWriter, r *http.Request) {
	service := s.attachments(w)
	if service == nil {
		return
	}
	if err := http.NewResponseController(w).SetReadDeadline(time.Now().Add(uploadReadTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		s.internalError(w, r, "setting the upload read deadline", err)
		return
	}
	limit := service.Limits().MaxFileBytes
	if r.ContentLength > limit+multipartSlack {
		writeTooLarge(w, limit)
		return
	}
	body := http.MaxBytesReader(w, r.Body, limit+multipartSlack)
	name, reader, cleanup, err := uploadPart(r, body)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, err.Error())
		return
	}
	defer cleanup()
	attachment, err := service.Upload(r.Context(), name, reader)
	if err != nil {
		s.writeAttachmentError(w, r, err)
		return
	}
	s.deps.Logger.Info("attachment uploaded", "attachment", attachment.ID, "type", attachment.Type, "bytes", attachment.Size)
	writeData(w, http.StatusCreated, attachment)
}

// uploadPart finds the file in the request: the first "file" part of a multipart body, or the
// raw body named by X-Filename.
func uploadPart(r *http.Request, body io.Reader) (string, io.Reader, func(), error) {
	mediaType, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != "multipart/form-data" {
		name, err := url.PathUnescape(r.Header.Get(HeaderFilename))
		if err != nil {
			name = r.Header.Get(HeaderFilename)
		}
		return name, body, func() {}, nil
	}
	reader := multipart.NewReader(body, params["boundary"])
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return "", nil, nil, errors.New(`send the file in a multipart part named "file"`)
		}
		if err != nil {
			return "", nil, nil, errors.New("the upload could not be read; try again")
		}
		if part.FormName() == "file" {
			return part.FileName(), part, func() { _ = part.Close() }, nil
		}
		_ = part.Close()
	}
}

// ---- Download ----------------------------------------------------------------------------

// handleAttachmentGet serves a file's bytes: inline only for the safe types, else as a download;
// ?download=1 forces a download. Ranges work (a PDF viewer asks for them).
func (s *Server) handleAttachmentGet(w http.ResponseWriter, r *http.Request) {
	service := s.attachments(w)
	if service == nil {
		return
	}
	attachment, file, err := service.Open(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeAttachmentError(w, r, err)
		return
	}
	defer func() { _ = file.Close() }()
	inline := attachment.Inline && r.URL.Query().Get("download") == ""
	header := w.Header()
	header.Set("Content-Type", attach.ServedType(attachment.Type))
	header.Set("Content-Disposition", disposition(inline, attachment.Name))
	header.Set("Cache-Control", "private, max-age=86400")
	header.Set("X-Content-Type-Options", "nosniff")
	if attachment.Type != "application/pdf" || !inline {
		header.Set("Content-Security-Policy", attachmentCSP)
	}
	http.ServeContent(w, r, "", attachment.CreatedAt, file)
}

// handleAttachmentThumb serves an image's thumbnail (a JPEG made by the server, inline).
func (s *Server) handleAttachmentThumb(w http.ResponseWriter, r *http.Request) {
	service := s.attachments(w)
	if service == nil {
		return
	}
	file, err := service.OpenThumb(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeAttachmentError(w, r, err)
		return
	}
	defer func() { _ = file.Close() }()
	header := w.Header()
	header.Set("Content-Type", "image/jpeg")
	header.Set("Content-Disposition", "inline")
	header.Set("Cache-Control", "private, max-age=86400")
	header.Set("Content-Security-Policy", attachmentCSP)
	http.ServeContent(w, r, "", time.Time{}, file)
}

// disposition is the Content-Disposition header, the name encoded per RFC 6266 (mime escapes
// quotes and non-ASCII), so a name can never add a parameter of its own.
func disposition(inline bool, name string) string {
	kind := "attachment"
	if inline {
		kind = "inline"
	}
	if value := mime.FormatMediaType(kind, map[string]string{"filename": name}); value != "" {
		return value
	}
	return kind
}

// handleAttachmentMeta returns one attachment's record (the conversation view's chips find a
// file from the id in a typed "Attached:" line).
func (s *Server) handleAttachmentMeta(w http.ResponseWriter, r *http.Request) {
	service := s.attachments(w)
	if service == nil {
		return
	}
	attachment, err := service.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeAttachmentError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, attachment)
}

// handleAttachmentList lists an owner's files: ?owner=job:j_3 (or question:q_7, orchestrator:),
// optionally &role=result; or ?answer=q_7, the files the human sent with that answer.
func (s *Server) handleAttachmentList(w http.ResponseWriter, r *http.Request) {
	service := s.attachments(w)
	if service == nil {
		return
	}
	query := r.URL.Query()
	var list []attach.Attachment
	var err error
	switch {
	case query.Get("answer") != "":
		list, err = service.ListByRef(r.Context(), attach.RoleAnswer, query.Get("answer"))
	case query.Get("owner") != "":
		kind, id, _ := strings.Cut(query.Get("owner"), ":")
		list, err = service.List(r.Context(), kind, id, query.Get("role"))
	default:
		writeError(w, http.StatusBadRequest, codeValidation, "Name an owner (?owner=job:j_3) or an answer (?answer=q_7).")
		return
	}
	if err != nil {
		s.internalError(w, r, "listing attachments", err)
		return
	}
	writeList(w, list, ListMeta{Total: len(list)})
}

// handleAttachmentDiscard removes an upload that was never sent (the composer's ×).
func (s *Server) handleAttachmentDiscard(w http.ResponseWriter, r *http.Request) {
	service := s.attachments(w)
	if service == nil {
		return
	}
	if err := service.Discard(r.Context(), r.PathValue("id")); err != nil {
		s.writeAttachmentError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- Errors ------------------------------------------------------------------------------

func writeTooLarge(w http.ResponseWriter, limit int64) {
	writeError(w, http.StatusRequestEntityTooLarge, codeTooLarge,
		fmt.Sprintf("That file is larger than %s, the most crowsnest takes. Share a smaller one, or put it where the agent can read it and name the path.", attach.FormatSize(limit)))
}

func (s *Server) writeAttachmentError(w http.ResponseWriter, r *http.Request, err error) {
	var tooLarge *attach.TooLargeError
	var maxBytes *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		writeTooLarge(w, tooLarge.Limit)
	case errors.As(err, &maxBytes):
		writeTooLarge(w, s.deps.Attachments.Limits().MaxFileBytes)
	case errors.Is(err, attach.ErrQuota):
		writeError(w, http.StatusInsufficientStorage, codeQuota, "Attachment storage is full. "+strings.TrimPrefix(err.Error(), attach.ErrQuota.Error()+": "))
	case errors.Is(err, attach.ErrNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, "No such attachment (it may have been cleaned up to make room).")
	case errors.Is(err, attach.ErrNotPending):
		writeError(w, http.StatusConflict, codeConflict, "That file was already sent; attach it again to send it again.")
	case errors.Is(err, attach.ErrEmpty):
		writeError(w, http.StatusBadRequest, codeValidation, "That file is empty.")
	case errors.Is(err, attach.ErrTooMany):
		writeError(w, http.StatusBadRequest, codeValidation, fmt.Sprintf("At most %d files can go with one message.", s.deps.Attachments.Limits().MaxPerMessage))
	default:
		s.internalError(w, r, "handling an attachment", err)
	}
}

// ---- Sending with a message --------------------------------------------------------------

// deliverAttachments copies a message's uploads into the inbox of the agent in paneID (or the
// job jobID) and returns them; the caller types attach.WithLines(text, them). ok is false when a
// response was written. No ids: nothing happens.
func (s *Server) deliverAttachments(w http.ResponseWriter, r *http.Request, ids []string, inbox attach.Inbox, role, ref string) ([]attach.Attachment, bool) {
	if len(ids) == 0 {
		return nil, true
	}
	service := s.attachments(w)
	if service == nil {
		return nil, false
	}
	delivered, err := service.Deliver(r.Context(), ids, inbox, role, ref)
	if err != nil {
		service.Undeliver(r.Context(), delivered)
		var notFound = errors.Is(err, attach.ErrNotFound)
		if notFound || errors.Is(err, attach.ErrNotPending) || errors.Is(err, attach.ErrTooMany) {
			s.writeAttachmentError(w, r, err)
			return nil, false
		}
		s.internalError(w, r, "copying attachments for the agent", err)
		return nil, false
	}
	return delivered, true
}

// takeBack undoes a delivery when the message that named it provably reached no one.
func (s *Server) takeBack(ctx context.Context, delivered []attach.Attachment, err error) {
	if len(delivered) == 0 || s.deps.Attachments == nil || !nothingTyped(err) {
		return
	}
	s.deps.Attachments.Undeliver(context.WithoutCancel(ctx), delivered)
}

// nothingTyped reports whether a send error means the text never reached the pane: a dialog, a
// draft, a refusal before typing, an empty or long text.
func nothingTyped(err error) bool {
	var blocked *orch.BlockedError
	var draft *orch.DraftError
	return errors.As(err, &blocked) || errors.As(err, &draft) || errors.Is(err, orch.ErrNotTyped) ||
		errors.Is(err, orch.ErrEmptyPrompt) || errors.Is(err, orch.ErrPromptTooLong)
}

// inboxForPane is where files for the agent in paneID go: the orchestrator's inbox for its pane,
// the job's for a pane holding a live job, else the pane's own.
func (s *Server) inboxForPane(ctx context.Context, paneID string) attach.Inbox {
	layout := s.deps.AttachLayout
	if s.deps.Orchestrator != nil && paneID != "" && s.deps.Orchestrator.Status().PaneID == paneID {
		return layout.Orchestrator()
	}
	if s.deps.Jobs != nil && paneID != "" {
		if jobID := s.deps.Jobs.JobInPane(ctx, paneID); jobID != "" {
			return s.inboxForJob(ctx, jobID)
		}
	}
	return layout.Pane(paneID)
}

// inboxForJob is a job's inbox: its own, or the orchestrator's when it holds the job.
func (s *Server) inboxForJob(ctx context.Context, jobID string) attach.Inbox {
	held := false
	if s.deps.Jobs != nil {
		if job, err := s.deps.Jobs.Get(ctx, jobID); err == nil {
			held = job.Dir == ""
		}
	}
	return s.deps.AttachLayout.Job(jobID, held)
}

// inboxForTarget resolves a prompt's {target} (an agent name or a pane id) to its inbox.
func (s *Server) inboxForTarget(ctx context.Context, target string) (attach.Inbox, error) {
	if target == orch.Name && s.deps.Orchestrator != nil {
		return s.deps.AttachLayout.Orchestrator(), nil
	}
	if s.deps.Herdr == nil {
		return attach.Inbox{}, errors.New("herdr is not available")
	}
	lookup, cancel := context.WithTimeout(ctx, herdrCallTimeout)
	defer cancel()
	if agent, err := s.deps.Herdr.AgentGet(lookup, target); err == nil {
		return s.inboxForPane(ctx, agent.PaneID), nil
	}
	return s.inboxForPane(ctx, target), nil
}
