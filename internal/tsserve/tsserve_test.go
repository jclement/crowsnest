// Tests against the fake tailscale (testdata/fake-tailscale), never the real one: the plan and
// the offer, port choice and never-clobber, refusals, the supervised child (published, stopped,
// gone after crowsnest stops and after it is SIGKILLed), restart with backoff, each failure
// message, leftovers, and the argv allowlist. Every test's argv log is checked for funnel and
// --bg at cleanup.
//
// The watchdog is this test binary itself: TestMain runs RunChild when TSSERVE_HELPER=child, and
// a whole publishing "crowsnest" when TSSERVE_HELPER=parent (for the SIGKILL test).

package tsserve

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/store"
)

func TestMain(m *testing.M) {
	switch os.Getenv("TSSERVE_HELPER") {
	case "child":
		os.Exit(RunChild(argsAfterDashes(os.Args), os.Stdin, os.Stdout, os.Stderr))
	case "parent":
		runParentHelper()
		return
	}
	os.Exit(m.Run())
}

func argsAfterDashes(args []string) []string {
	for i, arg := range args {
		if arg == "--" {
			return args[i+1:]
		}
	}
	return nil
}

// runParentHelper is a minimal crowsnest: publish, print the URL, then wait to be killed.
func runParentHelper() {
	service := New(helperOptions(os.Getenv("TSSERVE_FAKE"), os.Getenv("TSSERVE_TARGET")), true)
	service.opts.OnPublished = func(url string) { _, _ = os.Stdout.WriteString("PUBLISHED " + url + "\n") }
	service.Run(context.Background())
}

func helperOptions(binary, target string) Options {
	return Options{
		Binary:      binary,
		Self:        os.Args[0],
		SelfArgs:    []string{"--"},
		SelfEnv:     []string{"TSSERVE_HELPER=child"},
		Target:      func() (string, error) { return target, nil },
		Auth:        func() string { return "" },
		BackoffMin:  100 * time.Millisecond,
		BackoffMax:  400 * time.Millisecond,
		ConfirmPoll: 50 * time.Millisecond,
		StopGrace:   5 * time.Second,
	}
}

// ---- The fake -----------------------------------------------------------------------

type fake struct {
	t   *testing.T
	dir string
	bin string
}

func newFake(t *testing.T) *fake {
	t.Helper()
	bin, err := filepath.Abs("testdata/fake-tailscale")
	if err != nil {
		t.Fatal(err)
	}
	f := &fake{t: t, dir: t.TempDir(), bin: bin}
	t.Setenv("FAKE_TS_DIR", f.dir)
	t.Cleanup(func() {
		for _, line := range f.argv() {
			if strings.Contains(line, "funnel") || strings.Contains(line, "--bg") || strings.Contains(line, "reset") || strings.HasSuffix(line, " off") {
				t.Errorf("a forbidden tailscale command ran: %q", line)
			}
		}
	})
	return f
}

func (f *fake) write(name, content string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fake) argv() []string {
	raw, _ := os.ReadFile(filepath.Join(f.dir, "argv.log"))
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

// fgPID is the running foreground serve's pid (0 when none has started).
func (f *fake) fgPID() int {
	raw, _ := os.ReadFile(filepath.Join(f.dir, "fg.pid"))
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	return pid
}

func alive(pid int) bool { return pid > 0 && syscall.Kill(pid, 0) == nil }

const target = "http://127.0.0.1:2769"

type memKV struct {
	mu   sync.Mutex
	rows map[string]string
}

func (k *memKV) GetKV(_ context.Context, key string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	value, ok := k.rows[key]
	if !ok {
		return "", store.ErrNotFound
	}
	return value, nil
}

func (k *memKV) SetKV(_ context.Context, key, value string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.rows == nil {
		k.rows = map[string]string{}
	}
	k.rows[key] = value
	return nil
}

func (k *memKV) DeleteKV(_ context.Context, key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.rows, key)
	return nil
}

