// Tests for the browser over a real temporary tree: roots and escapes (.., links out of a root),
// unreadable directories, the entry cap, hidden files and files, git facts, and a slow mount
// stood in by a fake FS whose ReadDir does not return.

package browse

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// tree is a temporary layout:
//
//	base/home/Developer/repo        a git repo (origin on GitHub, credentials in the URL)
//	base/home/Developer/repo/sub     a folder inside it
//	base/home/Developer/azure        a git repo on Azure DevOps, branch feature/x
//	base/home/Developer/wt           a linked worktree of repo (.git is a file)
//	base/home/Developer/plain        not a repo
//	base/home/.hidden, notes.txt     a hidden folder and a file
//	base/home/out -> base/outside    a link out of every root
//	base/home/in  -> Developer/repo  a link that stays inside
//	base/home/dangling -> nowhere
//	base/outside                     not a root
//	base/extra                       a --browse-root
type tree struct {
	base, home, outside, extra string
}

func newTree(t *testing.T) tree {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tr := tree{base: base, home: filepath.Join(base, "home"), outside: filepath.Join(base, "outside"), extra: filepath.Join(base, "extra")}
	for _, dir := range []string{
		"home/Developer/repo/sub", "home/Developer/repo/.git/refs/remotes/origin", "home/Developer/repo/.git/refs/heads",
		"home/Developer/azure/.git", "home/Developer/plain", "home/Developer/wt", "home/.hidden", "home/Work",
		"outside/secret", "extra/thing",
	} {
		mkdir(t, filepath.Join(base, dir))
	}
	write(t, filepath.Join(tr.home, "Developer/repo/.git/HEAD"), "ref: refs/heads/main\n")
	write(t, filepath.Join(tr.home, "Developer/repo/.git/refs/heads/main"), "abc\n")
	write(t, filepath.Join(tr.home, "Developer/repo/.git/refs/remotes/origin/HEAD"), "ref: refs/remotes/origin/main\n")
	write(t, filepath.Join(tr.home, "Developer/repo/.git/config"), "[core]\n\tbare = false\n[remote \"origin\"]\n\turl = https://jeff:ghp_secret@github.com/jclement/repo.git\n")
	write(t, filepath.Join(tr.home, "Developer/repo/AGENTS.md"), "# agents\n")
	write(t, filepath.Join(tr.home, "Developer/repo/mise.toml"), "[tasks.check]\nrun = 'true'\n")
	write(t, filepath.Join(tr.home, "Developer/azure/.git/HEAD"), "ref: refs/heads/feature/x\n")
	write(t, filepath.Join(tr.home, "Developer/azure/.git/packed-refs"), "# pack-refs\nabc refs/heads/master\n")
	write(t, filepath.Join(tr.home, "Developer/azure/.git/config"), "[remote \"upstream\"]\n\turl = git@ssh.dev.azure.com:v3/org/proj/repo\n")
	write(t, filepath.Join(tr.home, "Developer/azure/CLAUDE.md"), "# claude\n")
	mkdir(t, filepath.Join(tr.home, "Developer/repo/.git/worktrees/wt"))
	write(t, filepath.Join(tr.home, "Developer/repo/.git/worktrees/wt/HEAD"), "ref: refs/heads/wip/x\n")
	write(t, filepath.Join(tr.home, "Developer/repo/.git/worktrees/wt/commondir"), "../..\n")
	write(t, filepath.Join(tr.home, "Developer/wt/.git"), "gitdir: "+filepath.Join(tr.home, "Developer/repo/.git/worktrees/wt")+"\n")
	write(t, filepath.Join(tr.home, "notes.txt"), "not served")
	write(t, filepath.Join(tr.outside, "secret/file"), "secret")
	link(t, tr.outside, filepath.Join(tr.home, "out"))
	link(t, filepath.Join(tr.home, "Developer/repo"), filepath.Join(tr.home, "in"))
	link(t, filepath.Join(base, "nowhere"), filepath.Join(tr.home, "dangling"))
	return tr
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func link(t *testing.T, target, name string) {
	t.Helper()
	if err := os.Symlink(target, name); err != nil {
		t.Fatal(err)
	}
}

func (tr tree) service(opts Options) *Service {
	opts.Home = tr.home
	if opts.Extra == nil {
		opts.Extra = []string{tr.extra}
	}
	return New(opts)
}

func names(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, entry := range entries {
		out[i] = entry.Name
	}
	return out
}

func entryNamed(t *testing.T, listing Listing, name string) Entry {
	t.Helper()
	for _, entry := range listing.Entries {
		if entry.Name == name {
			return entry
		}
	}
	t.Fatalf("no entry %q in %v", name, names(listing.Entries))
	return Entry{}
}

// ---- Roots and escapes ----------------------------------------------------------------------

func TestListRefusesPathsOutsideTheRoots(t *testing.T) {
	tr := newTree(t)
	service := tr.service(Options{})
	ctx := context.Background()
	cases := map[string]struct {
		path string
		want error
	}{
		"outside":              {tr.outside, ErrOutside},
		"dot-dot out of home":  {tr.home + "/../outside", ErrOutside},
		"tilde dot-dot":        {"~/../outside/secret", ErrOutside},
		"link out of the root": {filepath.Join(tr.home, "out"), ErrOutside},
		"through a link":       {filepath.Join(tr.home, "out", "secret"), ErrOutside},
		"filesystem root":      {"/", ErrOutside},
		"relative":             {"Developer", ErrInvalid},
		"empty":                {"", ErrInvalid},
		"nul":                  {tr.home + "\x00", ErrInvalid},
		"missing in a root":    {filepath.Join(tr.home, "nope"), ErrNotFound},
		"missing outside":      {filepath.Join(tr.outside, "nope"), ErrOutside},
		"a file":               {filepath.Join(tr.home, "notes.txt"), ErrNotDir},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := service.List(ctx, tc.path, ListOptions{})
			if !errors.Is(err, tc.want) {
				t.Fatalf("List(%q) = %v, want %v", tc.path, err, tc.want)
			}
		})
	}
}

