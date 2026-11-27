// Wiring for the controls added on wip/landing (DESIGN.md 6.11 to 6.14, 7.12): the pause switch,
// the throttle (slot limit, load hold, the heavy directory), presence, and the review gate and
// Land settings handed to the jobs service. They are built once, early in serve, and handed to
// every service that has to respect them; each is nil-safe where a caller may run without it.

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/jclement/crowsnest/internal/approvals"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/digest"
	"github.com/jclement/crowsnest/internal/herdr"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/jobs"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/pause"
	"github.com/jclement/crowsnest/internal/presence"
	"github.com/jclement/crowsnest/internal/projects"
	"github.com/jclement/crowsnest/internal/questions"
	"github.com/jclement/crowsnest/internal/spend"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/throttle"
)

// controls are the instance-wide switches every automation consults.
type controls struct {
	pause    *pause.Switch
	throttle *throttle.Throttle
	presence *presence.Service
	// heavyDir is <state>/heavy, named in every agent's pane as CROWSNEST_HEAVY_DIR.
	heavyDir string
	// heavyDeferred is an outside `heavy` that `crowsnest heavy` hands commands to ("" none).
	heavyDeferred string
	landing       jobs.LandingOptions
	logger        *slog.Logger
}

// newControls builds the controls. The pause file is honoured only for the real instance (a
// --demo run must not be paused by, or pause, the real one).
func newControls(cfg config.Config, db *store.Store, events *hub.Hub, journalLog *journal.Journal, logger *slog.Logger) *controls {
	file := ""
	if !cfg.Demo {
		file = pausedFile()
	}
	c := &controls{logger: logger, heavyDir: filepath.Join(cfg.StateDir, "heavy")}
	c.pause = pause.New(pause.Options{KV: db, File: file, Hub: events, Journal: journalLog, Logger: logger})
	c.throttle = throttle.New(throttle.Options{KV: db, Logger: logger,
		Defaults: throttle.Settings{MaxRunning: cfg.MaxRunningJobs, LoadPerCore: cfg.LoadHold}})
	c.presence = presence.New(presence.Options{KV: db, Hub: events, Logger: logger})
	if err := throttle.WriteHeavyConfig(c.heavyDir, cfg.HeavySlots); err != nil {
		logger.Warn("writing the heavy-command configuration", "err", err)
	}
	_, self := executablePaths()
	c.heavyDeferred = throttle.OutsideHeavy(exec.LookPath, self, os.Getenv("CROWSNEST_HEAVY_DEFER"))
	_, codexErr := exec.LookPath("codex")
	c.landing = jobs.LandingOptions{ReviewDefault: cfg.Review, CodexInstalled: codexErr == nil, MergeMode: cfg.MergeMode,
		Protected: cfg.ProtectedBranches}
	logger.Info("throttling", "max_running_jobs", c.throttle.Settings(context.Background()).MaxRunning, "load_hold", cfg.LoadHold,
		"heavy_slots", cfg.HeavySlots, "heavy_deferred_to", c.heavyDeferred, "review", cfg.Review, "merge", cfg.MergeMode)
	return c
}

// run starts the background loops: the pause's watch (auto-resume, the file), the load sampler,
// and do not disturb's end.
func (c *controls) run(ctx context.Context, demo bool) {
	go c.pause.Run(ctx)
	go c.presence.Run(ctx)
	if !demo {
		go c.throttle.Run(ctx)
	}
}

// paused is the pause as a plain func, for options that take one.
func (c *controls) paused() bool { return c != nil && c.pause.Paused() }

// held journals what the pause held, once per key.
func (c *controls) held(ctx context.Context, key, what string) {
	if c != nil {
		c.pause.Held(ctx, key, what)
	}
}

// presenceWord is here/maybe/away for the poke header.
func (c *controls) presenceWord() string {
	if c == nil {
		return ""
	}
	return c.presence.Presence()
}

// route asks presence whether item may go out on channel now (Web Push, --notify-url).
func (c *controls) route(ctx context.Context, item, channel string, blocking bool) (bool, string) {
	if c == nil {
		return true, ""
	}
	return c.presence.Route(ctx, item, channel, blocking)
}

// notifyURL wraps a --notify-url sender so presence and do not disturb decide whether a line
// goes out: away, yes; here, no (it is on screen).
func (c *controls) notifyURL(send func(string)) func(string) {
	if c == nil || send == nil {
		return send
	}
	return func(text string) {
		if ok, why := c.route(context.Background(), "notice:"+text, presence.ChannelNotify, false); !ok {
			c.logger.Info("not sending a notice to --notify-url: "+why, "text", text)
			return
		}
		send(text)
	}
}

// notifyAsked wraps the question notifier the same way, blocking questions passing do not
// disturb.
func (c *controls) notifyAsked(send func(questions.Question)) func(questions.Question) {
	if c == nil || send == nil {
		return send
	}
	return func(question questions.Question) {
		blocking := question.Urgency == "blocking"
		if ok, why := c.route(context.Background(), "question:"+question.ID, presence.ChannelNotify, blocking); !ok {
			c.logger.Info("not sending a question to --notify-url: "+why, "question", question.ID)
			return
		}
		send(question)
	}
}

