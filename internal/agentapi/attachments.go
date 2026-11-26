// Files agents attach (DESIGN.md 8.6): `ask` with attachments puts them on the question card, and
// `done` with attachments on the finished job. The agent names paths; the server reads a file
// only if it lies inside the caller's own working directory or job directory (attach's os.Root
// confinement), never anything else the user can read, and only for a caller with a token: an
// unidentified process cannot have crowsnest read files for it.

package agentapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/jclement/crowsnest/internal/attach"
	"github.com/jclement/crowsnest/internal/questions"
)

// importFiles stores the files a request names, pending until adopted. ok is false when a refusal
// was written. No paths: nothing happens.
func (s *Server) importFiles(w http.ResponseWriter, r *http.Request, caller questions.Caller, paths []string) ([]attach.Attachment, bool) {
	if len(paths) == 0 {
		return nil, true
	}
	if s.attachments == nil {
		writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "Attachments are not available on this crowsnest; leave attachments empty.")
		return nil, false
	}
	if caller.PaneID == "" {
		writeError(w, http.StatusForbidden, codeForbidden, "Only an agent crowsnest started can attach files (no valid CROWSNEST_TOKEN came with this request).")
		return nil, false
	}
	var roots []string
	if s.attachRoots != nil {
		roots = s.attachRoots(r.Context(), caller)
	}
	files, err := s.attachments.ImportAgentFiles(r.Context(), paths, roots, caller.PaneID)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, attachRefusal(err))
		return nil, false
	}
	return files, true
}

// attachRefusal words an import failure for the model.
func attachRefusal(err error) string {
	message := "A file could not be attached: " + err.Error() + "."
	if errors.Is(err, attach.ErrOutside) {
		message += " Only files inside your working directory or your job directory can be attached; copy the file there first."
	}
	return message
}

// adoptFiles gives imported files their owner and returns them as owned. A failure is logged:
// the question or result stands without them.
func (s *Server) adoptFiles(ctx context.Context, files []attach.Attachment, kind, ownerID, role string) []attach.Attachment {
	if len(files) == 0 {
		return nil
	}
	ids := make([]string, 0, len(files))
	for i := range files {
		ids = append(ids, files[i].ID)
		files[i].OwnerKind, files[i].OwnerID, files[i].Role = kind, ownerID, role
	}
	if err := s.attachments.Adopt(ctx, ids, kind, ownerID, role); err != nil {
		s.logger.Warn("attaching an agent's files failed", "owner", kind+":"+ownerID, "err", err)
		return nil
	}
	return files
}

// discardFiles drops imported files whose request failed afterwards.
func (s *Server) discardFiles(ctx context.Context, files []attach.Attachment) {
	for _, file := range files {
		_ = s.attachments.Discard(context.WithoutCancel(ctx), file.ID)
	}
}
