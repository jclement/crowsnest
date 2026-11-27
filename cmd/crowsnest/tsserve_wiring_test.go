// The publisher's wiring: which loopback listener tailscale serve proxies to, and when the
// MagicDNS name is added to the host check (never against --no-tailscale-detect).

package main

import (
	"net"
	"strings"
	"testing"

	"github.com/jclement/crowsnest/internal/config"
	"github.com/jclement/crowsnest/internal/tsserve"
)

func TestLoopbackTarget(t *testing.T) {
	v4 := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2769}
	v6 := &net.TCPAddr{IP: net.ParseIP("::1"), Port: 2769}
	tailnet := &net.TCPAddr{IP: net.ParseIP("100.101.102.103"), Port: 2769}
	cases := []struct {
		addrs []net.Addr
		want  string
	}{
		{[]net.Addr{v6, v4}, "http://127.0.0.1:2769"},
		{[]net.Addr{tailnet, v6}, "http://[::1]:2769"},
		{[]net.Addr{&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 41234}}, "http://127.0.0.1:41234"},
	}
	for _, test := range cases {
		got, err := loopbackTarget(test.addrs)
		if err != nil || got != test.want {
			t.Errorf("%v: %q %v", test.addrs, got, err)
		}
		if err := tsserve.CheckArgs(tsserve.ServeArgs(443, got)); err != nil {
			t.Errorf("%q is not a target the allowlist takes: %v", got, err)
		}
	}
	if _, err := loopbackTarget([]net.Addr{tailnet}); err == nil || !strings.Contains(err.Error(), "no loopback") {
		t.Fatalf("tailnet only: %v", err)
	}
}

func TestAllowTailnetName(t *testing.T) {
	if reason := allowTailnetName(config.Config{}, nil, "q.t.ts.net"); reason == "" {
		t.Fatal("no server yet: should wait")
	}
	if reason := allowTailnetName(config.Config{NoTailscaleDetect: true}, nil, "q.t.ts.net"); reason == "" {
		t.Fatal("no server yet")
	}
}
