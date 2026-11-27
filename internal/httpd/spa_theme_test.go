package httpd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// TestSPAWritesTheDefaultTheme: index.html carries the "theme" setting as data-theme-default on
// <html>, for every route that serves it, so a new browser's first paint is already in it; with
// no setting the page is served as built.
func TestSPAWritesTheDefaultTheme(t *testing.T) {
	files := fstest.MapFS{"index.html": {Data: []byte(`<!doctype html>` + htmlOpen + `<head></head></html>`)}}
	tests := []struct {
		theme, path, want string
	}{
		{"phosphor", "/", `<html lang="en" data-theme-default="phosphor">`},
		{"phosphor", "/jobs/deep", `<html lang="en" data-theme-default="phosphor">`},
		{"deep-sea", "/index.html", `<html lang="en" data-theme-default="deep-sea">`},
		{"", "/", htmlOpen},
	}
	for _, tc := range tests {
		handler := &spa{files: files, themeDefault: func() string { return tc.theme }}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), tc.want) {
			t.Errorf("theme %q, %s: %d %q, want it to contain %q", tc.theme, tc.path, response.Code, response.Body, tc.want)
		}
		if got := response.Header().Get("Cache-Control"); got != cacheRevalidate {
			t.Errorf("theme %q, %s: Cache-Control %q", tc.theme, tc.path, got)
		}
	}
}