func TestListInsideTheRoots(t *testing.T) {
	tr := newTree(t)
	ctx := context.Background()
	service := tr.service(Options{Sources: func(context.Context) []Place {
		return []Place{{Path: tr.outside + "/secret", Kind: KindProject}, {Path: "/", Kind: KindRemembered}}
	}})
	for _, path := range []string{"~", "~/Developer", tr.home + "/Developer/../Developer/repo", filepath.Join(tr.home, "in"), tr.extra, tr.outside + "/secret"} {
		if _, err := service.List(ctx, path, ListOptions{}); err != nil {
			t.Errorf("List(%q) = %v, want it listed", path, err)
		}
	}
	// A project's directory is a root; its parent is not. "/" from a remembered list is ignored.
	if _, err := service.List(ctx, tr.outside, ListOptions{}); !errors.Is(err, ErrOutside) {
		t.Errorf("a project's parent: %v, want ErrOutside", err)
	}
	if _, err := service.List(ctx, "/", ListOptions{}); !errors.Is(err, ErrOutside) {
		t.Errorf("/ remembered: %v, want ErrOutside", err)
	}
	// Only an explicit --browse-root opens /.
	open := New(Options{Home: tr.home, Extra: []string{"/"}})
	if _, err := open.List(ctx, "/", ListOptions{}); err != nil {
		t.Errorf("/ as a --browse-root: %v", err)
	}
}