func newService(f *fake, enabled bool, change func(*Options)) (*Service, *memKV) {
	kv := &memKV{}
	opts := helperOptions(f.bin, target)
	opts.KV = kv
	if change != nil {
		change(&opts)
	}
	return New(opts, enabled), kv
}

// running starts Run and returns a stop that cancels it and waits for it to return.
func running(t *testing.T, service *Service) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		service.Run(ctx)
		close(done)
	}()
	stop := func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Fatal("Run did not return")
		}
	}
	t.Cleanup(stop)
	return stop
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func waitState(t *testing.T, service *Service, state State) View {
	t.Helper()
	var view View
	waitFor(t, "state "+string(state), func() bool {
		view = service.View(context.Background())
		return view.State == state
	})
	return view
}

// ---- The allowlist ----------------------------------------------------------------

func TestCheckArgs(t *testing.T) {
	allowed := [][]string{
		{"version"}, {"status", "--json"}, {"serve", "status", "--json"},
		{"serve", "--https=443", "http://127.0.0.1:2769"}, {"serve", "--https=8443", "http://[::1]:80"},
	}
	for _, args := range allowed {
		if err := CheckArgs(args); err != nil {
			t.Errorf("%q refused: %v", args, err)
		}
	}
	refused := [][]string{
		{"funnel", "443"}, {"serve", "--https=443", "--funnel", "http://127.0.0.1:2769"}, {"FUNNEL"},
		{"serve", "--bg", "2769"}, {"serve", "--bg", "--https=443", "http://127.0.0.1:2769"},
		{"serve", "reset"}, {"serve", "--https=443", "off"}, {"up"}, {"down"}, {"set", "--operator=me"},
		{"serve", "--https=443", "http://100.64.0.1:2769"}, {"serve", "--https=443", "http://127.0.0.1:2769/x"},
		{"serve", "--https=0", "http://127.0.0.1:2769"}, {"serve", "--https=70000", "http://127.0.0.1:2769"},
		{"serve", "--http=80", "http://127.0.0.1:2769"}, {"serve", "--tcp=22", "tcp://127.0.0.1:22"}, {},
	}
	for _, args := range refused {
		if err := CheckArgs(args); err == nil {
			t.Errorf("%q allowed", args)
		}
	}
	if err := CheckArgs([]string{"funnel", "--bg", "443"}); !errors.Is(err, ErrFunnel) {
		t.Errorf("funnel: %v", err)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("ServeArgs built a command outside the allowlist")
			}
		}()
		ServeArgs(443, "http://example.com")
	}()
}

func TestRunChildRefusesOutsideTheAllowlist(t *testing.T) {
	var stderr strings.Builder
	for _, args := range [][]string{{"/bin/true", "funnel", "443"}, {"/bin/true", "serve", "--bg", "2769"}, {"/bin/true", "status"}} {
		if code := RunChild(args, strings.NewReader(""), &strings.Builder{}, &stderr); code != 2 {
			t.Errorf("%q: exit %d", args, code)
		}
	}
}

func TestVersion(t *testing.T) {
	minimum, _ := parseVersion(MinVersion)
	for text, ok := range map[string]bool{"1.104.5\n  tailscale commit: x": true, "1.52.0": true, "1.52": true, "1.104.5-t788-gd92": true, "1.50.1": false, "0.99.0": false} {
		parsed, valid := parseVersion(text)
		if !valid || parsed.atLeast(minimum) != ok {
			t.Errorf("%q: %v %v", text, parsed, valid)
		}
	}
	if _, valid := parseVersion("garbage"); valid {
		t.Error("garbage parsed")
	}
}

