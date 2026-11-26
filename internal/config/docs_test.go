package config

import (
	"os"
	"strings"
	"testing"
)

// TestREADMEDocumentsEveryOption keeps the README's config table and .env.sample honest: a new
// option without its documentation fails the build.
func TestREADMEDocumentsEveryOption(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	sample, err := os.ReadFile("../../.env.sample")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range FlagTable() {
		if !strings.Contains(string(readme), "`"+row[0]+"`") {
			t.Errorf("README.md does not document %s", row[0])
		}
		for env := range strings.SplitSeq(row[1], ", ") {
			if !strings.Contains(string(readme), "`"+env+"`") && !strings.Contains(string(readme), env+"=") {
				t.Errorf("README.md does not mention %s", env)
			}
			if !strings.Contains(string(sample), env+"=") {
				t.Errorf(".env.sample does not list %s", env)
			}
		}
	}
}
