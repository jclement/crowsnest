// The system service against an in-memory database and the doctor's demo machines: settings
// (validated by a reload, refused when a flag or env var set them, never for sensitive options,
// journalled, applied live), the first run, the gate and the log ring.

package system

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/doctor"
	"github.com/jclement/crowsnest/internal/hub"
	"github.com/jclement/crowsnest/internal/journal"
	"github.com/jclement/crowsnest/internal/orch"
	"github.com/jclement/crowsnest/internal/store"
)

type fakeOrchestrator struct{ kicks int }

func (f *fakeOrchestrator) Status() orch.Status { return orch.Status{State: orch.StateWaiting} }
func (f *fakeOrchestrator) Kick()               { f.kicks++ }

type rig struct {
	service *Service
	db      *store.Store
	journal *journal.Journal
	applied []config.Config
	orch    *fakeOrchestrator
}

// newRig builds a service whose config comes from config.Load with args and env, so the
// precedence under test is the real loader's.
func newRig(t *testing.T, state string, args []string, env map[string]string) *rig {
	t.Helper()
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	home := t.TempDir()
	load := func(stored map[string]string) (config.Config, error) {
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		config.RegisterFlags(flags)
		if err := flags.Parse(args); err != nil {
			return config.Config{}, err
		}
		return config.Load(flags, config.Options{
			Stored:     stored,
			Getenv:     func(key string) (string, bool) { value, ok := env[key]; return value, ok },
			HomeDir:    func() (string, error) { return home, nil },
			Hostname:   func() (string, error) { return "box", nil },
			Interfaces: func() ([]config.Interface, error) { return nil, nil },
		})
	}
	cfg, err := load(nil)
	if err != nil {
		t.Fatal(err)
	}
	events := hub.New()
	t.Cleanup(events.Close)
	r := &rig{db: db, journal: journal.New(db, events, slog.New(slog.DiscardHandler), nil), orch: &fakeOrchestrator{}}
	r.service = New(Options{
		Config: cfg, Version: "test", KV: db, Journal: r.journal, Hub: events, Logger: slog.New(slog.DiscardHandler),
		Reload: load, Env: doctor.DemoEnv(state),
		Apply: func(_, next config.Config) { r.applied = append(r.applied, next) },
	})
	r.service.SetOrchestrator(r.orch)
	return r
}

func (r *rig) setting(t *testing.T, name string) SettingView {
	t.Helper()
	page, err := r.service.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range page.Settings {
		if view.Name == name {
			return view
		}
	}
	t.Fatalf("no setting %s", name)
	return SettingView{}
}

func TestUpdateSettingSavesValidatesJournalsApplies(t *testing.T) {
	r := newRig(t, doctor.DemoAllGreen, nil, nil)
	ctx := context.Background()
	if view := r.setting(t, "poke-interval"); view.Value != "15m" || view.Source != config.KindDefault || view.Locked != "" {
		t.Fatalf("before: %+v", view)
	}
	view, err := r.service.UpdateSetting(ctx, "poke-interval", " 5m ", false)
	if err != nil {
		t.Fatal(err)
	}
	if view.Value != "5m" || view.Source != config.KindDatabase {
		t.Fatalf("after: %+v", view)
	}
	if stored, _ := r.db.GetKV(ctx, "settings.poke-interval"); stored != "5m" {
		t.Errorf("stored %q", stored)
	}
	if r.service.Config().PokeInterval.String() != "5m0s" || len(r.applied) != 1 || r.applied[0].PokeInterval.String() != "5m0s" {
		t.Errorf("not applied live: %v", r.applied)
	}
	page, _ := r.journal.List(ctx, journal.Query{})
	if len(page.Entries) == 0 || !strings.Contains(page.Entries[0].Summary, "Poke interval 15m → 5m") {
		t.Errorf("journal %+v", page.Entries)
	}
	if _, err := r.service.UpdateSetting(ctx, "poke-interval", "fifteen", false); !errors.Is(err, ErrInvalid) {
		t.Errorf("an invalid value: %v", err)
	}
	if stored, _ := r.db.GetKV(ctx, "settings.poke-interval"); stored != "5m" {
		t.Errorf("an invalid value replaced the stored one: %q", stored)
	}
	view, err = r.service.UpdateSetting(ctx, "poke-interval", "", true)
	if err != nil || view.Value != "15m" || view.Source != config.KindDefault {
		t.Errorf("clear: %+v %v", view, err)
	}
}

func TestEnvironmentLocksASetting(t *testing.T) {
	r := newRig(t, doctor.DemoAllGreen, []string{"--model=opus"}, map[string]string{"CROWSNEST_POKE_INTERVAL": "2m"})
	if view := r.setting(t, "poke-interval"); view.Locked == "" || view.Source != config.KindEnv || !strings.Contains(view.Locked, "CROWSNEST_POKE_INTERVAL") {
		t.Errorf("env: %+v", view)
	}
	if view := r.setting(t, "model"); view.Source != config.KindFlag || view.Locked == "" {
		t.Errorf("flag: %+v", view)
	}
	if _, err := r.service.UpdateSetting(context.Background(), "poke-interval", "5m", false); !errors.Is(err, ErrNotEditable) {
		t.Errorf("a locked setting was changed: %v", err)
	}
}

