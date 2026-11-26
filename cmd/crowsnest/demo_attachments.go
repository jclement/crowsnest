// The demo's attachments (DESIGN.md 8.6): one seeded question carries what an agent would attach,
// a screenshot and a Markdown report, so the Inbox shows a thumbnail, the lightbox and a download
// chip without a real agent. The screenshot is drawn here (a mock page with a highlighted box),
// written to the demo's throwaway state directory and attached through the same import an
// agent's `ask` uses.

package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"

	"github.com/jclement/crowsnest/internal/attach"
	"github.com/jclement/crowsnest/internal/questions"
)

// demoAttachmentQuestion is the seeded question that gets the files.
const demoAttachmentQuestion = "Which findings should the builder fix before merge?"

const demoReport = `# Review findings

1. **race**: fleet cache reads ` + "`snapshot`" + ` without the lock (cache.go:142).
2. **leak**: the event subscription is not closed on reconnect.
3. **naming**: ` + "`doThing`" + ` says nothing.
`

// seedDemoAttachments attaches the demo screenshot and report to the seeded question, once.
func seedDemoAttachments(ctx context.Context, files *attachWiring, service *questions.Service, stateDir string) error {
	open, err := service.List(ctx, "open")
	if err != nil {
		return err
	}
	for _, question := range open {
		if question.Title != demoAttachmentQuestion {
			continue
		}
		if existing, _ := files.service.List(ctx, attach.OwnerQuestion, question.ID, attach.RoleAsk); len(existing) > 0 {
			return nil
		}
		dir := filepath.Join(stateDir, "demo-attachments")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		shot := filepath.Join(dir, "review-screenshot.png")
		report := filepath.Join(dir, "findings.md")
		if err := os.WriteFile(shot, demoScreenshot(), 0o600); err != nil {
			return err
		}
		if err := os.WriteFile(report, []byte(demoReport), 0o600); err != nil {
			return err
		}
		imported, err := files.service.ImportAgentFiles(ctx, []string{shot, report}, []string{dir}, question.SourcePane)
		if err != nil {
			return fmt.Errorf("attaching the demo files: %w", err)
		}
		ids := make([]string, 0, len(imported))
		for _, file := range imported {
			ids = append(ids, file.ID)
		}
		return files.service.Adopt(ctx, ids, attach.OwnerQuestion, question.ID, attach.RoleAsk)
	}
	return nil
}

// demoScreenshot draws a mock page: a dark header, a sidebar, grey text lines and a red box round
// the line a reviewer flagged.
func demoScreenshot() []byte {
	const width, height = 960, 600
	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	fill := func(x0, y0, x1, y1 int, c color.RGBA) {
		draw.Draw(picture, image.Rect(x0, y0, x1, y1), image.NewUniform(c), image.Point{}, draw.Src)
	}
	fill(0, 0, width, height, color.RGBA{R: 250, G: 250, B: 250, A: 255})
	fill(0, 0, width, 48, color.RGBA{R: 24, G: 28, B: 36, A: 255})
	fill(16, 16, 120, 32, color.RGBA{R: 96, G: 165, B: 250, A: 255})
	fill(0, 48, 200, height, color.RGBA{R: 236, G: 238, B: 242, A: 255})
	for i := range 8 {
		fill(20, 72+i*36, 170, 88+i*36, color.RGBA{R: 205, G: 210, B: 218, A: 255})
	}
	for i := range 12 {
		lineWidth := 520 + (i*97)%200
		fill(232, 80+i*40, 232+lineWidth, 96+i*40, color.RGBA{R: 214, G: 218, B: 224, A: 255})
	}
	red := color.RGBA{R: 220, G: 38, B: 38, A: 255}
	const x0, y0, x1, y1 = 222, 230, 900, 266
	fill(x0, y0, x1, y0+4, red)
	fill(x0, y1-4, x1, y1, red)
	fill(x0, y0, x0+4, y1, red)
	fill(x1-4, y0, x1, y1, red)
	var buffer bytes.Buffer
	_ = png.Encode(&buffer, picture)
	return buffer.Bytes()
}
