// --demo for the diff view (DESIGN.md 6.21): the Website demo project's folder becomes a real,
// throwaway git repository (a small storefront in Go, TypeScript, CSS and Markdown) with edits
// its lead has not committed, and two worktree jobs filed under it:
//
//   - one running, its branch carrying a commit (a Go handler, a TypeScript module renamed, a
//     stylesheet, the README, a deleted Go file, a changed PNG and a large generated file) and
//     uncommitted work on top, one file of which keeps changing while you watch (DemoDiffTouch,
//     every minute and on demand), so a viewed mark is seen to clear by itself;
//   - one done and approved, ready to land, so the Review tab shows the viewer and Land's
//     "files not viewed" warning.
//
// Nothing here touches a real repository: everything is under the demo's state directory.

package jobs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/store"
)

// demoDiff remembers the running demo job's worktree for DemoDiffTouch.
var demoDiff struct {
	mu      sync.Mutex
	jobID   string
	dir     string
	touches int
}

const demoCartPath = "web/src/cart.ts"

// SeedDemoDiffs seeds the diff demo into repo (the Website project's folder, projectID its id).
// It runs after SeedDemoLanding, which switched the fake herdr to real git worktrees.
func SeedDemoDiffs(ctx context.Context, s *Service, repo string, projectID int64) error {
	if !gitInstalled() || repo == "" {
		return nil
	}
	if err := os.MkdirAll(repo, 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err == nil {
		return nil // a demo database reused: already seeded
	}
	if err := demoGit(repo, "init", "-q", "-b", "main"); err != nil {
		return err
	}
	for _, setting := range [][2]string{{"user.name", "Demo"}, {"user.email", "demo@example.invalid"}, {"commit.gpgsign", "false"}} {
		if err := demoGit(repo, "config", setting[0], setting[1]); err != nil {
			return err
		}
	}
	for name, body := range storefrontBase() {
		if err := demoWrite(repo, name, body); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Join(repo, "web", "public"), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(repo, "web/public/logo.png"), demoPNG(0), 0o600); err != nil {
		return err
	}
	if err := demoGit(repo, "add", "."); err != nil {
		return err
	}
	if err := demoGit(repo, "commit", "-q", "-m", "Storefront: first version"); err != nil {
		return err
	}
	// The lead's own uncommitted edits in the main checkout.
	if err := demoWrite(repo, "README.md", strings.Replace(storefrontBase()["README.md"], "## Running", "## Deploying\n\nPush to main; the site rebuilds in a minute.\n\n## Running", 1)); err != nil {
		return err
	}
	if err := demoWrite(repo, "NOTES.md", "# Lead's notes\n\n- Checkout redesign is j_running; review the cart once it settles.\n"); err != nil {
		return err
	}
	running, dir, err := seedDiffJob(ctx, s, repo, projectID, "job/120-checkout-redesign", "Redesign the checkout", StateRunning, demoCheckoutWork)
	if err != nil {
		return err
	}
	demoDiff.mu.Lock()
	demoDiff.jobID, demoDiff.dir = FormatID(running), dir
	demoDiff.mu.Unlock()
	if _, _, err := seedDiffJob(ctx, s, repo, projectID, "job/121-price-rounding", "Round prices to the cent", StateDone, demoRoundingWork); err != nil {
		return err
	}
	go demoKeepChanging(ctx, s)
	return nil
}

// demoKeepChanging edits the running job's cart once a minute until ctx ends.
func demoKeepChanging(ctx context.Context, s *Service) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			demoDiff.mu.Lock()
			id := demoDiff.jobID
			demoDiff.mu.Unlock()
			_ = DemoDiffTouch(ctx, s, id)
		}
	}
}

// DemoDiffTouch makes the running demo job "edit" its cart now: one more line in web/src/cart.ts,
// recorded on its timeline as activity, so the diff view reads it again and a viewed mark on the
// file clears. Only the diff demo's running job can be touched.
func DemoDiffTouch(ctx context.Context, s *Service, id string) error {
	demoDiff.mu.Lock()
	defer demoDiff.mu.Unlock()
	if demoDiff.jobID == "" || id != demoDiff.jobID {
		return fmt.Errorf("only the diff demo's running job (%s) edits files on demand", firstNonEmptyDir(demoDiff.jobID, "none"))
	}
	demoDiff.touches++
	path := filepath.Join(demoDiff.dir, filepath.FromSlash(demoCartPath))
	body, err := os.ReadFile(path) //nolint:gosec // the demo's own worktree
	if err != nil {
		return err
	}
	line := fmt.Sprintf("\n// Edit %d: keep the cart badge in step with the server (touch %d).\nexport const cartRevision%d = %d;\n", demoDiff.touches, demoDiff.touches, demoDiff.touches, demoDiff.touches)
	if err := os.WriteFile(path, append(body, line...), 0o600); err != nil { //nolint:gosec // the demo's own worktree and a fixed file
		return err
	}
	return s.Journal(ctx, id, EventProgress, fmt.Sprintf("Edited %s (change %d)", demoCartPath, demoDiff.touches), nil)
}

