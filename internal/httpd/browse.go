// The folder picker's routes (DESIGN.md 4.23, 10): one directory's listing, the facts about a
// folder, and the places the picker starts from. All three read only, all three need the
// owner's browser session (requireAuth: no read token reaches them, and the agent socket has no
// such routes), and none ever returns a file's contents. internal/browse holds the rules.

package httpd

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/jclement/crowsnest/internal/browse"
	"github.com/jclement/crowsnest/internal/projects"
)

// codeTimeout answers a folder that took too long to read (a dead network mount).
const codeTimeout = "TIMEOUT"

func (s *Server) registerBrowseRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/browse", s.requireAuth(http.HandlerFunc(s.handleBrowseList)))
	mux.Handle("GET /api/v1/browse/facts", s.requireAuth(http.HandlerFunc(s.handleBrowseFacts)))
	mux.Handle("GET /api/v1/browse/places", s.requireAuth(http.HandlerFunc(s.handleBrowsePlaces)))
}

func (s *Server) browser(w http.ResponseWriter) *browse.Service {
	if s.deps.Browse == nil {
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "The folder picker is not available on this server.")
	}
	return s.deps.Browse
}

// handleBrowseList is GET /browse?path=&files=1&hidden=1; no path lists the home directory.
func (s *Server) handleBrowseList(w http.ResponseWriter, r *http.Request) {
	service := s.browser(w)
	if service == nil {
		return
	}
	query := r.URL.Query()
	path := query.Get("path")
	if path == "" {
		path = "~"
	}
	listing, err := service.List(r.Context(), path, browse.ListOptions{Files: query.Get("files") == "1", Hidden: query.Get("hidden") == "1"})
	if err != nil {
		s.writeBrowseError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, listing)
}

// FolderFacts is GET /browse/facts: the folder's own facts plus how it relates to projects.
type FolderFacts struct {
	browse.Facts
	// Project is the live project whose directory holds this folder (Exact: it is that
	// directory), nil for none.
	Project *FolderProject `json:"project"`
	// Contains are live projects inside this folder: a project here would overlap them.
	Contains []FolderProject `json:"contains"`
}

// FolderProject names a project in FolderFacts.
type FolderProject struct {
	ID    int64  `json:"id"`
	Slug  string `json:"slug"`
	Name  string `json:"name"`
	Dir   string `json:"dir"`
	Exact bool   `json:"exact"`
}

func (s *Server) handleBrowseFacts(w http.ResponseWriter, r *http.Request) {
	service := s.browser(w)
	if service == nil {
		return
	}
	facts, err := service.Facts(r.Context(), r.URL.Query().Get("path"))
	if err != nil {
		s.writeBrowseError(w, r, err)
		return
	}
	out := FolderFacts{Facts: facts, Contains: []FolderProject{}}
	if s.deps.Projects != nil {
		out.Project, out.Contains = s.projectsAround(r.Context(), facts.Resolved)
	}
	writeData(w, http.StatusOK, out)
}

// projectsAround finds the live project holding resolved and the live projects inside it.
func (s *Server) projectsAround(ctx context.Context, resolved string) (*FolderProject, []FolderProject) {
	contains := []FolderProject{}
	var holder *FolderProject
	if project, ok := s.deps.Projects.ForDir(ctx, resolved); ok {
		holder = folderProject(project, project.RootDir == resolved)
	}
	list, err := s.deps.Projects.List(ctx, false)
	if err != nil {
		return holder, contains
	}
	for _, project := range list {
		if project.RootDir != resolved && pathInside(project.RootDir, resolved) {
			contains = append(contains, *folderProject(project, false))
		}
	}
	return holder, contains
}

func folderProject(project projects.Project, exact bool) *FolderProject {
	return &FolderProject{ID: project.ID, Slug: project.Slug, Name: project.Name, Dir: project.Dir, Exact: exact}
}

// BrowsePlaces is GET /browse/places: where the picker starts.
type BrowsePlaces struct {
	Home       string         `json:"home"`
	Favourites []browse.Place `json:"favourites"`
	Roots      []browse.Place `json:"roots"`
}

func (s *Server) handleBrowsePlaces(w http.ResponseWriter, r *http.Request) {
	service := s.browser(w)
	if service == nil {
		return
	}
	places, err := service.Places(r.Context())
	if err != nil {
		s.writeBrowseError(w, r, err)
		return
	}
	writeData(w, http.StatusOK, BrowsePlaces{Home: service.Home(), Favourites: places.Favourites, Roots: places.Roots})
}

// writeBrowseError answers with crowsnest's own sentence for each refusal; the filesystem's
// error text (which holds paths) goes only to the log.
func (s *Server) writeBrowseError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, browse.ErrInvalid):
		writeError(w, http.StatusBadRequest, codeValidation, "Give an absolute path, or one that starts with ~.")
	case errors.Is(err, browse.ErrOutside):
		writeError(w, http.StatusForbidden, codeForbidden, "That folder is outside what the picker may browse: your home, project and remembered folders, and any --browse-root.")
	case errors.Is(err, browse.ErrNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, "There is no such folder.")
	case errors.Is(err, browse.ErrNotDir):
		writeError(w, http.StatusBadRequest, codeValidation, "That is a file, not a folder.")
	case errors.Is(err, browse.ErrUnreadable):
		writeError(w, http.StatusForbidden, codeForbidden, "crowsnest may not read that folder (permission denied).")
	case errors.Is(err, browse.ErrTimeout):
		writeError(w, http.StatusGatewayTimeout, codeTimeout, "That folder took too long to read; it may be on a slow or disconnected network mount.")
	case errors.Is(err, browse.ErrBusy):
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusServiceUnavailable, codeUnavailable, "Other folders are still being read; try again in a moment.")
	case errors.Is(err, context.Canceled):
		// The browser went away; nobody reads an answer.
	default:
		s.internalError(w, r, "browsing a folder", err)
	}
}

// pathInside reports whether path is strictly inside dir (both clean and absolute).
func pathInside(path, dir string) bool {
	return dir != "" && strings.HasPrefix(path, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator))
}
