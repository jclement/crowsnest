// The sampler with a fake source (it samples only while watched, keeps a minute, publishes `sys`
// events), the cgroup override from fixture files, and a process tree measured for real (this
// test's own process).

package sysstat

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jclement/crowsnest/internal/hub"
)

type fakeSource struct{ calls atomic.Int32 }

func (f *fakeSource) Sample(context.Context) (Sample, error) {
	n := f.calls.Add(1)
	return Sample{At: time.Now(), CPU: float64(n), Cores: 8, MemUsed: 4 << 30, MemTotal: 16 << 30, Load1: 2.5, Scope: ScopeHost}, nil
}

func TestSamplerKeepsAMinuteAndPublishes(t *testing.T) {
	events := hub.New()
	defer events.Close()
	sub := events.Subscribe(64)
	defer sub.Close()
	source := &fakeSource{}
	sampler := New(Options{Source: source, Hub: events, Working: func() int { return 3 }})
	for range historyLength + 5 {
		sampler.Tick(context.Background())
	}
	stats := sampler.Stats(context.Background())
	if len(stats.History) != historyLength || stats.Latest == nil || stats.Latest.CPU != float64(historyLength+5) || stats.Latest.Working != 3 {
		t.Fatalf("stats: %d samples, latest %+v", len(stats.History), stats.Latest)
	}
	select {
	case event := <-sub.Events():
		if event.Type != "sys" {
			t.Fatalf("event %q", event.Type)
		}
		if sample, ok := event.Data.(Sample); !ok || sample.Cores != 8 {
			t.Fatalf("event data %#v", event.Data)
		}
	default:
		t.Fatal("no sys event")
	}
}

func TestSamplerOnlySamplesWhileWatched(t *testing.T) {
	source := &fakeSource{}
	var watching atomic.Bool
	sampler := New(Options{Source: source, Interval: 10 * time.Millisecond, Active: watching.Load})
	ctx, cancel := context.WithCancel(context.Background())
	go sampler.Run(ctx)
	time.Sleep(60 * time.Millisecond)
	if source.calls.Load() != 0 {
		t.Fatalf("sampled %d times with nobody watching", source.calls.Load())
	}
	watching.Store(true)
	time.Sleep(60 * time.Millisecond)
	cancel()
	if source.calls.Load() == 0 {
		t.Fatal("never sampled while watched")
	}
}

func TestStatsSamplesOnceWhenEmpty(t *testing.T) {
	source := &fakeSource{}
	sampler := New(Options{Source: source, Active: func() bool { return false }})
	if stats := sampler.Stats(context.Background()); stats.Latest == nil || source.calls.Load() != 1 {
		t.Fatalf("stats %+v calls %d", stats, source.calls.Load())
	}
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCgroupLimitsReplaceHostFigures(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "docker", "abc")
	writeFile(t, filepath.Join(root, "self-cgroup"), "0::/docker/abc\n")
	writeFile(t, filepath.Join(dir, "cgroup.controllers"), "cpu memory\n")
	writeFile(t, filepath.Join(dir, "memory.max"), "2147483648\n")
	writeFile(t, filepath.Join(dir, "memory.current"), "1610612736\n")
	writeFile(t, filepath.Join(dir, "memory.stat"), "anon 1\ninactive_file 536870912\n")
	writeFile(t, filepath.Join(dir, "cpu.max"), "200000 100000\n")
	writeFile(t, filepath.Join(dir, "cpu.stat"), "usage_usec 1000000\n")
	source := &HostSource{CgroupRoot: root, ProcSelfCgroup: filepath.Join(root, "self-cgroup")}
	sample := Sample{Scope: ScopeHost, MemTotal: 64 << 30, Cores: 16}
	source.applyCgroup(&sample)
	if sample.Scope != ScopeContainer || sample.MemTotal != 2<<30 || sample.MemUsed != 1<<30 || sample.Cores != 2 {
		t.Fatalf("after the cgroup: %+v", sample)
	}
	source.lastAt = time.Now().Add(-time.Second)
	source.lastUsage = 0
	source.applyCgroup(&sample)
	if sample.CPU <= 0 || sample.CPU > 100 {
		t.Errorf("cgroup CPU %v", sample.CPU)
	}
}

func TestNoCgroupLimitKeepsHost(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "self-cgroup"), "0::/\n")
	writeFile(t, filepath.Join(root, "cgroup.controllers"), "cpu memory\n")
	writeFile(t, filepath.Join(root, "memory.max"), "max\n")
	writeFile(t, filepath.Join(root, "cpu.max"), "max 100000\n")
	source := &HostSource{CgroupRoot: root, ProcSelfCgroup: filepath.Join(root, "self-cgroup")}
	sample := Sample{Scope: ScopeHost, MemTotal: 8}
	source.applyCgroup(&sample)
	if sample.Scope != ScopeHost || sample.MemTotal != 8 {
		t.Fatalf("%+v", sample)
	}
}

func TestHostSourceReadsThisMachine(t *testing.T) {
	sample, err := (&HostSource{DataDir: t.TempDir()}).Sample(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sample.Cores < 1 || sample.MemTotal == 0 || sample.DiskTotal == 0 {
		t.Fatalf("%+v", sample)
	}
}

func TestProcessTree(t *testing.T) {
	procs := NewProcesses()
	usage := procs.Tree(context.Background(), []int{os.Getpid()})
	if usage.Processes < 1 || usage.RSS == 0 {
		t.Fatalf("%+v", usage)
	}
	procs.Prune(context.Background())
}