func TestSensitiveOptionsAreNotEditable(t *testing.T) {
	r := newRig(t, doctor.DemoAllGreen, nil, nil)
	for _, name := range []string{"no-auth", "bind", "session", "data-dir", "password", "username", "port"} {
		if _, err := r.service.UpdateSetting(context.Background(), name, "x", false); !errors.Is(err, ErrNotEditable) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestNotifyURLIsWriteOnly(t *testing.T) {
	r := newRig(t, doctor.DemoAllGreen, nil, nil)
	view, err := r.service.UpdateSetting(context.Background(), "notify-url", "https://ntfy.sh/secret-topic", false)
	if err != nil {
		t.Fatal(err)
	}
	if view.Value != "" || !view.IsSet {
		t.Errorf("write-only view %+v", view)
	}
	page, _ := r.journal.List(context.Background(), journal.Query{})
	for _, entry := range page.Entries {
		if strings.Contains(entry.Summary, "secret-topic") {
			t.Errorf("the journal carries the notify URL: %q", entry.Summary)
		}
	}
}

func TestEngineChangeKicksTheOrchestrator(t *testing.T) {
	r := newRig(t, doctor.DemoBoth, nil, nil)
	if _, err := r.service.UpdateSetting(context.Background(), "engine", "codex", false); err != nil {
		t.Fatal(err)
	}
	if r.orch.kicks != 1 || r.service.Selection(context.Background()).Orchestrator != "codex" {
		t.Errorf("kicks %d, selection %+v", r.orch.kicks, r.service.Selection(context.Background()))
	}
}

func TestOnboardingAndGate(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, doctor.DemoAllGreen, nil, nil)
	if r.service.OnboardingPending(ctx) || r.service.Gate(ctx) != "" {
		t.Fatal("an instance without the row is not on the checklist and may start")
	}
	if err := MarkOnboardingPending(ctx, r.db); err != nil {
		t.Fatal(err)
	}
	if reason := r.service.Gate(ctx); !strings.Contains(reason, "checklist") {
		t.Fatalf("gate %q", reason)
	}
	view := r.service.Onboarding(ctx, false)
	if !view.Pending || view.ChooseEngine || view.Report.Selection.Orchestrator != "claude" {
		t.Fatalf("view %+v", view)
	}
	if err := r.service.StartOnboarding(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if r.service.onboardingState(ctx) != OnboardingDone || r.service.Gate(ctx) != "" || r.orch.kicks != 1 {
		t.Fatalf("after start: state %q gate %q kicks %d", r.service.onboardingState(ctx), r.service.Gate(ctx), r.orch.kicks)
	}
}

func TestOnboardingAsksForEngineWhenBothExist(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, doctor.DemoBoth, nil, nil)
	_ = MarkOnboardingPending(ctx, r.db)
	if !r.service.Onboarding(ctx, false).ChooseEngine {
		t.Fatal("both agents usable: the checklist should ask")
	}
	if err := r.service.StartOnboarding(ctx, "codex"); err != nil {
		t.Fatal(err)
	}
	if stored, _ := r.db.GetKV(ctx, "settings.engine"); stored != "codex" {
		t.Errorf("engine choice not saved: %q", stored)
	}
	if gaps := r.service.Onboarding(ctx, true).CodexGaps; len(gaps) == 0 {
		t.Error("codex mode must list its gaps")
	}
}

func TestGateWaitsForHerdrAndAgents(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, doctor.DemoHerdrMissing, nil, nil)
	if reason := r.service.Gate(ctx); !strings.Contains(reason, "herdr") {
		t.Fatalf("gate %q", reason)
	}
	r.service.SetEnv(doctor.DemoEnv(doctor.DemoNoAgent))
	if reason := r.service.Gate(ctx); !strings.Contains(reason, "No agent") {
		t.Fatalf("gate %q", reason)
	}
	_ = MarkOnboardingPending(ctx, r.db)
	if err := r.service.StartOnboarding(ctx, ""); !errors.Is(err, ErrNoAgent) {
		t.Fatalf("start with no agent: %v", err)
	}
	r.service.SetEnv(doctor.DemoEnv(doctor.DemoCodexOnly))
	if err := r.service.StartOnboarding(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if reason := r.service.Gate(ctx); reason != "" {
		t.Fatalf("codex only should start: %q", reason)
	}
}

func TestLiveViewAndLogRing(t *testing.T) {
	ring := NewLogRing("/home/u")
	logger := slog.New(ring.Handler(slog.DiscardHandler))
	logger.Info("ignored")
	logger.Warn("herdr is not answering", "path", "/home/u/x", "token", "cnt_"+strings.Repeat("a", 40))
	lines := ring.Lines()
	if len(lines) != 1 || !strings.Contains(lines[0].Message, "~/x") || strings.Contains(lines[0].Message, strings.Repeat("a", 40)) {
		t.Fatalf("ring %+v", lines)
	}
	r := newRig(t, doctor.DemoAllGreen, nil, nil)
	r.service.opts.Logs = ring
	r.service.RecordHook("Stop")
	live := r.service.Live(context.Background())
	if !live.Running || live.Hooks["Stop"] == "" || len(live.LastErrors) != 1 || live.Orchestrator != "waiting" {
		t.Fatalf("live %+v", live)
	}
}
