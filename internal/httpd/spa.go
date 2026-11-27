// The single-page app: static files from the embedded web/dist, with client-side routes falling
// back to index.html. Vite fingerprints everything under /assets/, so those files are cached
// forever; index.html and the other root files are revalidated on every load so a new build
// is picked up immediately.
//
// index.html carries one per-server value: the default theme (the "theme" setting) as
// data-theme-default on <html>, so public/theme-init.js applies it before first paint in a
// browser that has not chosen a theme of its own.

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
	return &spa{files: s.deps.Web, themeDefault: s.themeDefault}
}

// themeDefault is the "theme" setting now, or "" (the house theme) without a System.
func (s *Server) themeDefault() string {
	if s.deps.System == nil {
		return ""
	}
	return s.deps.System.Config().Theme
}

// devPlaceholder answers / when --web-dev is on: the UI comes from Vite, not from here.
func devPlaceholder(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "crowsnest API is running. In development the UI is served by Vite: open http://localhost:5173\n")
}

type spa struct {
	files        fs.FS
	themeDefault func() string
}

// htmlOpen is the <html> tag of web/index.html, where the default theme goes.
const htmlOpen = `<html lang="en">`

func (h *spa) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, codeValidation, "Only GET is supported here.")
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "index.html" {
		h.serveIndex(w, r)
		return
	}
	if name != "" && h.serveFile(w, r, name) {
		return
	}
	// A missing fingerprinted asset or any other file-looking path is a real 404: answering it
	// with index.html would make the browser parse HTML as JavaScript and fail confusingly.
	if name == "assets" || strings.HasPrefix(name, "assets/") || path.Ext(name) != "" {
		http.NotFound(w, r)
		return
	}
	h.serveIndex(w, r)
}

// serveIndex serves index.html with the default theme written into its <html> tag.
func (h *spa) serveIndex(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(h.files, "index.html")
	if err != nil {
		http.Error(w, "web app missing from this build", http.StatusInternalServerError)
		return
	}
	if theme := h.themeDefault(); theme != "" {
		// The setting is checked against config's theme-id pattern ([a-z0-9-]) when it is loaded,
		// so it needs no escaping here.
		data = bytes.Replace(data, []byte(htmlOpen), []byte(`<html lang="en" data-theme-default="`+theme+`">`), 1)
	}
	w.Header().Set("Cache-Control", cacheRevalidate)
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(data))
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