// seedDiffJob makes a worktree job: committed work, then uncommitted work, then the record.
func seedDiffJob(ctx context.Context, s *Service, repo string, projectID int64, branch, title string, state State, work func(dir string) error) (int64, string, error) {
	path := filepath.Join(filepath.Dir(repo), ".worktrees", strings.ReplaceAll(branch, "/", "-"))
	created, err := s.opts.Herdr.WorktreeCreate(ctx, herdr.WorktreeCreateParams{Cwd: repo, Branch: branch, Path: path, Label: branch})
	if err != nil {
		return 0, "", fmt.Errorf("making a demo worktree: %w", err)
	}
	if err := work(path); err != nil {
		return 0, "", err
	}
	now := s.opts.Now()
	record := store.JobRecord{
		Title: title, BriefMD: title, Role: "builder", Dir: repo, WorktreeBranch: branch, WorkDir: path, Isolate: true, ProjectID: projectID,
		Engine: EngineClaude, State: string(state), PaneID: created.RootPane.ID, AgentName: "job-demo", BriefSent: true,
		CreatedAt: now.Add(-2 * time.Hour), StartedAt: now.Add(-2 * time.Hour), LastActivityAt: now.Add(-time.Minute),
		BaseBranch: "main", WorkspaceID: created.Workspace.ID, VerifiedJSON: "[]", NotVerifiedJSON: "[]", Reviewer: ReviewerOff,
	}
	if state == StateRunning {
		record.Progress = "Restyling the cart and wiring the new checkout handler"
	} else {
		record.SettledAt = now.Add(-10 * time.Minute)
		record.ResultMD = "Prices are rounded half-even to the cent before tax; a table test covers the edge cases."
		record.VerifiedJSON = `["go test ./... (rounding table test)"]`
		record.ReviewRound, record.ReviewVerdict, record.ReviewMD = 1, VerdictApprove, "VERDICT: approve\n\nSmall and tested."
		record.AcceptState, record.AcceptDetail = AcceptUnverified, "No check command for this project"
	}
	id, err := s.opts.Store.InsertJob(ctx, record)
	if err != nil {
		return 0, "", err
	}
	_, _ = s.opts.Store.InsertJobEvent(ctx, store.JobEventRecord{JobID: id, Kind: EventLaunch, Summary: "Opened a worktree on " + branch, At: now.Add(-2 * time.Hour)})
	return id, path, nil
}

