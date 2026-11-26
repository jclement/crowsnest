// Package web embeds the built single-page app (web/dist) into the crowsnest binary.
//
// dist/.gitkeep is committed (Vite copies it back from public/ on every build) so that this
// package always compiles, even before the first `npm run build`. A binary built that way has no
// index.html; HasApp reports it and `serve` refuses to start rather than serve a blank page.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the app's files with index.html at the root.
func Dist() fs.FS {
	files, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // "dist" is a literal embedded directory; this cannot fail at run time
	}
	return files
}

// HasApp reports whether the web app was built into this binary.
func HasApp() bool {
	_, err := fs.Stat(Dist(), "index.html")
	return err == nil
}
