// Tests for the R7 browse fixes: every filesystem touch of the places route is bounded (R7-C10);
// metadata reads do not block on a FIFO or follow a link out (R7-S10); remembered roots are not
// widened by a link swapped in later, and nothing above home becomes a root (R7-S11); and the
// disclosure details (R7-S12).

package browse

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// stuckEvalFS is a mount that hangs on resolving one path.
type stuckEvalFS struct {
	OS
	stuck   string
	release chan struct{}
}

func (f *stuckEvalFS) EvalSymlinks(name string) (string, error) {
	if name == f.stuck {
		<-f.release
	}
	return f.OS.EvalSymlinks(name)
}

// TestPlacesIsBounded is R7-C10: favourites and roots are read in one bounded call, so a source
// that hangs while the roots are resolved answers "took too long" and holds a slot, rather than
// stranding the handler outside the deadline.
func TestPlacesIsBounded(t *testing.T) {
	tr := newTree(t)
	stuck := filepath.Join(tr.base, "mount")
	mkdir(t, stuck)
	fake := &stuckEvalFS{stuck: stuck, release: make(chan struct{})}
	t.Cleanup(func() { close(fake.release) })
	service := tr.service(Options{FS: fake, Timeout: 50 * time.Millisecond, MaxInFlight: 1,
		Sources: func(context.Context) []Place { return []Place{{Path: stuck, Kind: KindProject}} }})
	service.busyWait = 20 * time.Millisecond
	started := time.Now()
	if _, err := service.Places(context.Background()); !errors.Is(err, ErrTimeout) {
		t.Fatalf("places over a stuck mount: %v, want ErrTimeout", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("it took %v", elapsed)
	}
	if _, err := service.Roots(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("roots while the stuck read holds the only slot: %v, want ErrBusy", err)
	}
}

// TestMetadataReadsDoNotBlockOrFollowLinks is R7-S10: a FIFO planted as .git/HEAD does not hang
// the facts call (and so does not keep a slot), and a .git/HEAD that links to a file outside is
// not read.
func TestMetadataReadsDoNotBlockOrFollowLinks(t *testing.T) {
	tr := newTree(t)
	head := filepath.Join(tr.home, "Developer/repo/.git/HEAD")
	if err := os.Remove(head); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(head, 0o600); err != nil {
		t.Fatal(err)
	}
	service := tr.service(Options{Timeout: 2 * time.Second, MaxInFlight: 1})
	facts, err := service.Facts(context.Background(), filepath.Join(tr.home, "Developer/repo"))
	if err != nil {
		t.Fatalf("facts with a FIFO as HEAD: %v", err)
	}
	if facts.Branch != "" || !facts.Git {
		t.Fatalf("facts = %+v", facts)
	}

	// HEAD linked to a file outside every root, whose first line looks like a ref.
	write(t, filepath.Join(tr.outside, "planted"), "ref: refs/heads/stolen-line\nsecond line\n")
	if err := os.Remove(head); err != nil {
		t.Fatal(err)
	}
	link(t, filepath.Join(tr.outside, "planted"), head)
	facts, err = service.Facts(context.Background(), filepath.Join(tr.home, "Developer/repo"))
	if err != nil {
		t.Fatal(err)
	}
	if facts.Branch != "" {
		t.Fatalf("a linked HEAD was read: branch %q", facts.Branch)
	}
}

// TestRootsAreNotWidenedLater is R7-S11: a remembered directory replaced by a link to somewhere
// else is no root any more, and a directory above home is never one.
func TestRootsAreNotWidenedLater(t *testing.T) {
	tr := newTree(t)
	remembered := filepath.Join(tr.base, "work-p")
	mkdir(t, remembered)
	sources := []Place{{Path: remembered, Kind: KindRemembered}, {Path: tr.base, Kind: KindRemembered}}
	service := tr.service(Options{Sources: func(context.Context) []Place { return sources }})
	ctx := context.Background()
	if _, err := service.List(ctx, remembered, ListOptions{}); err != nil {
		t.Fatalf("the remembered directory: %v", err)
	}
	if _, err := service.List(ctx, tr.outside, ListOptions{}); !errors.Is(err, ErrOutside) {
		t.Fatalf("a directory above home made %s browsable: %v", tr.outside, err)
	}
	// The agent replaces the remembered directory with a link to the outside tree.
	if err := os.Remove(remembered); err != nil {
		t.Fatal(err)
	}
	link(t, tr.outside, remembered)
	if _, err := service.List(ctx, filepath.Join(tr.outside, "secret"), ListOptions{}); !errors.Is(err, ErrOutside) {
		t.Fatalf("the swapped link widened the roots: %v", err)
	}
}

// R9-S11: a project's folder is a root only while it resolves where it did when the project was
// made; swapped for a link to another tree, it widens nothing.
func TestProjectRootsAreNotWidenedLater(t *testing.T) {
	tr := newTree(t)
	project := filepath.Join(tr.home, "proj")
	mkdir(t, project)
	resolved, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	sources := []Place{{Path: project, Kind: KindProject, Resolved: resolved}}
	service := tr.service(Options{Sources: func(context.Context) []Place { return sources }})
	ctx := context.Background()
	if err := os.Remove(project); err != nil {
		t.Fatal(err)
	}
	link(t, tr.outside, project)
	if _, err := service.List(ctx, filepath.Join(tr.outside, "secret"), ListOptions{}); !errors.Is(err, ErrOutside) {
		t.Fatalf("the project's swapped link widened the roots: %v", err)
	}
}

// TestDisclosureDetails is R7-S12: a link out of the roots says neither where it points nor
// whether that is a folder; a path under a file answers like a missing one; and a remote's query
// and an scp-like password are not shown.
func TestDisclosureDetails(t *testing.T) {
	tr := newTree(t)
	service := tr.service(Options{})
	ctx := context.Background()
	listing, err := service.List(ctx, tr.home, ListOptions{Hidden: true, Files: true})
	if err != nil {
		t.Fatal(err)
	}
	out := entryNamed(t, listing, "out")
	if !out.Outside || out.Target != "" || out.Dir {
		t.Fatalf("a link out of the roots: %+v", out)
	}
	write(t, filepath.Join(tr.outside, "afile"), "x")
	if _, err := service.List(ctx, filepath.Join(tr.outside, "afile", "x"), ListOptions{}); !errors.Is(err, ErrOutside) {
		t.Fatalf("a path under an outside file: %v, want ErrOutside like a missing one", err)
	}
	for remote, want := range map[string]string{
		"https://github.com/o/r.git?token=abc#frag": "https://github.com/o/r.git",
		"jeff:ghp_tok@github.com:o/r.git":           "jeff@github.com:o/r.git",
		"git@github.com:o/r.git":                    "git@github.com:o/r.git",
	} {
		if got := cleanRemote(remote); got != want {
			t.Errorf("cleanRemote(%q) = %q, want %q", remote, got, want)
		}
	}
}