// heavyEnv is the pane environment entry naming the heavy directory.
func (c *controls) heavyEnv() map[string]string {
	if c == nil || c.heavyDir == "" {
		return nil
	}
	return map[string]string{throttle.DirEnv: c.heavyDir}
}

// dndSummary is sent once when do not disturb ends with notifications held.
func dndSummary(notify func(string)) func(int) {
	return func(held int) {
		if notify != nil {
			notify(fmt.Sprintf("crowsnest: do not disturb ended; %d notification(s) were held meanwhile: see the tray and the Inbox", held))
		}
	}
}

// pauseOrNil is the switch, or nil without controls.
func (c *controls) pauseOrNil() *pause.Switch {
	if c == nil {
		return nil
	}
	return c.pause
}

// ---- The digest ----------------------------------------------------------------------------

// digestProxy lets the agent socket, built before the digest service, reach it once it exists.
type digestProxy struct {
	service atomic.Pointer[digest.Service]
}

// Submit implements agentapi.DigestSubmitter.
func (p *digestProxy) Submit(ctx context.Context, day, body string) error {
	service := p.service.Load()
	if service == nil {
		return errors.New("the digest is not ready yet")
	}
	return service.Submit(ctx, day, body)
}

// digestDeps are what the digest service reads.
type digestDeps struct {
	cfg          config.Config
	db           *store.Store
	journal      *journal.Journal
	client       herdr.Client
	orchestrator *orch.Orchestrator
	questions    *questions.Service
	approvals    *approvals.Service
	meter        *spend.Meter
	projects     *projects.Service
	ctl          *controls
	logger       *slog.Logger
}

// startDigest builds the daily digest and runs its schedule (not under --demo, whose digest is
// seeded and made with Digest now).
func startDigest(ctx context.Context, d digestDeps) *digest.Service {
	service := digest.New(digest.Options{
		Store: d.db, Journal: d.journal, Logger: d.logger, StateDir: d.cfg.StateDir, Paused: d.ctl.paused,
		Notice: func(ctx context.Context, title, body string) (int64, error) {
			if d.questions == nil {
				return 0, errors.New("the inbox is not ready")
			}
			question, err := d.questions.Ask(ctx, questions.AskInput{Title: title, BodyMD: body, Kind: "notice", Urgency: "low"}, questions.Caller{})
			if err != nil {
				return 0, err
			}
			id, _ := strconv.ParseInt(strings.TrimPrefix(question.ID, "q_"), 10, 64)
			return id, nil
		},
		Orchestrator: func(ctx context.Context, day, file string) error {
			if d.orchestrator == nil || d.cfg.NoOrchestrator || d.cfg.Demo {
				// --demo's scripted orchestrator cannot write one: crowsnest's own, at once.
				return errors.New("the orchestrator is off")
			}
			status := d.orchestrator.Status()
			if status.State != orch.StateIdle || status.PaneID == "" {
				return fmt.Errorf("the orchestrator is %s", status.State)
			}
			text := fmt.Sprintf("[crowsnest digest %s] Write today's digest for the human from the facts in %s (crowsnest's records; use nothing else). "+
				"Keep it short: what finished, what is stuck or waiting on them, what each project's lead did, the day's spend, what is next. "+
				"Write it to a file and deliver it with `crowsnest digest submit --day %s --file <file>`; nothing else is needed.", day, file, day)
			_, err := orch.Prompt(ctx, d.client, status.PaneID, orch.PromptRequest{Text: text, ExpectAgent: status.Name})
			return err
		},
		Projects: func(ctx context.Context) map[int64][2]string {
			out := map[int64][2]string{}
			if d.projects == nil {
				return out
			}
			list, err := d.projects.List(ctx, true)
			if err != nil {
				return out
			}
			for _, project := range list {
				out[project.ID] = [2]string{project.Name, "lead-" + project.Slug}
			}
			return out
		},
		Spend: func(ctx context.Context) string {
			if d.meter == nil {
				return "not measured"
			}
			period, err := d.meter.Period(ctx, d.meter.Location())
			if err != nil || !period.Today.Measured {
				return "not measured"
			}
			return spend.CostText(period.Today.Summary) + " (" + spend.FormatTokens(period.Today.TotalTokens) + " tokens)"
		},
		Approvals: func(ctx context.Context) int {
			if d.approvals == nil {
				return 0
			}
			pending, err := d.approvals.List(ctx, []approvals.Status{approvals.StatusPending}, 200)
			if err != nil {
				return 0
			}
			return len(pending)
		},
	})
	if !d.cfg.Demo {
		go service.Run(ctx)
	} else if list, err := service.List(ctx, 1); err == nil && len(list) == 0 {
		// --demo shows one digest in the list and the Inbox (DESIGN.md 7.12).
		if err := service.SeedDemo(ctx); err != nil {
			d.logger.Warn("seeding the demo digest", "err", err)
		}
	}
	return service
}
