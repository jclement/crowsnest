// Tests for the demo's question rotation.

package questions

import (
	"context"
	"testing"
)

// R10: the demo asked the same scripted questions again and again (four open copies of one after
// 25 minutes). Each scripted question is asked only while it has no open copy.
func TestDemoRotationSkipsOpenQuestions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	rotation := []demoSeed{{input: confirm("First?")}, {input: confirm("Second?")}}
	next := 0
	asked := 0
	for range 5 {
		seed, ok := nextUnasked(ctx, f.service, rotation, &next)
		if !ok {
			continue
		}
		f.ask(t, seed.input)
		asked++
	}
	if asked != 2 {
		t.Fatalf("asked %d questions, want each of the two once", asked)
	}
	open, err := f.service.List(ctx, string(StatusOpen))
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 2 {
		t.Fatalf("%d open questions, want 2", len(open))
	}
	// Answered, a question may be asked again.
	if _, err := f.service.Answer(ctx, open[0].ID, AnswerInput{Value: []byte(`"yes"`)}); err != nil {
		t.Fatal(err)
	}
	if seed, ok := nextUnasked(ctx, f.service, rotation, &next); !ok || seed.input.Title != open[0].Title {
		t.Fatalf("after an answer: %v %q, want %q again", ok, seed.input.Title, open[0].Title)
	}
}