func TestClassify(t *testing.T) {
	cases := map[string]string{
		"Serve is not enabled on your tailnet.\nTo enable, visit:\n\n  https://login.tailscale.com/f/serve?node=n1\n": "Serve is not turned on",
		"error: HTTPS certificates are not enabled":                 "HTTPS certificates are off",
		"MagicDNS is required":                                      "MagicDNS is off",
		"failed to connect to local Tailscale daemon; not running?": "not running",
		"backend state NeedsLogin":                                  "logged out",
		"Access denied: serve config denied":                        "refused to let this user",
		"flag provided but not defined: -https":                     "older than",
		"error: listener already exists for port 443":               "took the port",
	}
	for output, want := range cases {
		problem, ok := classify(output)
		if !ok || !strings.Contains(problem.Reason, want) {
			t.Errorf("%q: %+v", output, problem)
		}
	}
	if problem, _ := classify("Serve is not enabled on your tailnet. https://login.tailscale.com/f/serve?node=n1 https://evil.example/x"); problem.FixURL != "https://login.tailscale.com/f/serve?node=n1" || !problem.Waiting {
		t.Errorf("link: %+v", problem)
	}
	if _, ok := classify("something else entirely"); ok {
		t.Error("an unknown message matched")
	}
}

// ---- The plan and the offer ---------------------------------------------------------

func TestOfferShownAndHidden(t *testing.T) {
	f := newFake(t)
	service, _ := newService(f, false, nil)
	view := service.View(context.Background())
	if !view.Show || view.State != StateOff || view.URL != "https://quark.tail1234.ts.net" || view.Port != 443 || view.Target != target || view.Version != "1.104.5" {
		t.Fatalf("offer: %+v", view)
	}

	notInstalled, _ := newService(f, false, func(opts *Options) { opts.Binary = "" })
	if view := notInstalled.View(context.Background()); view.Show || view.State != StateUnavailable {
		t.Fatalf("not installed: %+v", view)
	}

	loggedOut := newFake(t)
	loggedOut.write("status.json", `{"BackendState":"NeedsLogin","Self":{"DNSName":""}}`)
	service, _ = newService(loggedOut, false, nil)
	if view := service.View(context.Background()); view.Show || !strings.Contains(view.Reason, "logged out") {
		t.Fatalf("logged out: %+v", view)
	}
}

func TestPlanRefusals(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(*fake)
		change func(*Options)
		state  State
		reason string
		fixURL string
	}{
		{name: "tailscaled down", setup: func(f *fake) { f.write("down", "") }, state: StateUnavailable, reason: "not running"},
		{name: "stopped", setup: func(f *fake) { f.write("status.json", `{"BackendState":"Stopped"}`) }, state: StateUnavailable, reason: "not connected"},
		{name: "MagicDNS off", setup: func(f *fake) {
			f.write("status.json", `{"BackendState":"Running","Self":{"DNSName":"q.t.ts.net."},"CertDomains":["q.t.ts.net"],"CurrentTailnet":{"MagicDNSEnabled":false}}`)
		}, state: StateRefused, reason: "MagicDNS is off", fixURL: adminDNS},
		{name: "HTTPS off", setup: func(f *fake) {
			f.write("status.json", `{"BackendState":"Running","Self":{"DNSName":"q.t.ts.net."},"CertDomains":null,"CurrentTailnet":{"MagicDNSEnabled":true}}`)
		}, state: StateRefused, reason: "HTTPS certificates are off", fixURL: adminDNS},
		{name: "too old", setup: func(f *fake) { f.write("version", "1.48.2\n") }, state: StateRefused, reason: "older than 1.52.0"},
		{name: "no auth", change: func(opts *Options) { opts.Auth = func() string { return "crowsnest runs with --no-auth" } }, state: StateRefused, reason: "--no-auth"},
		{name: "no loopback", change: func(opts *Options) {
			opts.Target = func() (string, error) { return "", errors.New("crowsnest listens on no loopback address") }
		}, state: StateRefused, reason: "no loopback"},
		{name: "host refused", change: func(opts *Options) {
			opts.AllowHost = func(string) string { return "--no-tailscale-detect is on" }
		}, state: StateRefused, reason: "--no-tailscale-detect"},
		{name: "external", setup: func(f *fake) {
			f.write("serve.json", `{"TCP":{"443":{"HTTPS":true}},"Web":{"quark.tail1234.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:2769"}}}}}`)
		}, state: StateExternal, reason: "did not start already publishes it at https://quark.tail1234.ts.net"},
		{name: "all ports used", setup: func(f *fake) {
			f.write("serve.json", `{"TCP":{"443":{"HTTPS":true},"8443":{"HTTPS":true},"10000":{"TCPForward":"127.0.0.1:22"}}}`)
		}, state: StateRefused, reason: "443, 8443 and 10000 all serve"},
		{name: "configured port used", setup: func(f *fake) {
			f.write("serve.json", `{"TCP":{"9000":{"HTTPS":true}}}`)
		}, change: func(opts *Options) { opts.Port = 9000 }, state: StateRefused, reason: "Port 9000 (--tailscale-serve-port) already serves"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			f := newFake(t)
			if test.setup != nil {
				test.setup(f)
			}
			service, _ := newService(f, false, test.change)
			plan := service.plan(context.Background())
			if plan.State != test.state || !strings.Contains(plan.Reason, test.reason) || plan.Fix == "" || (test.fixURL != "" && plan.FixURL != test.fixURL) {
				t.Fatalf("%+v", plan)
			}
		})
	}
}

