package throttle

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHelperHoldSlot is not a test: run as a child process (CROWSNEST_HEAVY_HELPER set) it takes
// a slot in the given directory, prints "held", and keeps it until its stdin closes. It lets the
// tests below prove the gate across real processes, not only goroutines.
func TestHelperHoldSlot(t *testing.T) {
	dir := os.Getenv("CROWSNEST_HEAVY_HELPER")
	if dir == "" {
		t.Skip("helper process only")
	}
	slot, err := AcquireHeavy(context.Background(), HeavyOptions{Dir: dir, Slots: 2, Job: "j_9"}, "helper build")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = os.Stdout.WriteString("held\n")
	buf := make([]byte, 1)
	_, _ = os.Stdin.Read(buf)
	slot.Release()
}

// holdInChild starts a helper process holding a slot in dir and returns a function that lets it
// go. It waits until the child says it holds the slot.
func holdInChild(t *testing.T, dir string) func() {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperHoldSlot$") //nolint:gosec // the test binary itself
	cmd.Env = append(os.Environ(), "CROWSNEST_HEAVY_HELPER="+dir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line := make([]byte, 5)
	if _, err := stdout.Read(line); err != nil || string(line) != "held\n" {
		t.Fatalf("child did not take a slot: %q %v", line, err)
	}
	release := func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return release
}

func TestHeavyGateAcrossProcesses(t *testing.T) {
	dir := t.TempDir()
	if err := WriteHeavyConfig(dir, 2); err != nil {
		t.Fatal(err)
	}
	releaseA := holdInChild(t, dir)
	releaseB := holdInChild(t, dir)

	status := ReadHeavyStatus(dir, 0)
	if status.Slots != 2 || len(status.Running) != 2 || status.Running[0].Job != "j_9" || status.Running[0].Command != "helper build" {
		t.Fatalf("status with two holders: %+v", status)
	}

	// A third command waits, says why, and is listed as waiting.
	var said bytes.Buffer
	got := make(chan *HeavySlot, 1)
	go func() {
		slot, err := AcquireHeavy(context.Background(), HeavyOptions{Dir: dir, Poll: 20 * time.Millisecond, Stderr: &said}, "go test ./...")
		if err != nil {
			t.Error(err)
		}
		got <- slot
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(ReadHeavyStatus(dir, 0).Waiting) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if waiting := ReadHeavyStatus(dir, 0).Waiting; len(waiting) != 1 || waiting[0].Command != "go test ./..." {
		t.Fatalf("waiting %+v", waiting)
	}
	select {
	case <-got:
		t.Fatal("a third command got a slot while two were held")
	case <-time.After(100 * time.Millisecond):
	}
	releaseA() // the child exits: the kernel drops its lock
	select {
	case slot := <-got:
		if slot == nil {
			t.Fatal("no slot")
		}
		defer slot.Release()
	case <-time.After(5 * time.Second):
		t.Fatal("the waiter never got the freed slot")
	}
	if !strings.Contains(said.String(), "all 2 slots busy, waiting for helper build (job j_9)") {
		t.Fatalf("the waiter did not say why: %q", said.String())
	}
	if len(ReadHeavyStatus(dir, 0).Waiting) != 0 {
		t.Fatal("the wait file was left behind")
	}
	releaseB()
}

func TestHeavyKilledHolderFreesItsSlot(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperHoldSlot$") //nolint:gosec // the test binary itself
	cmd.Env = append(os.Environ(), "CROWSNEST_HEAVY_HELPER="+dir)
	stdout, _ := cmd.StdoutPipe()
	_, _ = cmd.StdinPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line := make([]byte, 5)
	_, _ = stdout.Read(line)
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if running := ReadHeavyStatus(dir, 1).Running; len(running) != 0 {
		t.Fatalf("a killed holder still holds: %+v", running)
	}
	slot, err := AcquireHeavy(context.Background(), HeavyOptions{Dir: dir, Slots: 1, Timeout: time.Second}, "x")
	if err != nil {
		t.Fatal(err)
	}
	slot.Release()
}

func TestHeavyTimeout(t *testing.T) {
	dir := t.TempDir()
	first, err := AcquireHeavy(context.Background(), HeavyOptions{Dir: dir, Slots: 1}, "a")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	_, err = AcquireHeavy(context.Background(), HeavyOptions{Dir: dir, Slots: 1, Poll: 10 * time.Millisecond, Timeout: 50 * time.Millisecond, Stderr: &bytes.Buffer{}}, "b")
	if !errors.Is(err, ErrHeavyTimeout) {
		t.Fatalf("err %v", err)
	}
}

func TestOutsideHeavy(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "heavy")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o700); err != nil { //nolint:gosec // a test script
		t.Fatal(err)
	}
	look := func(name string) (string, error) {
		if name == "heavy" {
			return script, nil
		}
		return "", exec.ErrNotFound
	}
	none := func(string) (string, error) { return "", exec.ErrNotFound }
	resolved, _ := filepath.EvalSymlinks(script)
	cases := []struct {
		name, self, setting, want string
		look                      func(string) (string, error)
	}{
		{"defers to an outside heavy", "/usr/local/bin/crowsnest", "", script, look},
		{"none on PATH", "/usr/local/bin/crowsnest", "", "", none},
		{"turned off", "/usr/local/bin/crowsnest", "0", "", look},
		{"heavy is crowsnest itself", resolved, "", "", look},
	}
	for _, tc := range cases {
		if got := OutsideHeavy(tc.look, tc.self, tc.setting); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestCapEnvKeepsWhatIsSet(t *testing.T) {
	env := CapEnv([]string{"GOFLAGS=-p=1", "PATH=/bin"})
	if !hasEnv(env, "MAKEFLAGS") || !hasEnv(env, "VITEST_MAX_FORKS") {
		t.Fatalf("caps missing: %v", env)
	}
	count := 0
	for _, entry := range env {
		if strings.HasPrefix(entry, "GOFLAGS=") {
			count++
			if entry != "GOFLAGS=-p=1" {
				t.Fatalf("overrode the caller's GOFLAGS: %s", entry)
			}
		}
	}
	if count != 1 {
		t.Fatalf("GOFLAGS %d times", count)
	}
}