func TestListEntriesAndCrumbs(t *testing.T) {
	tr := newTree(t)
	listing, err := tr.service(Options{}).List(context.Background(), "~", ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if listing.Path != tr.home || listing.Label != "~" || listing.Root.Kind != KindHome {
		t.Fatalf("listing = %+v", listing)
	}
	// Directories only, sorted without case; the link in counts as a directory; hidden and
	// files counted, not listed; a link out and a dangling one are files or marked.
	if got := strings.Join(names(listing.Entries), ","); got != "Developer,in,out,Work" {
		t.Fatalf("entries = %s", got)
	}
	if listing.HiddenCount != 1 || listing.FileCount != 2 { // .hidden; notes.txt and dangling
		t.Fatalf("hidden %d files %d", listing.HiddenCount, listing.FileCount)
	}
	in := entryNamed(t, listing, "in")
	if !in.Symlink || !in.Dir || in.Outside || !in.Git || in.Target != filepath.Join(tr.home, "Developer/repo") {
		t.Fatalf("in = %+v", in)
	}
	out := entryNamed(t, listing, "out")
	if !out.Symlink || !out.Outside || out.Readable || out.Git || !out.Modified.IsZero() {
		t.Fatalf("out = %+v: a link out must be marked and not described", out)
	}
	if listing.Parent != "" {
		t.Fatalf("parent of home = %q, want none (above the root)", listing.Parent)
	}
	last := listing.Crumbs[len(listing.Crumbs)-1]
	if last.Name != "~" || !last.Browsable || listing.Crumbs[0].Path != "/" || listing.Crumbs[0].Browsable {
		t.Fatalf("crumbs = %+v", listing.Crumbs)
	}

	with, err := tr.service(Options{}).List(context.Background(), "~", ListOptions{Files: true, Hidden: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(names(with.Entries), ","); got != ".hidden,Developer,in,out,Work,dangling,notes.txt" {
		t.Fatalf("with files and hidden = %s", got)
	}
	if !entryNamed(t, with, ".hidden").Hidden || entryNamed(t, with, "dangling").Error == "" {
		t.Fatalf("hidden or dangling not marked: %+v", with.Entries)
	}
	dev, err := tr.service(Options{}).List(context.Background(), "~/Developer", ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if dev.Parent != tr.home || !entryNamed(t, dev, "repo").Git || entryNamed(t, dev, "plain").Git {
		t.Fatalf("Developer = %+v", dev)
	}
}

func TestListUnreadableDirectoryIsPerEntry(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every directory")
	}
	tr := newTree(t)
	locked := filepath.Join(tr.home, "locked")
	mkdir(t, filepath.Join(locked, "inner"))
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	service := tr.service(Options{})
	listing, err := service.List(context.Background(), "~", ListOptions{})
	if err != nil {
		t.Fatalf("an unreadable entry failed the whole list: %v", err)
	}
	entry := entryNamed(t, listing, "locked")
	if entry.Readable || entry.Error != "permission denied" {
		t.Fatalf("locked = %+v", entry)
	}
	if _, err := service.List(context.Background(), locked, ListOptions{}); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("listing the locked folder itself: %v, want ErrUnreadable", err)
	}
}

func TestListCapsEntries(t *testing.T) {
	tr := newTree(t)
	for i := range 7 {
		mkdir(t, filepath.Join(tr.extra, fmt.Sprintf("d%02d", i)))
	}
	listing, err := tr.service(Options{MaxEntries: 3}).List(context.Background(), tr.extra, ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(names(listing.Entries), ","); got != "d00,d01,d02" || listing.More != 5 {
		t.Fatalf("entries %s more %d, want d00,d01,d02 and 5 more", got, listing.More)
	}
}

// ---- Facts ----------------------------------------------------------------------------------

func TestFactsOfARepository(t *testing.T) {
	tr := newTree(t)
	service := tr.service(Options{
		CheckCommand: func(dir string) string {
			if _, err := os.Stat(filepath.Join(dir, "mise.toml")); err == nil {
				return "mise run check"
			}
			return ""
		},
		Trusted: func(dir string) (bool, bool) { return strings.HasSuffix(dir, "/repo"), true },
	})
	ctx := context.Background()
	repo, err := service.Facts(ctx, "~/Developer/repo")
	if err != nil {
		t.Fatal(err)
	}
	if !repo.Git || repo.Branch != "main" || repo.DefaultBranch != "main" || repo.Host != HostGitHub || repo.Worktree {
		t.Fatalf("repo facts = %+v", repo)
	}
	if strings.Contains(repo.Remote, "ghp_secret") || strings.Contains(repo.Remote, "jeff") || repo.Remote != "https://github.com/jclement/repo.git" {
		t.Fatalf("remote %q: credentials must be removed", repo.Remote)
	}
	if !repo.AgentsMD || repo.ClaudeMD || repo.CheckCommand != "mise run check" || repo.ClaudeTrusted == nil || !*repo.ClaudeTrusted || repo.Name != "repo" {
		t.Fatalf("repo facts = %+v", repo)
	}

	azure, err := service.Facts(ctx, "~/Developer/azure")
	if err != nil {
		t.Fatal(err)
	}
	if azure.Host != HostAzure || azure.Branch != "feature/x" || azure.DefaultBranch != "master" || !azure.ClaudeMD || azure.CheckCommand != "" {
		t.Fatalf("azure facts = %+v", azure)
	}

	wt, err := service.Facts(ctx, "~/Developer/wt")
	if err != nil {
		t.Fatal(err)
	}
	if !wt.Git || !wt.Worktree || wt.Branch != "wip/x" || wt.DefaultBranch != "main" || wt.Host != HostGitHub {
		t.Fatalf("worktree facts = %+v", wt)
	}

	sub, err := service.Facts(ctx, "~/Developer/repo/sub")
	if err != nil {
		t.Fatal(err)
	}
	if sub.Git || sub.GitTop != filepath.Join(tr.home, "Developer/repo") {
		t.Fatalf("sub facts = %+v, want inside repo", sub)
	}

	home, err := service.Facts(ctx, "~")
	if err != nil {
		t.Fatal(err)
	}
	if !home.IsHome || home.Git || home.GitTop != "" {
		t.Fatalf("home facts = %+v", home)
	}
	if _, err := service.Facts(ctx, tr.outside); !errors.Is(err, ErrOutside) {
		t.Fatalf("facts outside: %v", err)
	}
}

func TestHostOf(t *testing.T) {
	cases := map[string]string{
		"":                                       "",
		"git@github.com:jclement/crowsnest.git":  HostGitHub,
		"https://github.com/a/b":                 HostGitHub,
		"https://org@dev.azure.com/org/p/_git/r": HostAzure,
		"https://org.visualstudio.com/p/_git/r":  HostAzure,
		"git@ssh.dev.azure.com:v3/org/p/r":       HostAzure,
		"https://gitlab.com/a/b":                 HostGitLab,
		"git@bitbucket.org:a/b.git":              HostBitbucket,
		"ssh://git@git.example.com/a.git":        HostOther,
		"/srv/git/a.git":                         HostOther,
	}
	for remote, want := range cases {
		if got := HostOf(remote); got != want {
			t.Errorf("HostOf(%q) = %q, want %q", remote, got, want)
		}
	}
}

func TestFavourites(t *testing.T) {
	tr := newTree(t)
	service := tr.service(Options{Sources: func(context.Context) []Place {
		return []Place{
			{Path: filepath.Join(tr.home, "Developer/repo"), Kind: KindProject},
			{Path: filepath.Join(tr.home, "Developer"), Kind: KindProjectParent},
			{Path: tr.outside, Kind: KindRecent},                         // not under a root: dropped
			{Path: filepath.Join(tr.home, "gone"), Kind: KindRemembered}, // missing: dropped
		}
	}})
	places, err := service.Favourites(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, place := range places {
		got = append(got, place.Kind+":"+service.label(place.Path))
	}
	want := "home:~,favourite:~/Developer,favourite:~/Work," + "root:" + tr.extra + ",project:~/Developer/repo"
	if strings.Join(got, ",") != want {
		t.Fatalf("favourites = %s\nwant %s", strings.Join(got, ","), want)
	}
	if !places[4].Git {
		t.Fatalf("the repo favourite is not marked git: %+v", places[4])
	}
}

// ---- Bounds: a slow mount -------------------------------------------------------------------

// slowFS is the real filesystem except that ReadDir of one directory blocks until released.
type slowFS struct {
	OS
	slow    string
	release chan struct{}
	once    sync.Once
}

func (f *slowFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name == f.slow {
		<-f.release
	}
	return f.OS.ReadDir(name)
}

func (f *slowFS) done() { f.once.Do(func() { close(f.release) }) }

func TestListTimesOutOnASlowMountAndBoundsInFlight(t *testing.T) {
	tr := newTree(t)
	slow := &slowFS{slow: tr.extra, release: make(chan struct{})}
	t.Cleanup(slow.done)
	service := tr.service(Options{FS: slow, Timeout: 50 * time.Millisecond, MaxInFlight: 1})
	service.busyWait = 20 * time.Millisecond
	ctx := context.Background()

	started := time.Now()
	if _, err := service.List(ctx, tr.extra, ListOptions{}); !errors.Is(err, ErrTimeout) {
		t.Fatalf("slow listing: %v, want ErrTimeout", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("the timeout took %v", elapsed)
	}
	// The stuck read still holds the only slot: the next call is busy, not stacked up.
	if _, err := service.List(ctx, "~", ListOptions{}); !errors.Is(err, ErrBusy) {
		t.Fatalf("while a read is stuck: %v, want ErrBusy", err)
	}
	slow.done()
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, err := service.List(ctx, "~", ListOptions{})
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the slot was never given back: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