func TestPortChoiceNeverClobbers(t *testing.T) {
	cases := []struct {
		serve string
		port  int
		want  int
		url   string
		note  string
	}{
		{serve: `{}`, want: 443, url: "https://quark.tail1234.ts.net"},
		{serve: `{"TCP":{"443":{"HTTPS":true}},"Web":{"quark.tail1234.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:3000"}}}}}`, want: 8443, url: "https://quark.tail1234.ts.net:8443", note: "Port 443 already serves"},
		{serve: `{"Foreground":{"x":{"TCP":{"443":{"HTTPS":true}}}},"TCP":{"8443":{"TCPForward":"127.0.0.1:22"}}}`, want: 10000, url: "https://quark.tail1234.ts.net:10000", note: "Port 443 and 8443"},
		{serve: `{"TCP":{"443":{"HTTPS":true}}}`, port: 4443, want: 4443, url: "https://quark.tail1234.ts.net:4443"},
	}
	for _, test := range cases {
		f := newFake(t)
		f.write("serve.json", test.serve)
		service, _ := newService(f, false, func(opts *Options) { opts.Port = test.port })
		plan := service.plan(context.Background())
		if plan.State != stateReady || plan.Port != test.want || plan.URL != test.url || !strings.Contains(plan.PortNote, test.note) {
			t.Errorf("%s: %+v", test.serve, plan)
		}
	}
}

// ---- The supervised child ---------------------------------------------------------------

