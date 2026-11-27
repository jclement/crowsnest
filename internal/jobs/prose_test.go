package jobs

import "testing"

// TestLooksLikeProseQuestion: precision first. Each row is a final message of the kind agents
// end turns with; true rows must be caught, and no false row may be.
func TestLooksLikeProseQuestion(t *testing.T) {
	cases := []struct {
		message string
		want    bool
	}{
		// Questions to the human, asked in prose.
		{"I've added the migration and the tests pass.\n\nWant me to start on the next milestone?", true},
		{"The build is green. Should I push the branch and open a PR?", true},
		{"There are two ways to do this:\n\n1. Keep the cache\n2. Drop it\n\nWhich would you prefer?", true},
		{"I found the bug in `parse.go`. Do you want me to fix it as part of this job, or file it separately?", true},
		{"Before I delete the old table, is that okay?", true},
		{"I can either rename the flag or keep an alias. Let me know which you'd like?", true},
		{"**Ready to merge.** Shall I go ahead?", true},
		{"Done with the refactor. Would you like me to also update the README?", true},
		// Not questions to the human.
		{"Why does the test fail? Because the fixture uses UTC. Fixed it and committed.", false},
		{"All tests pass. I've called done.", false},
		{"Investigating why `go test` hangs. Next I'll bisect the change.", false},
		{"I asked the human in q_12 whether to push; carrying on with the docs meanwhile. Should I wait?", false},
		{"```go\nif ok := check(); !ok { return }\n// should I return here?\n```\nThe guard is in place.", false},
		{"What could cause this? The lock is taken twice. I'll restructure it.", false},
		{"Is the cache stale? Checking the timestamps now.", false},
		{"", false},
		{"Running the suite again?", false},
	}
	for _, tc := range cases {
		if got := LooksLikeProseQuestion(tc.message); got != tc.want {
			t.Errorf("%q: got %v, want %v", tc.message, got, tc.want)
		}
	}
}
