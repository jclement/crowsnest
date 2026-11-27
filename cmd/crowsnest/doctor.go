// `crowsnest doctor`: the read-only check list (internal/doctor), as text on a terminal, plain
// when piped, or --json. Exit 0 when nothing failed (warnings included), 1 when something did,
// 2 for a usage error. Against a running instance it also asks that instance, over the agent
// socket, for its live view.
//
// loadConfig is here too: the config as serve resolves it, including the settings saved in the
// app (read-only from the database), so doctor reports the values serve would use.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/jclement/crowsnest/internal/agentapi"
	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/doctor"
	"github.com/jclement/crowsnest/internal/store"
	"github.com/jclement/crowsnest/internal/tailscale"
)

// settingsPrefix is the kv prefix of settings saved in the app: settings.<option name>.
const settingsPrefix = "settings."

// doctorTimeout bounds the whole run without --deep; --deep adds the agents' round trips.
const doctorTimeout = 45 * time.Second

func newDoctorCommand() *cobra.Command {
	var asJSON, deep bool
	command := &cobra.Command{
		Use:   "doctor",
		Short: "Check this machine can run crowsnest, and say how to fix what is missing",
		Long: "Checks herdr, the agents (Claude Code, Codex) and what crowsnest needs from them, the\n" +
			"machine, optional features and where each setting comes from. Read-only: it starts\n" +
			"nothing and writes nothing. --deep also makes one tiny real call per agent, which\n" +
			"costs a few tokens. Exit 0 when nothing failed, 1 when something did.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDoctor(cmd, asJSON, deep)
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "print the report as JSON")
	command.Flags().BoolVar(&deep, "deep", false, "also make one tiny real call per agent (costs a few tokens) and check the ElevenLabs key")
	return command
}

func runDoctor(cmd *cobra.Command, asJSON, deep bool) error {
	timeout := doctorTimeout
	if deep {
		timeout += 4 * time.Minute
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()
	cfg, loadErr := loadConfig(ctx, cmd.Flags())
	inputs := doctor.Inputs{Config: cfg, LoadErr: loadErr, Version: version, Deep: deep}
	if loadErr == nil {
		inputs.AgentSocket = agentSocketPath(cfg)
	}
	report := doctor.Run(ctx, inputs)
	if loadErr == nil {
		report.Live = mergeLive(report.Live, askLive(ctx, inputs.AgentSocket))
	}
	out := cmd.OutOrStdout()
	if asJSON {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return err
		}
	} else if err := doctor.Write(out, report, doctor.Style{Color: stdoutIsTerminal()}); err != nil {
		return err
	}
	if report.Verdict == doctor.StatusFail {
		return exitCodeError{code: 1}
	}
	return nil
}

// loadConfig is config.Load with the settings saved in the app layered in (DESIGN.md 3): a
// first load finds the database (its path never comes from the database), then the saved
// settings are read read-only and the config is loaded again with them. The startup refusals
// are applied last.
func loadConfig(ctx context.Context, flags *pflag.FlagSet) (config.Config, error) {
	var once sync.Once
	var tailnetName string
	detect := func() string { // at most once per start: Load runs twice when settings are saved
		once.Do(func() { tailnetName = tailscale.Status(ctx, tailscale.CLI).DNSName })
		return tailnetName
	}
	cfg, err := config.Load(flags, config.Options{TailnetName: detect})
	if err != nil {
		return config.Config{}, err
	}
	if !cfg.Demo {
		stored, err := store.ReadKVPrefix(ctx, cfg.DatabasePath(), settingsPrefix)
		if err != nil {
			return config.Config{}, fmt.Errorf("reading the settings saved in the app: %w", err)
		}
		if len(stored) > 0 {
			if cfg, err = config.Load(flags, config.Options{Stored: stored, TailnetName: detect}); err != nil {
				return config.Config{}, fmt.Errorf("%w (a value saved in the app's Settings; clear it there or override it with its flag or variable)", err)
			}
		}
	}
	if err := cfg.CheckStartup(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// askLive asks a running instance for its own view over the agent socket; nil when none answers.
func askLive(ctx context.Context, socket string) *doctor.Live {
	if _, err := os.Stat(socket); err != nil {
		return nil
	}
	liveCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var live doctor.Live
	if err := agentapi.NewClient(socket, "").Live(liveCtx, &live); err != nil {
		return nil
	}
	return &live
}

// mergeLive combines what the health endpoint said (port check) with the socket's fuller view.
func mergeLive(fromHealth, fromSocket *doctor.Live) *doctor.Live {
	switch {
	case fromSocket == nil:
		return fromHealth
	case fromHealth != nil && fromSocket.UpdateLatest == "":
		fromSocket.UpdateLatest = fromHealth.UpdateLatest
	}
	fromSocket.Running = true
	return fromSocket
}
