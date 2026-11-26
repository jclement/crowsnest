// The Inbox's questions (DESIGN.md sections 8 and 10): list, read, answer and cancel from the
// browser, plus the badge count. Every route needs a session; the answer and the cancel are
// non-GET, so the origin check and the X-Crowsnest header (middleware.go) guard them against
// cross-site requests like every other state change.

package httpd

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jclement/crowsnest/internal/questions"
)

// maxAnswerBody caps an answer: a few keys and a note.
const maxAnswerBody = 16 << 10

func (s *Server) registerQuestionRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/questions", s.requireAuth(http.HandlerFunc(s.handleQuestionList)))
	mux.Handle("GET /api/v1/questions/{id}", s.requireAuth(http.HandlerFunc(s.handleQuestionGet)))
	mux.Handle("POST /api/v1/questions/{id}/answer", s.requireAuth(http.HandlerFunc(s.handleQuestionAnswer)))
	mux.Handle("DELETE /api/v1/questions/{id}", s.requireAuth(http.HandlerFunc(s.handleQuestionCancel)))
	mux.Handle("GET /api/v1/inbox/summary", s.requireAuth(http.HandlerFunc(s.handleInboxSummary)))
}

// questionService returns the service, or answers 503 when this server has none.
func (s *Server) questionService(w http.ResponseWriter) *questions.Service {
	if s.deps.Questions == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "Questions are not available on this server.")
	}
	return s.deps.Questions
}

func (s *Server) handleQuestionList(w http.ResponseWriter, r *http.Request) {
	service := s.questionService(w)
	if service == nil {
		return
	}
	list, err := service.List(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		s.writeQuestionError(w, r, err)
		return
	}
	writeList(w, list, ListMeta{Total: len(list)})
}

func (s *Server) handleQuestionGet(w http.ResponseWriter, r *http.Request) {
	service := s.questionService(w)
	if service == nil {
		return
	}
	question, err := service.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeQuestionError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, question)
}

func (s *Server) handleQuestionAnswer(w http.ResponseWriter, r *http.Request) {
	service := s.questionService(w)
	if service == nil {
		return
	}
	var answer questions.AnswerInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAnswerBody)).Decode(&answer); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, "Send JSON with answer_value and answer_note.")
		return
	}
	question, err := service.Answer(r.Context(), r.PathValue("id"), answer)
	if err != nil {
		s.writeQuestionError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, question)
}

func (s *Server) handleQuestionCancel(w http.ResponseWriter, r *http.Request) {
	service := s.questionService(w)
	if service == nil {
		return
	}
	question, err := service.Cancel(r.Context(), r.PathValue("id"), false)
	if err != nil {
		s.writeQuestionError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, question)
}

func (s *Server) handleInboxSummary(w http.ResponseWriter, r *http.Request) {
	service := s.questionService(w)
	if service == nil {
		return
	}
	summary, err := service.Summary(r.Context())
	if err != nil {
		s.internalError(w, r, "counting open questions", err)
		return
	}
	writeData(w, http.StatusOK, summary)
}

func (s *Server) writeQuestionError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case questions.IsValidation(err):
		writeError(w, http.StatusBadRequest, codeValidation, err.Error())
	case errors.Is(err, questions.ErrNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, "No such question.")
	case errors.Is(err, questions.ErrNotOpen):
		writeError(w, http.StatusConflict, codeConflict, "This question was already answered, expired or cancelled.")
	default:
		s.internalError(w, r, "handling a question", err)
	}
}
