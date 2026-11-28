package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Inside a sandbox that keeps writes in the workspace (a Codex job), heavy cannot make a slot
// file: it runs the command at once instead of waiting for a slot for ever.
func TestHeavyRunsAtOnceWhenSlotsCannotBeWritten(t *testing.T) {
	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	if slotsWritable(filepath.Join(locked, "heavy")) || slotsWritable(locked) {
		t.Fatal("a directory that refuses writes was called writable")
	}
	if !slotsWritable(filepath.Join(t.TempDir(), "heavy")) {
		t.Fatal("a writable directory was refused")
	}
	marker := filepath.Join(t.TempDir(), "ran")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := runHeavy(ctx, locked, time.Hour, []string{"touch", marker}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("the command did not run")
	}
}
