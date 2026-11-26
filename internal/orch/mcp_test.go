package orch

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"testing"

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
