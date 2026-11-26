// Per-agent CPU and memory, for the System page. herdr's pane.process_info names each pane's
// foreground processes; an agent's cost is its process and everything under it (the tools it
// runs: a build, a test suite), summed. CPU % needs two readings, so a Processes keeps each
// process between calls and reports its usage since the previous call (the first call reports
// 0 for a process it has not seen).

package sysstat

import (
	"context"
	"sync"

	"github.com/shirou/gopsutil/v4/process"
)

// maxTreeSize caps how many processes one agent's tree may count, so a fork bomb in a pane
// cannot make the System page expensive.
const maxTreeSize = 200

// Usage is one process tree's cost.
type Usage struct {
	CPU       float64 `json:"cpu"` // percent of one core (can exceed 100 with several cores)
	RSS       uint64  `json:"rss"` // bytes resident
	Processes int     `json:"processes"`
}

// Processes measures process trees across calls.
type Processes struct {
	mu    sync.Mutex
	known map[int32]*process.Process
}

// NewProcesses returns an empty tracker.
func NewProcesses() *Processes { return &Processes{known: map[int32]*process.Process{}} }

// Tree measures the processes rooted at pids and their descendants.
func (p *Processes) Tree(ctx context.Context, pids []int) Usage {
	p.mu.Lock()
	defer p.mu.Unlock()
	seen := map[int32]bool{}
	var usage Usage
	queue := make([]int32, 0, len(pids))
	for _, pid := range pids {
		queue = append(queue, int32(pid)) //nolint:gosec // pids fit in int32
	}
	for len(queue) > 0 && len(seen) < maxTreeSize {
		pid := queue[0]
		queue = queue[1:]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		proc := p.known[pid]
		if proc == nil {
			created, err := process.NewProcessWithContext(ctx, pid)
			if err != nil {
				continue
			}
			proc = created
			p.known[pid] = proc
		}
		if percent, err := proc.PercentWithContext(ctx, 0); err == nil {
			usage.CPU += percent
		}
		if memory, err := proc.MemoryInfoWithContext(ctx); err == nil {
			usage.RSS += memory.RSS
		}
		usage.Processes++
		if children, err := proc.ChildrenWithContext(ctx); err == nil {
			for _, child := range children {
				queue = append(queue, child.Pid)
			}
		}
	}
	return usage
}

// Prune drops processes that have exited, so the tracker does not grow.
func (p *Processes) Prune(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for pid, proc := range p.known {
		if running, err := proc.IsRunningWithContext(ctx); err != nil || !running {
			delete(p.known, pid)
		}
	}
}
