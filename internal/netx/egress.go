package netx

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// EgressPolicy decides which addresses this process may dial on somebody else's
// say-so.
//
// A node tells a relay where to reach it, and the relay then dials that address
// holding a credential. Without a policy that turns any holder of a pairing
// token into a probe of the relay's own network - and of whatever the cloud
// serves on its metadata address.
type EgressPolicy struct {
	// AllowLoopback permits 127.0.0.0/8 and ::1. Sensible when the relay itself
	// is bound to loopback, which is the development case.
	AllowLoopback bool
	// AllowPrivate permits RFC1918 and unique-local addresses for every host.
	// Prefer naming hosts in AllowHosts: this opens the whole range.
	AllowPrivate bool
	// AllowHosts lists host names whose range rules are relaxed. It is scoped to
	// those names and never lifts the absolute refusals below.
	AllowHosts []string
}

// metadataAddrs are the link-local endpoints cloud providers answer credentials
// on. They are never allowed, whatever else is.
var metadataAddrs = []netip.Addr{
	netip.MustParseAddr("169.254.169.254"),
	netip.MustParseAddr("fd00:ec2::254"),
}

func (p EgressPolicy) allows(host string) bool {
	for _, h := range p.AllowHosts {
		if strings.EqualFold(strings.TrimSpace(h), host) {
			return true
		}
	}
	return false
}

// CheckAddr reports whether one resolved address may be dialled.
func (p EgressPolicy) CheckAddr(addr netip.Addr) error {
	addr = addr.Unmap()
	for _, meta := range metadataAddrs {
		if addr == meta {
			return fmt.Errorf("address %s is a cloud metadata endpoint", addr)
		}
	}
	switch {
	case addr.IsLoopback():
		if !p.AllowLoopback {
			return fmt.Errorf("address %s is loopback", addr)
		}
	case addr.IsLinkLocalUnicast(), addr.IsLinkLocalMulticast():
		return fmt.Errorf("address %s is link-local", addr)
	case addr.IsUnspecified():
		return fmt.Errorf("address %s is unspecified", addr)
	case addr.IsMulticast():
		return fmt.Errorf("address %s is multicast", addr)
	case addr.IsPrivate(), isSharedAddressSpace(addr):
		if !p.AllowPrivate {
			return fmt.Errorf("address %s is in a private range", addr)
		}
	}
	return nil
}

// Resolve looks host up and returns the addresses that pass the policy.
//
// The addresses are returned so a caller can dial exactly them. Re-resolving at
// dial time would reopen the gap this closes: a name that answers with a public
// address while it is being checked and a private one a moment later.
func (p EgressPolicy) Resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil, fmt.Errorf("no host to resolve")
	}
	// Naming a host relaxes the range rules for that host alone. It never lifts
	// the absolute refusals: an operator allowing one internal name should not
	// thereby open every private range, nor the metadata endpoint.
	relaxed := p
	if p.allows(host) {
		relaxed.AllowLoopback = true
		relaxed.AllowPrivate = true
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if cerr := relaxed.CheckAddr(addr); cerr != nil {
			return nil, cerr
		}
		return []netip.Addr{addr}, nil
	}
	addrs, err := resolveAll(ctx, host)
	if err != nil {
		return nil, err
	}
	out := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		if cerr := relaxed.CheckAddr(a); cerr != nil {
			return nil, fmt.Errorf("host %q resolves to a refused address: %w", host, cerr)
		}
		out = append(out, a)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("host %q resolved to nothing usable", host)
	}
	return out, nil
}

func resolveAll(ctx context.Context, host string) ([]netip.Addr, error) {
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("resolve %q: no addresses", host)
	}
	return ips, nil
}

// PinnedDialer returns a dial function that only ever connects to addrs,
// whatever the name resolves to by the time it is called.
//
// This is what makes the check above worth doing: validating a name and then
// dialling it again leaves room for the answer to change in between.
func PinnedDialer(addrs []netip.Addr, base func(ctx context.Context, network, addr string) (net.Conn, error)) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		_, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, a := range addrs {
			target := net.JoinHostPort(a.String(), port)
			conn, derr := base(ctx, network, target)
			if derr == nil {
				return conn, nil
			}
			lastErr = derr
		}
		if lastErr == nil {
			lastErr = fmt.Errorf("no pinned address to dial for %s", address)
		}
		return nil, lastErr
	}
}

// sharedAddressSpace is RFC 6598's 100.64.0.0/10. Go does not count it as
// private, but it is carrier-grade NAT and the range several mesh VPNs hand out,
// so a node advertising one is asking the relay to dial somewhere it should not
// reach on a stranger's word.
var sharedAddressSpace = netip.MustParsePrefix("100.64.0.0/10")

func isSharedAddressSpace(addr netip.Addr) bool {
	return addr.Is4() && sharedAddressSpace.Contains(addr)
}
