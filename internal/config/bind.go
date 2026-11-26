// Bind resolution and the host-name set. A bind value is a keyword (localhost, tailnet), an
// interface name, an IP or ip:port; each resolves to one or more concrete listen addresses.
// The interface lister is injected so `tailnet` and interface names are testable anywhere.

package config

import (
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
)

// Interface is one network interface and its unicast addresses.
type Interface struct {
	Name  string
	Addrs []netip.Addr
}

// InterfaceLister returns this machine's interfaces.
type InterfaceLister func() ([]Interface, error)

// Tailscale's address ranges: the CGNAT block it hands out for IPv4, and its ULA prefix.
var (
	tailnetV4 = netip.MustParsePrefix("100.64.0.0/10")
	tailnetV6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

// IsTailnet reports whether addr is a Tailscale address.
func IsTailnet(addr netip.Addr) bool {
	addr = addr.Unmap()
	return tailnetV4.Contains(addr) || tailnetV6.Contains(addr)
}

// SystemInterfaces lists the real interfaces via the net package.
func SystemInterfaces() ([]Interface, error) {
	netInterfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []Interface
	for _, netInterface := range netInterfaces {
		addrs, err := netInterface.Addrs()
		if err != nil {
			continue // an interface that vanished mid-listing is not worth failing over
		}
		item := Interface{Name: netInterface.Name}
		for _, addr := range addrs {
			if prefix, err := netip.ParsePrefix(addr.String()); err == nil {
				item.Addrs = append(item.Addrs, prefix.Addr())
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// resolveBinds turns bind values into listeners, de-duplicated, in the order given.
func resolveBinds(binds []string, port int, interfaces []Interface) ([]Listener, error) {
	var out []Listener
	seen := map[netip.AddrPort]bool{}
	for _, bind := range binds {
		listeners, err := resolveBind(bind, port, interfaces)
		if err != nil {
			return nil, err
		}
		for _, listener := range listeners {
			if !seen[listener.Addr] {
				seen[listener.Addr] = true
				out = append(out, listener)
			}
		}
	}
	return out, nil
}

// resolveBind resolves one value. Order matters: keywords first, then ip:port, then a bare IP,
// then interface names, so an interface literally named "tailnet" cannot shadow the keyword.
func resolveBind(bind string, port int, interfaces []Interface) ([]Listener, error) {
	at := func(addr netip.Addr, optional bool) Listener {
		// port was checked to be 1..65535 in loadNetwork before binds are resolved.
		return Listener{Addr: netip.AddrPortFrom(addr, uint16(port)), Optional: optional, Source: bind} //nolint:gosec // range-checked
	}
	switch strings.ToLower(bind) {
	case "localhost":
		return []Listener{at(netip.MustParseAddr("127.0.0.1"), false), at(netip.IPv6Loopback(), true)}, nil
	case "tailnet":
		return resolveTailnet(bind, interfaces, at)
	}
	if addrPort, err := netip.ParseAddrPort(bind); err == nil {
		if addrPort.Port() == 0 {
			return nil, fmt.Errorf("--bind %q: port 0 is not allowed; name a port or leave it off to use --port", bind)
		}
		return []Listener{{Addr: netip.AddrPortFrom(addrPort.Addr().Unmap(), addrPort.Port()), Source: bind}}, nil
	}
	if addr, err := netip.ParseAddr(bind); err == nil {
		return []Listener{at(addr.Unmap(), false)}, nil
	}
	for _, item := range interfaces {
		if item.Name == bind {
			return resolveInterface(item, bind, at)
		}
	}
	return nil, fmt.Errorf("--bind %q is not localhost, tailnet, an interface on this machine, an IP or ip:port", bind)
}

// resolveTailnet binds the Tailscale address(es). With none, it refuses rather than falling back
// to all interfaces: "only on the tailnet" must never quietly become "everywhere".
func resolveTailnet(bind string, interfaces []Interface, at func(netip.Addr, bool) Listener) ([]Listener, error) {
	var out []Listener
	for _, item := range interfaces {
		for _, addr := range item.Addrs {
			if IsTailnet(addr) {
				out = append(out, at(addr.Unmap(), false))
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--bind tailnet: no Tailscale address (100.64.0.0/10 or fd7a:115c:a1e0::/48) on this machine; start Tailscale, or pass --bind localhost")
	}
	return out, nil
}

// resolveInterface binds every usable address on a named interface. IPv6 link-local addresses
// are skipped: they need a zone to bind and are not reachable the way a user means.
func resolveInterface(item Interface, bind string, at func(netip.Addr, bool) Listener) ([]Listener, error) {
	var out []Listener
	for _, addr := range item.Addrs {
		if addr.Is6() && addr.IsLinkLocalUnicast() {
			continue
		}
		out = append(out, at(addr.Unmap(), false))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--bind %q: interface has no usable address; pass an IP instead", bind)
	}
	return out, nil
}

// hostNames builds the set of names the Host header may carry (DESIGN.md 4.3): the loopback
// names, every bound IP (every interface IP when bound to the unspecified address), this
// machine's hostname in its full, short and .local forms, and the --allowed-host extras.
func hostNames(listeners []Listener, interfaces []Interface, hostname string, extra []string) []string {
	names := []string{"localhost", "127.0.0.1", "::1"}
	add := func(name string) {
		name = strings.ToLower(strings.TrimSuffix(name, "."))
		if name != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	for _, listener := range listeners {
		addr := listener.Addr.Addr()
		if !addr.IsUnspecified() {
			add(addr.String())
			continue
		}
		for _, item := range interfaces {
			for _, interfaceAddr := range item.Addrs {
				add(interfaceAddr.Unmap().String())
			}
		}
	}
	if hostname != "" {
		short, _, _ := strings.Cut(hostname, ".")
		add(hostname)
		add(short)
		add(short + ".local")
	}
	for _, name := range extra {
		add(name)
	}
	return names
}

// isOpenAddr reports whether a listen address is reachable beyond this machine and the
// tailnet: anything that is not loopback and not a Tailscale address, the unspecified
// address included.
func isOpenAddr(addr netip.Addr) bool {
	return !addr.IsLoopback() && !IsTailnet(addr)
}
