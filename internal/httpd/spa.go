// The single-page app: static files from the embedded web/dist, with client-side routes falling
// back to index.html. Vite fingerprints everything under /assets/, so those files are cached
// forever; index.html and the other root files are revalidated on every load so a new build
// is picked up immediately.

package httpd

import (
	"bytes"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

const (
	cacheImmutable  = "public, max-age=31536000, immutable"
	cacheRevalidate = "no-cache"
)

func (s *Server) spaHandler() http.Handler {
	if s.deps.Web == nil {
		return http.HandlerFunc(devPlaceholder)
	}
	return &spa{files: s.deps.Web}
}

// devPlaceholder answers / when --web-dev is on: the UI comes from Vite, not from here.
func devPlaceholder(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "crowsnest API is running. In development the UI is served by Vite: open http://localhost:5173\n")
}

type spa struct {
	files fs.FS
}

func (h *spa) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, codeValidation, "Only GET is supported here.")
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name != "" && h.serveFile(w, r, name) {
		return
	}
	// A missing fingerprinted asset or any other file-looking path is a real 404: answering it
	// with index.html would make the browser parse HTML as JavaScript and fail confusingly.
	if name == "assets" || strings.HasPrefix(name, "assets/") || path.Ext(name) != "" {
		http.NotFound(w, r)
		return
	}
	if !h.serveFile(w, r, "index.html") {
		http.Error(w, "web app missing from this build", http.StatusInternalServerError)
	}
}

// serveFile serves name if it is a regular file, returning false when it is absent.
func (h *spa) serveFile(w http.ResponseWriter, r *http.Request, name string) bool {
	data, err := fs.ReadFile(h.files, name)
	if err != nil {
		return false // missing, or a directory
	}
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", cacheImmutable)
	} else {
		w.Header().Set("Cache-Control", cacheRevalidate)
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
	return true
}
