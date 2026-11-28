// Tests for the R9 fixes to comment sends: a send claims its notes at a version before anything is
// delivered (R9-C5), and a note is outdated by a change to the side it was written on (R9-C11).

package changes

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/landing"
	"github.com/jclement/crowsnest/internal/store"
)

// TestTwoSendsNeverCarryTheSameNote (R9-C5): sends racing for the same drafts: one claims them,
// the others are refused, and nothing is marked sent until the winner settles.
func TestTwoSendsNeverCarryTheSameNote(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.commentOn("app.go", 3, 3, "rename A")
	f.commentOn("b.go", 3, 3, "fine")
	var wg sync.WaitGroup
	var mu sync.Mutex
	claimed := []Outgoing{}
	for i := range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := f.svc.Claim(ctx, job1, "", "send:"+string(rune('a'+i)))
			if err == nil {
				mu.Lock()
				claimed = append(claimed, out)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(claimed) != 1 || len(claimed[0].IDs) != 2 {
		t.Fatalf("%d sends claimed the notes, want exactly 1 with both: %+v", len(claimed), claimed)
	}
	list, _ := f.svc.Comments(ctx, job1)
	if list.Waiting != 2 || list.Sent != 0 || list.Drafts != 0 {
		t.Fatalf("while the send holds them: %+v", list)
	}
	if err := f.svc.Settle(ctx, claimed[0].Claim, SendDelivered); err != nil {
		t.Fatal(err)
	}
	if list, _ = f.svc.Comments(ctx, job1); list.Sent != 2 || list.Waiting != 0 {
		t.Fatalf("after delivery: %+v", list)
	}
}

// TestAnEditDuringASendStaysDue (R9-C5): a note edited after the send composed it is not marked
// sent with text it did not carry; an edit between reading and claiming refuses the claim.
func TestAnEditDuringASendStaysDue(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	note := f.commentOn("app.go", 3, 3, "text A")
	out, err := f.svc.Claim(ctx, job1, "", "send:one")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.EditComment(ctx, job1, note.ID, "text B"); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Settle(ctx, out.Claim, SendDelivered); err != nil {
		t.Fatal(err)
	}
	list, _ := f.svc.Comments(ctx, job1)
	if len(list.Comments) != 1 || !list.Comments[0].Pending || list.Comments[0].Body != "text B" || list.Comments[0].Status != StatusDraft {
		t.Fatalf("the edit made during the send: %+v", list.Comments)
	}
	// Composed at one version, edited before the claim: the claim is refused.
	read, err := f.svc.Outgoing(ctx, job1, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.EditComment(ctx, job1, note.ID, "text C"); err != nil {
		t.Fatal(err)
	}
	if err := f.st.ClaimDiffComments(ctx, "j_1", "send:two", read.notes, f.now); !errors.Is(err, store.ErrClaimConflict) {
		t.Fatalf("a claim of a version that changed: %v", err)
	}
}

// TestALostSendIsHandedBackUncertain (R9-C5): a send its request never settled (crowsnest stopped
// after delivering, or mid-send) is not left holding the notes: the sweep hands them back as due,
// marked uncertain; one a job's queued batch still holds is left alone.
func TestALostSendIsHandedBackUncertain(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.commentOn("app.go", 3, 3, "rename A")
	out, err := f.svc.Claim(ctx, job1, "", "send:lost")
	if err != nil {
		t.Fatal(err)
	}
	held := true
	live := func(context.Context, string, string) (bool, error) { return held, nil }
	f.now = f.now.Add(claimStale + time.Minute)
	if n, err := f.svc.SweepClaims(ctx, live, false); err != nil || n != 0 {
		t.Fatalf("a claim a queued batch holds was swept: %d %v", n, err)
	}
	held = false
	if n, err := f.svc.SweepClaims(ctx, live, false); err != nil || n != 1 {
		t.Fatalf("sweep = %d %v", n, err)
	}
	list, _ := f.svc.Comments(ctx, job1)
	if len(list.Comments) != 1 || !list.Comments[0].Pending || !list.Comments[0].Uncertain || list.Comments[0].Waiting {
		t.Fatalf("after the sweep: %+v", list.Comments)
	}
	if err := f.svc.Settle(ctx, out.Claim, SendDelivered); err != nil {
		t.Fatal(err)
	}
	if list, _ = f.svc.Comments(ctx, job1); list.Sent != 0 {
		t.Fatalf("a settled-late claim marked notes sent: %+v", list)
	}
}

// TestOutdatedFollowsTheCommentedSide (R9-C11): an old-side note is outdated when the old side's
// content changes even though the new side's did not; a new-side note is not.
func TestOutdatedFollowsTheCommentedSide(t *testing.T) {
	target := Target{Subject: "j_1", Kind: KindJob}
	rows := []store.DiffComment{
		{ID: 1, Path: "app.go", Side: SideOld, OldBlob: "old1", NewBlob: "new1"},
		{ID: 2, Path: "app.go", Side: SideNew, OldBlob: "old1", NewBlob: "new1"},
		{ID: 3, Path: "app.go", Side: SideFile, OldBlob: "old1", NewBlob: "new1"},
		{ID: 4, Path: "gone.go", Side: SideOld, OldBlob: "del1", NewBlob: ""},
	}
	for i := range rows {
		rows[i].Version = 1
	}
	status := func(files ...landing.DiffFile) map[int64]string {
		list := listComments(target, &reading{scopes: map[string][]landing.DiffFile{ScopeAll: files}}, rows)
		out := map[int64]string{}
		for _, comment := range list.Comments {
			out[comment.ID] = comment.Status
		}
		return out
	}
	same := status(landing.DiffFile{Path: "app.go", OldBlob: "old1", NewBlob: "new1"}, landing.DiffFile{Path: "gone.go", OldBlob: "del1"})
	for id, want := range map[int64]string{1: StatusDraft, 2: StatusDraft, 3: StatusDraft, 4: StatusDraft} {
		if same[id] != want {
			t.Errorf("unchanged: note %d is %s, want %s", id, same[id], want)
		}
	}
	// The base moved: the old side is another blob, the work-tree content is the same. A deleted
	// file whose old content changed outdates its deletion note too.
	moved := status(landing.DiffFile{Path: "app.go", OldBlob: "old2", NewBlob: "new1"}, landing.DiffFile{Path: "gone.go", OldBlob: "del2"})
	for id, want := range map[int64]string{1: StatusOutdated, 2: StatusDraft, 3: StatusOutdated, 4: StatusOutdated} {
		if moved[id] != want {
			t.Errorf("base moved: note %d is %s, want %s", id, moved[id], want)
		}
	}
}
