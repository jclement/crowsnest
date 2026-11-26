// Throttling, the review gate and landing (DESIGN.md 6.7, 6.11, 6.12): how many job agents run
// at once, when a busy machine holds new starts, how many heavy commands run at once, which
// reviewer a builder's work gets, and how Land merges. Kept in their own file and loader step,
// like upkeep.go, so the option table's other rows stay as they were.

package config

import (
	"fmt"
	"strconv"
	"strings"
)

// Option names for this file's settings.
const (
	optMaxRunningJobs = "max-running-jobs"
	optLoadHold       = "load-hold"
	optHeavySlots     = "heavy-slots"
	optReview         = "review"
	optMerge          = "merge"
)

// Review gate defaults.
const (
	ReviewAuto   = "auto"   // the other engine when it is installed, else the same engine
	ReviewOff    = "off"    // no review gate unless a job asks for one
	ReviewClaude = "claude" // always a Claude reviewer
	ReviewCodex  = "codex"  // always a Codex reviewer
	ReviewSame   = "same"   // the builder's own engine, in the reviewer role
)

// Merge modes for Land.
const (
	MergeCommit = "merge" // always a merge commit (--no-ff)
	MergeFF     = "ff"    // fast-forward only; refuses when the base branch has moved on
)

// DefaultLoadHold is the load per core above which new job starts wait (for a minute).
const DefaultLoadHold = 2.0

func init() {
	options = append(options,
		option{name: optMaxRunningJobs, env: []string{"CROWSNEST_MAX_RUNNING_JOBS"}, kind: kindInt, def: "0", usage: "job agents running at once; more wait queued and start in order (0: a quarter of the cores, at least 2)"},
		option{name: optLoadHold, env: []string{"CROWSNEST_LOAD_HOLD"}, def: strconv.FormatFloat(DefaultLoadHold, 'f', -1, 64), usage: "hold new job starts while the load average per core stays above this for a minute (0: off)"},
		option{name: optHeavySlots, env: []string{"CROWSNEST_HEAVY_SLOTS"}, kind: kindInt, def: "2", usage: "heavy commands (builds, test suites) that may run at once through `crowsnest heavy`"},
		option{name: optReview, env: []string{"CROWSNEST_REVIEW"}, def: ReviewAuto, usage: "review gate for builder jobs in a worktree: auto (the other engine if installed), same, claude, codex or off"},
		option{name: optMerge, env: []string{"CROWSNEST_MERGE"}, def: MergeCommit, usage: "how Land merges a job's branch: merge (a merge commit) or ff (fast-forward only)"},
	)
}

func (l *loader) loadLanding(cfg *Config) error {
	var err error
	if cfg.MaxRunningJobs, err = l.integer(optMaxRunningJobs); err != nil {
		return err
	}
	if cfg.MaxRunningJobs < 0 || cfg.MaxRunningJobs > 256 {
		return fmt.Errorf("--max-running-jobs %d: give 0 (a quarter of the cores) or 1 to 256", cfg.MaxRunningJobs)
	}
	raw := strings.TrimSpace(l.str(optLoadHold))
	if cfg.LoadHold, err = strconv.ParseFloat(raw, 64); err != nil || cfg.LoadHold < 0 || cfg.LoadHold > 64 {
		return fmt.Errorf("--load-hold %q: give a load per core such as 2 or 1.5, or 0 to turn it off", raw)
	}
	if cfg.HeavySlots, err = l.integer(optHeavySlots); err != nil {
		return err
	}
	if cfg.HeavySlots < 1 || cfg.HeavySlots > 64 {
		return fmt.Errorf("--heavy-slots %d: give 1 to 64", cfg.HeavySlots)
	}
	cfg.Review = strings.ToLower(strings.TrimSpace(l.str(optReview)))
	switch cfg.Review {
	case ReviewAuto, ReviewOff, ReviewClaude, ReviewCodex, ReviewSame:
	default:
		return fmt.Errorf("--review %q: give auto, same, claude, codex or off", cfg.Review)
	}
	cfg.MergeMode = strings.ToLower(strings.TrimSpace(l.str(optMerge)))
	if cfg.MergeMode != MergeCommit && cfg.MergeMode != MergeFF {
		return fmt.Errorf("--merge %q: give merge or ff", cfg.MergeMode)
	}
	return nil
}