func TestPublishThenStop(t *testing.T) {
	f := newFake(t)
	f.write("serve.json", `{"TCP":{"443":{"HTTPS":true}},"Web":{"quark.tail1234.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:3000"}}}}}`)
	var published []string
	var mu sync.Mutex
	service, kv := newService(f, true, func(opts *Options) {
		opts.OnPublished = func(url string) { mu.Lock(); published = append(published, url); mu.Unlock() }
	})
	running(t, service)
	view := waitState(t, service, StatePublishing)
	if view.URL != "https://quark.tail1234.ts.net:8443" || view.Port != 8443 || view.Since == nil {
		t.Fatalf("%+v", view)
	}
	pid := f.fgPID()
	if !alive(pid) {
		t.Fatal("the foreground serve is not running")
	}
	if _, err := kv.GetKV(context.Background(), childKey); err != nil {
		t.Fatal("no child record while publishing")
	}
	if line := f.argv(); !containsLine(line, "serve --https=8443 http://127.0.0.1:2769") {
		t.Fatalf("argv: %q", line)
	}

	service.SetEnabled(false)
	view = waitState(t, service, StateStopped)
	waitFor(t, "the foreground serve to end", func() bool { return !alive(pid) })
	if view.URL == "" {
		t.Fatalf("stopped view lost the offer: %+v", view)
	}
	if _, err := kv.GetKV(context.Background(), childKey); err == nil {
		t.Fatal("the child record outlived the child")
	}
	if !containsLine(f.argv(), "interrupted") {
		t.Fatal("tailscale serve was not interrupted (Ctrl+C) first")
	}
	if config, _ := os.ReadFile(filepath.Join(f.dir, "serve.json")); !strings.Contains(string(config), "127.0.0.1:3000") {
		t.Fatal("the other serve config was touched")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(published) != 1 || published[0] != "https://quark.tail1234.ts.net:8443" {
		t.Fatalf("OnPublished: %q", published)
	}

	service.SetEnabled(true)
	waitState(t, service, StatePublishing)
}

func containsLine(lines []string, want string) bool {
	for _, line := range lines {
		if strings.Contains(line, want) {
			return true
		}
	}
	return false
}

func TestChildGoneWhenRunEnds(t *testing.T) {
	f := newFake(t)
	service, _ := newService(f, true, nil)
	stop := running(t, service)
	waitState(t, service, StatePublishing)
	pid := f.fgPID()
	stop()
	if alive(pid) {
		t.Fatal("tailscale serve outlived Run")
	}
}

// crowsnest SIGKILLed: the watchdog sees its lifeline pipe close and interrupts tailscale serve.
func TestChildGoneWhenCrowsnestIsKilled(t *testing.T) {
	f := newFake(t)
	parent := exec.Command(os.Args[0]) //nolint:gosec // this test binary, as a helper
	parent.Env = append(os.Environ(), "TSSERVE_HELPER=parent", "TSSERVE_FAKE="+f.bin, "TSSERVE_TARGET="+target)
	stdout := &tail{limit: 4096}
	parent.Stdout, parent.Stderr = stdout, stdout
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Process.Kill(); _ = parent.Wait() })
	waitFor(t, "the helper crowsnest to publish", func() bool { return strings.Contains(stdout.String(), "PUBLISHED https://quark.tail1234.ts.net") })
	pid := f.fgPID()
	if !alive(pid) {
		t.Fatal("no foreground serve")
	}
	if err := parent.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	_ = parent.Wait()
	waitFor(t, "tailscale serve to end after crowsnest was killed", func() bool { return !alive(pid) })
	if !containsLine(f.argv(), "interrupted") {
		t.Fatal("tailscale serve was killed rather than interrupted")
	}
}

func TestRestartWithBackoff(t *testing.T) {
	f := newFake(t)
	f.write("mode", "die-after")
	service, _ := newService(f, true, nil)
	running(t, service)
	waitState(t, service, StatePublishing)
	view := waitState(t, service, StateFailed)
	if !strings.Contains(view.Reason, "stopped by itself") || view.RetryAt == nil {
		t.Fatalf("%+v", view)
	}
	waitState(t, service, StatePublishing) // retried
	starts := 0
	for _, line := range f.argv() {
		if strings.HasPrefix(line, "serve --https=") {
			starts++
		}
	}
	if starts < 2 {
		t.Fatalf("started %d times", starts)
	}
}

func TestCrashOnceThenPublishes(t *testing.T) {
	f := newFake(t)
	f.write("mode", "crash-once")
	service, _ := newService(f, true, nil)
	running(t, service)
	waitState(t, service, StatePublishing)
}

func TestServeNotEnabledWaitsWithTheLink(t *testing.T) {
	f := newFake(t)
	f.write("mode", "serve-disabled")
	service, _ := newService(f, true, nil)
	running(t, service)
	var view View
	waitFor(t, "the enable-Serve reason", func() bool {
		view = service.View(context.Background())
		return view.State == StateStarting && strings.Contains(view.Reason, "Serve is not turned on")
	})
	if view.FixURL != "https://login.tailscale.com/f/serve?node=nFAKE123" {
		t.Fatalf("%+v", view)
	}
	f.write("enabled", "")
	waitState(t, service, StatePublishing)
}

