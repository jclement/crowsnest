// What a file is, decided from its bytes, and whether a browser may show it inline (DESIGN.md
// 8.6). The client's Content-Type is never believed: a browser labels a file by its extension,
// and an agent or a page could label anything as anything.
//
// The served-inline set is deliberately small: raster images a browser decodes without running
// anything, PDF (its viewer runs scripts in its own sandbox, not in this origin), and plain text
// served as text/plain. SVG and HTML are active content in this origin (script, links, forms),
// so they are always a download, whatever they were called; so is anything else.

package attach

import (
	"bytes"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
)

// sniffBytes is how much of a file decides its type: what http.DetectContentType reads.
const sniffBytes = 512

// Media types crowsnest names itself (DetectContentType has no notion of these).
const (
	typeSVG      = "image/svg+xml"
	typeHEIC     = "image/heic"
	typeMarkdown = "text/markdown"
	typePlain    = "text/plain"
	typeOctet    = "application/octet-stream"
)

// inlineTypes are the sniffed types a browser may render in this origin.
var inlineTypes = map[string]bool{
	"image/png":       true,
	"image/jpeg":      true,
	"image/gif":       true,
	"image/webp":      true,
	"image/bmp":       true,
	"application/pdf": true,
	typePlain:         true,
	typeMarkdown:      true, // served as text/plain
}

// activeExtensions are names a browser or OS would treat as active content even when the bytes
// sniffed as something harmless (an HTML file that starts with plain words sniffs as text/plain).
// Such a file is never inline, so the name cannot talk a browser into rendering it.
var activeExtensions = map[string]bool{
	".html": true, ".htm": true, ".xhtml": true, ".xht": true, ".shtml": true, ".svg": true, ".svgz": true,
	".xml": true, ".xsl": true, ".xslt": true, ".js": true, ".mjs": true, ".swf": true, ".mht": true, ".mhtml": true,
}

// thumbTypes are the types internal/attach can decode for a thumbnail (the standard library's
// decoders; WebP and HEIC need decoders crowsnest does not carry, so they get none).
var thumbTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true}

// Sniff returns the media type of a file from its first bytes, refined by its name only where the
// bytes cannot tell (Markdown is plain text by its bytes). It never returns parameters.
func Sniff(head []byte, name string) string {
	if len(head) > sniffBytes {
		head = head[:sniffBytes]
	}
	detected, _, err := mime.ParseMediaType(http.DetectContentType(head))
	if err != nil {
		detected = typeOctet
	}
	ext := strings.ToLower(filepath.Ext(name))
	switch {
	case looksSVG(head):
		return typeSVG
	case detected == typeOctet && looksHEIC(head):
		return typeHEIC
	case detected == typePlain && (ext == ".md" || ext == ".markdown"):
		return typeMarkdown
	}
	return detected
}

// looksSVG finds an <svg element near the start of an XML or text file. DetectContentType calls
// an SVG text/xml (with a declaration) or text/plain; either way it is script-capable content.
func looksSVG(head []byte) bool {
	lower := bytes.ToLower(head)
	return bytes.Contains(lower, []byte("<svg"))
}

// looksHEIC recognises the ISO base media file brands an iPhone photo carries.
func looksHEIC(head []byte) bool {
	if len(head) < 12 || !bytes.Equal(head[4:8], []byte("ftyp")) {
		return false
	}
	switch string(head[8:12]) {
	case "heic", "heix", "hevc", "hevx", "heim", "heis", "mif1", "msf1":
		return true
	}
	return false
}

// Inline reports whether a file of this sniffed type and name may be shown in the browser
// rather than downloaded.
func Inline(mediaType, name string) bool {
	return inlineTypes[mediaType] && !activeExtensions[strings.ToLower(filepath.Ext(name))]
}

// ServedType is the Content-Type header for a file: the sniffed type, with text served as
// text/plain in UTF-8 (Markdown included), so a browser shows it rather than guessing.
func ServedType(mediaType string) string {
	switch {
	case mediaType == typePlain || mediaType == typeMarkdown:
		return "text/plain; charset=utf-8"
	case strings.HasPrefix(mediaType, "text/"):
		return mediaType + "; charset=utf-8"
	}
	return mediaType
}

// IsImage reports whether the web app may show the file as a picture (a thumbnail, the
// lightbox): an inline-safe image type. SVG is an image to a browser but never shown.
func IsImage(mediaType string) bool {
	return strings.HasPrefix(mediaType, "image/") && inlineTypes[mediaType]
}
