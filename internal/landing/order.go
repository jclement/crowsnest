// The reading order of a change (the guided-diff standard Jeff's agents follow): never git's
// alphabetical order, but the way data flows, so a reviewer meets the shape of the data before
// the code that uses it:
//
//	schema / migration → server logic → API surface → client state → UI → tests → docs → config
//
// with churn (lockfiles, generated code, snapshots, screenshots) collapsed at the end. When the
// agent's walkthrough names files, its order wins for those files: it is the author's own account
// of how the change is best understood. Within a group, files keep their path order.

package landing

import (
	"path"
	"sort"
	"strings"
)

// groups in reading order.
var groups = []string{"schema", "server", "api", "client", "ui", "tests", "docs", "config", "churn"}

// Classify places a path in a reading-order group and says whether it is churn.
func Classify(file string) (group string, churn bool) {
	lower := strings.ToLower(file)
	base := path.Base(lower)
	ext := path.Ext(lower)
	switch {
	case isChurn(lower, base, ext):
		return "churn", true
	case strings.Contains(lower, "migration") || ext == ".sql" || strings.Contains(lower, "schema"):
		return "schema", false
	case isTest(lower, base):
		return "tests", false
	case ext == ".md" || ext == ".mdx" || ext == ".txt" || strings.HasPrefix(lower, "docs/"):
		return "docs", false
	case isConfig(lower, base, ext):
		return "config", false
	case ext == ".tsx" || ext == ".jsx" || ext == ".css" || ext == ".scss" || ext == ".html" || ext == ".vue" || ext == ".svelte" ||
		strings.Contains(lower, "/components/") || strings.Contains(lower, "/routes/") || strings.Contains(lower, "/views/"):
		return "ui", false
	case ext == ".ts" || ext == ".js" || ext == ".mjs":
		return "client", false
	case strings.Contains(lower, "httpd/") || strings.Contains(lower, "/api/") || strings.Contains(lower, "handler") ||
		strings.Contains(lower, "controller") || strings.Contains(lower, "cmd/"):
		return "api", false
	default:
		return "server", false
	}
}

func isChurn(lower, base, ext string) bool {
	switch base {
	case "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "go.sum", "cargo.lock", "poetry.lock", "uv.lock", "bun.lockb", "gemfile.lock":
		return true
	}
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".ico", ".svg", ".woff", ".woff2", ".ttf", ".snap":
		return true
	}
	return strings.Contains(lower, "generated") || strings.Contains(lower, "/dist/") || strings.HasSuffix(base, ".pb.go") ||
		strings.HasSuffix(base, ".min.js") || strings.Contains(lower, "__snapshots__") || strings.HasPrefix(lower, "vendor/")
}

func isTest(lower, base string) bool {
	return strings.HasSuffix(base, "_test.go") || strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") ||
		strings.Contains(lower, "/e2e/") || strings.HasPrefix(lower, "test/") || strings.HasPrefix(lower, "tests/") ||
		strings.Contains(lower, "/testdata/") || strings.Contains(lower, "/__tests__/")
}

func isConfig(lower, base, ext string) bool {
	switch base {
	case "mise.toml", "makefile", "dockerfile", "go.mod", "package.json", ".gitignore", ".env.sample", "tsconfig.json":
		return true
	}
	switch ext {
	case ".yml", ".yaml", ".toml", ".ini", ".cfg":
		return true
	}
	return strings.HasPrefix(lower, ".github/") || strings.HasPrefix(lower, "deploy/")
}

// Order sorts files for reading: files the walkthrough names first, in the order it names them,
// then the rest by group, then by path. It returns a new slice.
func Order(files []FileChange, walkthrough string) []FileChange {
	out := append([]FileChange(nil), files...)
	mention := map[string]int{}
	lower := walkthrough
	for _, file := range out {
		if index := strings.Index(lower, file.Path); index >= 0 && file.Path != "" && !file.Churn {
			mention[file.Path] = index
		}
	}
	rank := map[string]int{}
	for i, group := range groups {
		rank[group] = i
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		ai, aMentioned := mention[a.Path]
		bi, bMentioned := mention[b.Path]
		switch {
		case aMentioned && bMentioned:
			return ai < bi
		case aMentioned != bMentioned:
			return aMentioned
		case rank[a.Group] != rank[b.Group]:
			return rank[a.Group] < rank[b.Group]
		default:
			return a.Path < b.Path
		}
	})
	return out
}
