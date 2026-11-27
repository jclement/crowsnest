// Instruction layers (DESIGN.md 10 and 21.3): the human's standing instructions crowsnest adds
// to the prompt of every agent it starts, and what each agent was started with and loaded.
//
//	GET    /instructions                          every layer, where its text comes from, built-ins
//	GET    /instructions/{scope}/{key}            one layer with its kept revisions
//	PUT    /instructions/{scope}/{key}            {body_md}: a new revision
//	DELETE /instructions/role/{name}              reset a role to its built-in
//	POST   /instructions/{scope}/{key}/restore    {revision}: that text again, as a new revision
//	GET    /agents-instructions/{subject}         an agent's layers at start, changed?, files it loaded
//
// The global layer's key is "-" in a URL. Only crowsnest's own layers are served as text; the
// user's own instruction files are listed by path and never read (21.3).

package httpd

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jclement/crowsnest/internal/instructions"
)

// maxInstructionsBody bounds a PUT: the layer cap plus room for the JSON around it (escaping
// can double a body's size).
const maxInstructionsBody = 2*instructions.MaxBodyBytes + 1<<10

// globalKeyInURL stands for the global layer's empty key in a path.
const globalKeyInURL = "-"

func (s *Server) registerInstructionRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/instructions", s.requireAuth(http.HandlerFunc(s.handleInstructionsList)))
	mux.Handle("GET /api/v1/instructions/{scope}/{key}", s.requireAuth(http.HandlerFunc(s.handleInstructionsGet)))
	mux.Handle("PUT /api/v1/instructions/{scope}/{key}", s.requireAuth(http.HandlerFunc(s.handleInstructionsPut)))
	mux.Handle("POST /api/v1/instructions/role/{name}/file-seen", s.requireAuth(http.HandlerFunc(s.handleRoleFileSeen)))
	mux.Handle("DELETE /api/v1/instructions/role/{name}", s.requireAuth(http.HandlerFunc(s.handleInstructionsReset)))
	mux.Handle("POST /api/v1/instructions/{scope}/{key}/restore", s.requireAuth(http.HandlerFunc(s.handleInstructionsRestore)))
	mux.Handle("GET /api/v1/agents-instructions/{subject}", s.requireAuth(http.HandlerFunc(s.handleAgentInstructions)))
}

func (s *Server) instructionsService(w http.ResponseWriter) *instructions.Service {
	if s.deps.Instructions == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "Instruction layers are not available in this server.")
	}
	return s.deps.Instructions
}

// layerTarget reads {scope} and {key} from the path, the global layer's "-" as "".
func layerTarget(r *http.Request) (scope, key string) {
	scope, key = r.PathValue("scope"), r.PathValue("key")
	if scope == instructions.ScopeGlobal && key == globalKeyInURL {
		key = ""
	}
	return scope, key
}

func (s *Server) handleInstructionsList(w http.ResponseWriter, r *http.Request) {
	service := s.instructionsService(w)
	if service == nil {
		return
	}
	listing, err := service.List(r.Context())
	if err != nil {
		s.instructionsError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, listing)
}

func (s *Server) handleInstructionsGet(w http.ResponseWriter, r *http.Request) {
	if service := s.instructionsService(w); service != nil {
		scope, key := layerTarget(r)
		s.writeLayer(w, r, service, scope, key)
	}
}

// handleRoleFileSeen records that the human read a role file as it is now (R5-S14): the body
// names the hash of the text they were shown, and a file changed since is refused (409).
func (s *Server) handleRoleFileSeen(w http.ResponseWriter, r *http.Request) {
	service := s.instructionsService(w)
	if service == nil {
		return
	}
	var input struct {
		Hash string `json:"hash"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&input); err != nil || input.Hash == "" {
		writeError(w, http.StatusBadRequest, codeValidation, `Send JSON like {"hash": "<the file_hash the layer showed>"}.`)
		return
	}
	name := r.PathValue("name")
	if err := service.MarkFileSeen(r.Context(), name, input.Hash); err != nil {
		s.instructionsError(w, r, err)
		return
	}
	s.writeLayer(w, r, service, instructions.ScopeRole, name)
}

type instructionsPutRequest struct {
	BodyMD *string `json:"body_md"`
}

func (s *Server) handleInstructionsPut(w http.ResponseWriter, r *http.Request) {
	service := s.instructionsService(w)
	if service == nil {
		return
	}
	var body instructionsPutRequest
	if !decodeStrict(w, r, maxInstructionsBody, &body) {
		return
	}
	if body.BodyMD == nil {
		writeError(w, http.StatusBadRequest, codeValidation, `Send JSON like {"body_md": "…"}.`)
		return
	}
	scope, key := layerTarget(r)
	if err := service.Save(r.Context(), scope, key, *body.BodyMD); err != nil {
		s.instructionsError(w, r, err)
		return
	}
	s.writeLayer(w, r, service, scope, key)
}

func (s *Server) handleInstructionsReset(w http.ResponseWriter, r *http.Request) {
	service := s.instructionsService(w)
	if service == nil {
		return
	}
	name := r.PathValue("name")
	if err := service.Reset(r.Context(), name); err != nil {
		s.instructionsError(w, r, err)
		return
	}
	s.writeLayer(w, r, service, instructions.ScopeRole, name)
}

type instructionsRestoreRequest struct {
	Revision int `json:"revision"`
}

func (s *Server) handleInstructionsRestore(w http.ResponseWriter, r *http.Request) {
	service := s.instructionsService(w)
	if service == nil {
		return
	}
	var body instructionsRestoreRequest
	if !decodeStrict(w, r, 1<<10, &body) {
		return
	}
	if body.Revision <= 0 {
		writeError(w, http.StatusBadRequest, codeValidation, `Send JSON like {"revision": 3}.`)
		return
	}
	scope, key := layerTarget(r)
	if err := service.Restore(r.Context(), scope, key, body.Revision); err != nil {
		s.instructionsError(w, r, err)
		return
	}
	s.writeLayer(w, r, service, scope, key)
}

func (s *Server) handleAgentInstructions(w http.ResponseWriter, r *http.Request) {
	service := s.instructionsService(w)
	if service == nil {
		return
	}
	view, err := service.Agent(r.Context(), r.PathValue("subject"))
	if err != nil {
		s.instructionsError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, view)
}

// writeLayer answers with one layer and its revisions: what every write returns too.
func (s *Server) writeLayer(w http.ResponseWriter, r *http.Request, service *instructions.Service, scope, key string) {
	detail, err := service.Get(r.Context(), scope, key)
	if err != nil {
		s.instructionsError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, detail)
}

// decodeStrict reads a small JSON body that must hold only known fields, answering 400 itself.
func decodeStrict(w http.ResponseWriter, r *http.Request, limit int64, into any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		writeError(w, http.StatusBadRequest, codeValidation, "The request body must be JSON with only the documented fields: "+err.Error())
		return false
	}
	return true
}

// instructionsError maps the service's error kinds; their messages are written for the human.
func (s *Server) instructionsError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, instructions.ErrInvalid):
		writeError(w, http.StatusBadRequest, codeValidation, err.Error())
	case errors.Is(err, instructions.ErrNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, err.Error())
	case errors.Is(err, instructions.ErrFileChanged):
		writeError(w, http.StatusConflict, codeConflict, err.Error()+"; read it again")
	case errors.Is(err, instructions.ErrReadOnly):
		writeError(w, http.StatusConflict, codeConflict, err.Error())
	default:
		s.internalError(w, r, "reading or changing instruction layers", err)
	}
}
