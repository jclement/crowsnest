// Thumbnails for image attachments: a JPEG at most thumbEdge pixels on its long side, made once
// per blob at upload, so a phone listing a question's screenshots fetches a few KB per picture
// rather than the full image. Only the standard library's decoders are used (PNG, JPEG, GIF); a
// picture it cannot decode, or one too large to decode safely, simply has no thumbnail and the
// web app shows a file chip instead.

package attach

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif" // registers the GIF decoder
	"image/jpeg"
	_ "image/png" // registers the PNG decoder
	"io"
	"os"
)

const (
	// thumbEdge is the longest side of a thumbnail in pixels: twice the 160 px a card shows, for
	// a phone's high-density screen.
	thumbEdge = 320
	// maxDecodePixels refuses to decode pictures larger than this (40 megapixels, about 160 MB
	// of RGBA), so a small, highly compressed upload cannot make the server allocate gigabytes.
	maxDecodePixels = 40_000_000
	thumbQuality    = 82
)

// makeThumb decodes the image at source and writes its thumbnail to target. It reports false,
// with no error, when the picture is of a kind or a size it does not thumbnail.
func makeThumb(source, target string) (bool, error) {
	file, err := os.Open(source) //nolint:gosec // a blob path crowsnest built from a hash
	if err != nil {
		return false, err
	}
	defer func() { _ = file.Close() }()
	config, _, err := image.DecodeConfig(file)
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width*config.Height > maxDecodePixels {
		return false, nil //nolint:nilerr // undecodable or too large: no thumbnail, not a failure
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return false, err
	}
	picture, _, err := image.Decode(file)
	if err != nil {
		return false, nil //nolint:nilerr // a truncated or odd file: no thumbnail
	}
	small := scaleDown(picture, thumbEdge)
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // crowsnest's own path
	if err != nil {
		return false, err
	}
	if err := jpeg.Encode(out, small, &jpeg.Options{Quality: thumbQuality}); err != nil {
		_ = out.Close()
		return false, fmt.Errorf("encoding a thumbnail: %w", err)
	}
	return true, out.Close()
}

// scaleDown fits picture inside edge×edge by averaging the source pixels that fall in each target
// pixel (a box filter: sharp enough for screenshots, no dependency), over a white background so
// a transparent PNG's thumbnail does not turn black in JPEG.
func scaleDown(picture image.Image, edge int) *image.RGBA {
	bounds := picture.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	targetW, targetH := width, height
	if width > edge || height > edge {
		if width >= height {
			targetW, targetH = edge, max(1, height*edge/width)
		} else {
			targetW, targetH = max(1, width*edge/height), edge
		}
	}
	out := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	draw.Draw(out, out.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	for y := range targetH {
		y0, y1 := bounds.Min.Y+y*height/targetH, bounds.Min.Y+max((y+1)*height/targetH, y*height/targetH+1)
		for x := range targetW {
			x0, x1 := bounds.Min.X+x*width/targetW, bounds.Min.X+max((x+1)*width/targetW, x*width/targetW+1)
			out.Set(x, y, average(picture, x0, x1, y0, y1))
		}
	}
	return out
}

// average is the mean colour of the source rectangle [x0,x1)×[y0,y1), composited over white.
func average(picture image.Image, x0, x1, y0, y1 int) color.RGBA {
	var red, green, blue, count uint64
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			r, g, b, a := picture.At(x, y).RGBA() // premultiplied, 16-bit
			white := 0xffff - a
			red += uint64(r + white)
			green += uint64(g + white)
			blue += uint64(b + white)
			count++
		}
	}
	if count == 0 {
		return color.RGBA{R: 255, G: 255, B: 255, A: 255}
	}
	return color.RGBA{R: uint8(red / count >> 8), G: uint8(green / count >> 8), B: uint8(blue / count >> 8), A: 255} //nolint:gosec // 16-bit averages shifted to 8
}
