// The real Source: the host's figures from gopsutil (pure Go on Linux, purego on macOS, so CGO
// stays off), replaced by the cgroup's where a container's cgroup v2 limits CPU or memory. A
// container that sets no limit shares the host's CPU and memory, so the host's figures are the
// honest ones there and Scope stays "host".

package sysstat

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
)

// HostSource reads this machine (or its container's cgroup).
type HostSource struct {
	DataDir string // the volume whose free space is reported
	// CgroupRoot is where cgroup v2 is mounted ("/sys/fs/cgroup"); empty means that default.
	// ProcSelfCgroup is /proc/self/cgroup. Tests point both at fixtures.
	CgroupRoot     string
	ProcSelfCgroup string

	mu        sync.Mutex
	lastUsage uint64 // cgroup cpu.stat usage_usec at the previous sample
	lastAt    time.Time
}

// Sample reads the host, then lets the cgroup override what it limits.
func (h *HostSource) Sample(ctx context.Context) (Sample, error) {
	sample := Sample{At: time.Now(), Scope: ScopeHost}
	percents, err := cpu.PercentWithContext(ctx, 0, false)
	if err != nil {
		return Sample{}, err
	}
	if len(percents) > 0 {
		sample.CPU = percents[0]
	}
	if cores, err := cpu.CountsWithContext(ctx, true); err == nil {
		sample.Cores = float64(cores)
	}
	if memory, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		sample.MemUsed, sample.MemTotal = memory.Used, memory.Total
	}
	if average, err := load.AvgWithContext(ctx); err == nil {
		sample.Load1, sample.Load5, sample.Load15 = average.Load1, average.Load5, average.Load15
	}
	if h.DataDir != "" {
		if usage, err := disk.UsageWithContext(ctx, nearestDir(h.DataDir)); err == nil {
			sample.DiskFree, sample.DiskTotal = usage.Free, usage.Total
		}
	}
	h.applyCgroup(&sample)
	return sample, nil
}

// applyCgroup replaces CPU and memory with the cgroup's when it sets limits.
func (h *HostSource) applyCgroup(sample *Sample) {
	dir := h.cgroupDir()
	if dir == "" {
		return
	}
	if limit, ok := readLimit(filepath.Join(dir, "memory.max")); ok {
		if current, ok := readUint(filepath.Join(dir, "memory.current")); ok {
			used := current
			if inactive, ok := statValue(filepath.Join(dir, "memory.stat"), "inactive_file"); ok && inactive < used {
				used -= inactive // page cache the kernel would drop first, as `docker stats` does
			}
			sample.MemUsed, sample.MemTotal, sample.Scope = used, limit, ScopeContainer
		}
	}
	if cores, ok := readCPUQuota(filepath.Join(dir, "cpu.max")); ok {
		if usage, ok := statValue(filepath.Join(dir, "cpu.stat"), "usage_usec"); ok {
			h.mu.Lock()
			now := time.Now()
			if !h.lastAt.IsZero() && usage >= h.lastUsage {
				elapsed := now.Sub(h.lastAt).Microseconds()
				if elapsed > 0 {
					sample.CPU = min(100, float64(usage-h.lastUsage)/(float64(elapsed)*cores)*100)
				}
			}
			h.lastUsage, h.lastAt = usage, now
			h.mu.Unlock()
			sample.Cores, sample.Scope = cores, ScopeContainer
		}
	}
}

// cgroupDir is this process's cgroup v2 directory, "" when there is none (macOS, cgroup v1).
func (h *HostSource) cgroupDir() string {
	root, self := h.CgroupRoot, h.ProcSelfCgroup
	if root == "" {
		root = "/sys/fs/cgroup"
	}
	if self == "" {
		self = "/proc/self/cgroup"
	}
	data, err := os.ReadFile(self) //nolint:gosec // a fixed system path, or a test fixture
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if path, ok := strings.CutPrefix(line, "0::"); ok {
			dir := filepath.Join(root, filepath.Clean("/"+path))
			if _, err := os.Stat(filepath.Join(dir, "cgroup.controllers")); err == nil {
				return dir
			}
		}
	}
	return ""
}

// readLimit reads a cgroup limit file: a number, or "max" (no limit: false).
func readLimit(path string) (uint64, bool) {
	data, err := os.ReadFile(path) //nolint:gosec // cgroup file
	if err != nil {
		return 0, false
	}
	text := strings.TrimSpace(string(data))
	if text == "max" {
		return 0, false
	}
	value, err := strconv.ParseUint(text, 10, 64)
	return value, err == nil
}

func readUint(path string) (uint64, bool) {
	data, err := os.ReadFile(path) //nolint:gosec // cgroup file
	if err != nil {
		return 0, false
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	return value, err == nil
}

// readCPUQuota reads cpu.max ("<quota> <period>" or "max <period>") as cores.
func readCPUQuota(path string) (float64, bool) {
	data, err := os.ReadFile(path) //nolint:gosec // cgroup file
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(data))
	if len(fields) != 2 || fields[0] == "max" {
		return 0, false
	}
	quota, err1 := strconv.ParseFloat(fields[0], 64)
	period, err2 := strconv.ParseFloat(fields[1], 64)
	if err1 != nil || err2 != nil || period <= 0 {
		return 0, false
	}
	return quota / period, true
}

// statValue reads one "key value" line from a cgroup stat file.
func statValue(path, key string) (uint64, bool) {
	data, err := os.ReadFile(path) //nolint:gosec // cgroup file
	if err != nil {
		return 0, false
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, key+" "); ok {
			value, err := strconv.ParseUint(strings.TrimSpace(rest), 10, 64)
			return value, err == nil
		}
	}
	return 0, false
}

func nearestDir(path string) string {
	for {
		if _, err := os.Stat(path); err == nil {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return path
		}
		path = parent
	}
}