func demoCheckoutWork(dir string) error {
	steps := []func() error{
		func() error { return demoWrite(dir, "server/checkout.go", checkoutGo) },
		func() error { return os.MkdirAll(filepath.Join(dir, "web", "src", "lib"), 0o700) },
		func() error { return demoGit(dir, "mv", "web/src/util.ts", "web/src/lib/format.ts") },
		func() error {
			return demoWrite(dir, "web/src/lib/format.ts", utilTS+"\nexport function percent(n: number): string {\n  return Math.round(n * 100) + \"%\";\n}\n")
		},
		func() error {
			return demoWrite(dir, "web/src/styles/cart.css", strings.Replace(cartCSS, "  padding: 8px;\n  border: 1px solid #ccc;", "  padding: 12px 16px;\n  border-radius: 8px;\n  border: 1px solid var(--border);", 1))
		},
		func() error { return demoGit(dir, "rm", "-q", "server/legacy_checkout.go") },
		func() error { return os.WriteFile(filepath.Join(dir, "web/public/logo.png"), demoPNG(1), 0o600) },
		func() error { return demoWrite(dir, "web/src/generated/api.ts", generatedAPI(1400)) },
		func() error {
			return demoWrite(dir, "README.md", strings.Replace(storefrontBase()["README.md"], "A tiny shop.", "A tiny shop with a one-page checkout.", 1))
		},
		func() error { return demoGit(dir, "add", "-A") },
		func() error {
			return demoGit(dir, "commit", "-q", "-m", "Checkout: one page, Intl money, rounded cart")
		},
		// Uncommitted on top: the cart (which keeps changing), the handler's test, a new doc.
		func() error {
			return demoWrite(dir, demoCartPath, strings.Replace(cartTS, "return items.length;", "return items.reduce((sum, item) => sum + item.quantity, 0);", 1))
		},
		func() error { return demoWrite(dir, "server/checkout_test.go", checkoutTestGo) },
		func() error {
			return demoWrite(dir, "docs/checkout.md", "# Checkout\n\nOne page: cart, address, payment.\n\n```ts\nmoney(1999) // \"$19.99\"\n```\n")
		},
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

func demoRoundingWork(dir string) error {
	if err := demoWrite(dir, "server/price.go", strings.Replace(priceGo, "return cents", "return int64(math.RoundToEven(float64(cents)))", 1)); err != nil {
		return err
	}
	if err := demoWrite(dir, "server/price_test.go", "package server\n\nimport \"testing\"\n\nfunc TestRound(t *testing.T) {\n\tfor in, want := range map[float64]int64{1.5: 2, 2.5: 2, 3.5: 4} {\n\t\tif got := Round(in); got != want {\n\t\t\tt.Errorf(\"Round(%v) = %d, want %d\", in, got, want)\n\t\t}\n\t}\n}\n"); err != nil {
		return err
	}
	if err := demoGit(dir, "add", "-A"); err != nil {
		return err
	}
	return demoGit(dir, "commit", "-q", "-m", "Round prices half-even to the cent")
}

// demoPNG is a tiny valid-looking PNG whose last byte varies (a binary change).
func demoPNG(variant byte) []byte {
	return []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0x0d, 'I', 'H', 'D', 'R', 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, variant}
}

func generatedAPI(endpoints int) string {
	var b strings.Builder
	b.WriteString("// Code generated by openapi-gen. DO NOT EDIT.\n\n")
	for i := range endpoints {
		fmt.Fprintf(&b, "export interface Endpoint%d {\n  path: \"/api/v%d/items/%d\";\n  method: \"GET\";\n}\n", i, 1+i%3, i)
	}
	return b.String()
}

func storefrontBase() map[string]string {
	return map[string]string{
		"README.md":                 "# Storefront\n\nA tiny shop.\n\n## Running\n\n```sh\ngo run ./server\n```\n",
		"server/checkout.go":        "package server\n\nimport \"net/http\"\n\n// Checkout takes the cart and answers with an order id.\nfunc Checkout(w http.ResponseWriter, r *http.Request) {\n\tw.WriteHeader(http.StatusNotImplemented)\n}\n",
		"server/legacy_checkout.go": "package server\n\n// legacyCheckout is the old three-step flow.\nfunc legacyCheckout() {}\n",
		"server/price.go":           priceGo,
		"web/src/util.ts":           utilTS,
		"web/src/cart.ts":           cartTS,
		"web/src/styles/cart.css":   cartCSS,
	}
}

const priceGo = "package server\n\nimport \"math\"\n\nvar _ = math.Pi\n\n// Round turns a price in fractional cents into whole cents.\nfunc Round(cents float64) int64 {\n\treturn int64(cents)\n}\n"

const utilTS = `// Formatting helpers for the storefront.

export function money(cents: number): string {
  return "$" + (cents / 100).toFixed(2);
}

export function plural(n: number, word: string): string {
  return n === 1 ? word : word + "s";
}
`

const cartTS = `import { money } from "./util";

export interface Item {
  sku: string;
  price: number; // cents
  quantity: number;
}

/* The cart badge shows how many things are in it. */
export function count(items: Item[]): number {
  return items.length;
}

export function total(items: Item[]): string {
  return money(items.reduce((sum, item) => sum + item.price * item.quantity, 0));
}
`

const cartCSS = `.cart {
  display: flex;
  gap: 4px;
  padding: 8px;
  border: 1px solid #ccc;
}

.cart__total {
  font-weight: 600;
}
`

const checkoutGo = `package server

import (
	"encoding/json"
	"net/http"
)

// order is what Checkout answers with.
type order struct {
	ID    string ` + "`json:\"id\"`" + `
	Total int64  ` + "`json:\"total\"`" + `
}

// Checkout takes the cart and answers with an order id. One page: the cart, the address and the
// payment arrive together, so there is no session to lose between steps.
func Checkout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(order{ID: "o_1", Total: Round(1999.5)})
}
`

const checkoutTestGo = `package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckoutRefusesGet(t *testing.T) {
	rec := httptest.NewRecorder()
	Checkout(rec, httptest.NewRequest(http.MethodGet, "/checkout", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("code = %d", rec.Code)
	}
}
`
