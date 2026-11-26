// Package taglines holds crowsnest's ~200 one-liners, shown in `crowsnest version` and on the
// login page. taglines.txt is the single source: Go embeds it here, and the web app imports the
// same file at build time, so the two can never drift.
package taglines

import (
	_ "embed"
	"math/rand/v2"
	"strings"
)

//go:embed taglines.txt
var raw string

// All returns every tagline, in file order.
func All() []string {
	var out []string
	for line := range strings.SplitSeq(raw, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// Random returns one tagline at random.
func Random() string {
	all := All()
	return all[rand.IntN(len(all))] //nolint:gosec // picking a joke, not a key
}
