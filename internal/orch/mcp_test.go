package orch

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/jclement/crowsnest/internal/agentid"
	"github.com/jclement/crowsnest/internal/herdr/fake"
)

// With an agent socket and the binary's path, the orchestrator gets CROWSNEST_SOCKET in its
// pane, an mcp.json naming `crowsnest mcp`, settings that pre-allow its tools, and --mcp-config.
func TestStartWiresTheAgentSocketAndMCP(t *testing.T) {
	f := fake.New()
	rig := newRig(t, f, func(o *Options) {
		o.Executable, o.AgentSocket = "/opt/crowsnest/bin/crowsnest", "/run/crowsnest/test.sock"
	})
	status, err := rig.orch.Ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if env := f.PaneEnv(status.PaneID); env["CROWSNEST_SOCKET"] != "/run/crowsnest/test.sock" {
		t.Fatalf("pane env %v", env)
	}
	args := f.Starts()[0].Args
	var mcpConfig struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	readJSON(t, argValue(args, "--mcp-config"), &mcpConfig)
	server := mcpConfig.MCPServers["crowsnest"]
	if server.Command != "/opt/crowsnest/bin/crowsnest" || !slices.Equal(server.Args, []string{"mcp"}) || server.Env["CROWSNEST_SOCKET"] != "/run/crowsnest/test.sock" {
		t.Fatalf("mcp.json %+v", mcpConfig)
	}
	var settings struct {
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	readJSON(t, argValue(args, "--settings"), &settings)
	if !slices.Equal(settings.Permissions.Allow, []string{MCPAllowRule}) {
		t.Fatalf("settings %+v", settings)
	}
	info, _ := os.Stat(argValue(args, "--mcp-config"))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mcp.json mode %v", info.Mode().Perm())
	}
}

// TestStartHandsTheOrchestratorItsTokens is R1b N2/N7 on the orchestrator's side: its pane's
// environment and its MCP server's each carry a token the registry maps to its pane, and a
// restart replaces the MCP one.
func TestStartHandsTheOrchestratorItsTokens(t *testing.T) {
	f := fake.New()
	var registry *agentid.Registry
	rig := newRig(t, f, func(o *Options) {
		o.Executable, o.AgentSocket = "/opt/crowsnest/bin/crowsnest", "/run/crowsnest/test.sock"
		registry = agentid.New(o.KV.(agentid.KV), nil)
		o.Identity = registry
	})
	ctx := context.Background()
	status, err := rig.orch.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	paneToken := f.PaneEnv(status.PaneID)[agentid.EnvToken]
	if binding, ok, _ := registry.Resolve(ctx, paneToken); !ok || binding.PaneID != status.PaneID || binding.Kind != agentid.KindPane {
		t.Fatalf("pane token %q resolves to %+v (%v)", paneToken, binding, ok)
	}
	mcpToken := mcpTokenOf(t, f.Starts()[0].Args)
	if binding, ok, _ := registry.Resolve(ctx, mcpToken); !ok || binding.PaneID != status.PaneID || binding.Kind != agentid.KindStart {
		t.Fatalf("MCP token resolves to %+v (%v)", binding, ok)
	}
	if _, err := rig.orch.Restart(ctx); err != nil {
		t.Fatal(err)
	}
	starts := f.Starts()
	if len(starts) < 2 {
		t.Fatalf("restart started Claude %d times in all", len(starts))
	}
	if _, ok, _ := registry.Resolve(ctx, mcpToken); ok {
		t.Fatal("the previous start's MCP token still resolves after a restart")
	}
	if _, ok, _ := registry.Resolve(ctx, mcpTokenOf(t, starts[len(starts)-1].Args)); !ok {
		t.Fatal("the restarted orchestrator's MCP token does not resolve")
	}
}

func mcpTokenOf(t *testing.T, args []string) string {
	t.Helper()
	var config struct {
		MCPServers map[string]struct {
			Env map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	readJSON(t, argValue(args, "--mcp-config"), &config)
	return config.MCPServers["crowsnest"].Env[agentid.EnvToken]
}

func TestStartWithoutASocketPassesNoMCPConfig(t *testing.T) {
	f := fake.New()
	rig := newRig(t, f)
	status, err := rig.orch.Ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(f.Starts()[0].Args, "--mcp-config") {
		t.Fatal("--mcp-config passed without an agent socket")
	}
	if _, ok := f.PaneEnv(status.PaneID)["CROWSNEST_SOCKET"]; ok {
		t.Fatal("CROWSNEST_SOCKET set without a socket")
	}
}

func readJSON(t *testing.T, path string, into any) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, into); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}
