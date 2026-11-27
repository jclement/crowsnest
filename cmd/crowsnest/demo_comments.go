// --demo for line comments on the diff (DESIGN.md 6.21): once the diff demo's repository exists,
// its running job (the checkout redesign) gets review comments through the real service, so they
// carry real hashes and quotes: two drafts (a range in the Go handler, a note on the whole
// stylesheet), one sent on the README, and one sent on the checkout doc that the "agent" then
// rewrites, so it shows as outdated with its original quote. Nothing here runs outside --demo.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jclement/crowsnest/internal/changes"
	"github.com/jclement/crowsnest/internal/jobs"
)

// demoCommentWait bounds how long the seeding waits for the demo's repository.
const demoCommentWait = 90 * time.Second

// seedDemoComments waits for the diff demo and leaves its comments; errors are logged.
func seedDemoComments(ctx context.Context, service *changes.Service, logger *slog.Logger) {
	deadline := time.Now().Add(demoCommentWait)
	var jobID, dir string
	for {
		var ok bool
		if jobID, dir, ok = jobs.DemoDiffReady(); ok {
			break
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	if err := leaveDemoComments(ctx, service, jobID, dir); err != nil {
		logger.Warn("seeding the diff demo's comments", "err", err)
	}
}

func leaveDemoComments(ctx context.Context, service *changes.Service, jobID, dir string) error {
	ask := changes.Ask{Kind: changes.KindJob, Ref: jobID}
	if list, err := service.Comments(ctx, ask); err == nil && len(list.Comments) > 0 {
		return nil // a reused demo database: already there
	}
	summary, err := service.Summary(ctx, ask, changes.ScopeAll, true)
	if err != nil {
		return err
	}
	hashes := map[string]string{}
	for _, file := range summary.Files {
		hashes[file.Path] = file.Hash
	}
	add := func(path, side string, start, end int, body string) (changes.Comment, error) {
		return service.AddComment(ctx, ask, changes.CommentAsk{Path: path, Scope: changes.ScopeAll, Side: side, LineStart: start, LineEnd: end, Body: body, Hash: hashes[path]})
	}
	handler := lineOf(filepath.Join(dir, "server/checkout.go"), "func Checkout(")
	if handler == 0 {
		return fmt.Errorf("the demo handler has no func Checkout")
	}
	if _, err := add("server/checkout.go", changes.SideNew, handler, handler+2, "Set an `Allow: POST` header on the 405, and have the test check for it."); err != nil {
		return err
	}
	if _, err := add("web/src/styles/cart.css", changes.SideFile, 0, 0, "Use the theme's spacing variables here rather than raw pixel values."); err != nil {
		return err
	}
	readme, err := add("README.md", changes.SideNew, 1, 1, "Mention the new one-page checkout in the README's first paragraph.")
	if err != nil {
		return err
	}
	doc, err := add("docs/checkout.md", changes.SideNew, 1, 3, "Say what happens when the payment fails.")
	if err != nil {
		return err
	}
	if err := service.MarkSent(ctx, summary.Subject, []int64{readme.ID, doc.ID}, time.Now().Add(-20*time.Minute).UTC()); err != nil {
		return err
	}
	// The agent answers the doc's comment: the file changes, and its comment is outdated.
	path := filepath.Join(dir, "docs/checkout.md")
	body, err := os.ReadFile(path) //nolint:gosec // the demo's own worktree
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(body, "\n## When the payment fails\n\nThe cart is kept and the page says why, with a Try again button.\n"...), 0o600) //nolint:gosec // the demo's own worktree
}

// lineOf is the 1-based line of the first line in path holding text (0 when none).
func lineOf(path, text string) int {
	body, err := os.ReadFile(path) //nolint:gosec // the demo's own worktree
	if err != nil {
		return 0
	}
	for i, line := range strings.Split(string(body), "\n") {
		if strings.Contains(line, text) {
			return i + 1
		}
	}
	return 0
}