func TestHTTPSErrorFromServe(t *testing.T) {
	f := newFake(t)
	f.write("mode", "exit-https")
	service, _ := newService(f, true, nil)
	running(t, service)
	view := waitState(t, service, StateFailed)
	if !strings.Contains(view.Reason, "HTTPS certificates are off") || view.FixURL != adminDNS {
		t.Fatalf("%+v", view)
	}
}

func TestRefusedWhileEnabledIsShown(t *testing.T) {
	f := newFake(t)
	f.write("status.json", `{"BackendState":"NeedsLogin"}`)
	service, _ := newService(f, true, nil)
	running(t, service)
	view := waitState(t, service, StateRefused)
	if !view.Show || !strings.Contains(view.Reason, "logged out") || !view.Enabled {
		t.Fatalf("%+v", view)
	}
	if containsLine(f.argv(), "serve --https") {
		t.Fatal("tailscale serve ran while logged out")
	}
}

func TestNoAuthNeverRuns(t *testing.T) {
	f := newFake(t)
	service, _ := newService(f, true, func(opts *Options) { opts.Auth = func() string { return "crowsnest runs with --no-auth" } })
	running(t, service)
	waitState(t, service, StateRefused)
	time.Sleep(200 * time.Millisecond)
	if containsLine(f.argv(), "serve --https") {
		t.Fatal("tailscale serve ran without a login")
	}
}

// ---- Leftovers -------------------------------------------------------------------

func TestLeftoverOfOursIsStopped(t *testing.T) {
	f := newFake(t)
	watchdog := exec.Command(os.Args[0], append([]string{"--", f.bin}, ServeArgs(443, target)...)...) //nolint:gosec // this test binary
	watchdog.Env = append(os.Environ(), "TSSERVE_HELPER=child")
	watchdog.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	lifeline, _ := watchdog.StdinPipe()
	if err := watchdog.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = lifeline.Close()
		_ = syscall.Kill(-watchdog.Process.Pid, syscall.SIGKILL)
		_ = watchdog.Wait()
	})
	waitFor(t, "the orphan to publish", func() bool { return alive(f.fgPID()) })
	orphan := f.fgPID()

	service, kv := newService(f, false, nil)
	_ = kv.SetKV(context.Background(), childKey, `{"pid":`+strconv.Itoa(watchdog.Process.Pid)+`,"pgid":`+strconv.Itoa(watchdog.Process.Pid)+`,"port":443,"target":"`+target+`"}`)
	running(t, service)
	waitFor(t, "the leftover to be stopped", func() bool { return !alive(orphan) })
	waitFor(t, "the note", func() bool { return strings.Contains(service.currentNote(), "earlier crowsnest left running") })
	if _, err := kv.GetKV(context.Background(), childKey); err == nil {
		t.Fatal("the record was kept")
	}
}

func TestLeftoverNotOursIsLeftAlone(t *testing.T) {
	f := newFake(t)
	other := exec.Command("sleep", "30")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Process.Kill(); _ = other.Wait() })
	service, kv := newService(f, false, nil)
	_ = kv.SetKV(context.Background(), childKey, `{"pid":`+strconv.Itoa(other.Process.Pid)+`,"port":443,"target":"`+target+`"}`)
	running(t, service)
	waitFor(t, "the note", func() bool { return strings.Contains(service.currentNote(), "left it alone") })
	if !alive(other.Process.Pid) {
		t.Fatal("an unrelated process was killed")
	}
}

func TestQR(t *testing.T) {
	if got := QRDataURL("https://quark.tail1234.ts.net:8443"); !strings.HasPrefix(got, "data:image/png;base64,") {
		t.Fatalf("%.40s", got)
	}
	if QRDataURL("") != "" {
		t.Fatal("empty text")
	}
}

// Under go test a Service never looks the real CLI up, whatever FindReal says.
func TestNeverTheRealBinaryUnderTest(t *testing.T) {
	service := New(Options{FindReal: true}, false)
	if service.opts.Binary != "" {
		t.Fatalf("found %q", service.opts.Binary)
	}
}
