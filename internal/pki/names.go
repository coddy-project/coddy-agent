package pki

import (
	"net"
	"net/url"
	"os"
	"strings"
)

// maxInterfaceIPs bounds the addresses of the machine's interfaces that go into a server certificate: a host with dozens of bridges
// and tunnels would otherwise reissue its certificate each time one came up.
const maxInterfaceIPs = 16

// Hostname is the machine's name for the subjects and the names of a certificate, "localhost" when it has none.
func Hostname() string {
	h, err := os.Hostname()
	if err != nil || strings.TrimSpace(h) == "" {
		return "localhost"
	}
	return strings.ToLower(strings.TrimSpace(h))
}

// InterfaceAddrs reads the addresses of the machine, for DefaultNames. A test passes its own.
type InterfaceAddrs func() ([]net.Addr, error)

// DefaultNames are the names a server certificate must carry so that a client reaching this machine by any usual address verifies it:
// the host name, `localhost`, the loopback addresses, the global unicast addresses of the machine's interfaces (link-local ones change
// and say nothing a client dials), the hosts of the given addresses (a bind address, an advertised URL, a remote's URL) and the
// extra names the operator listed. A wildcard address (0.0.0.0, ::) is no name and is skipped.
func DefaultNames(host string, addrs InterfaceAddrs, hosts []string, extra []string) []string {
	names := []string{host, "localhost", "127.0.0.1", "::1"}
	if addrs == nil {
		addrs = net.InterfaceAddrs
	}
	if list, err := addrs(); err == nil {
		n := 0
		for _, a := range list {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipn.IP
			if !ip.IsGlobalUnicast() || ip.IsLinkLocalUnicast() {
				continue
			}
			names = append(names, ip.String())
			if n++; n >= maxInterfaceIPs {
				break
			}
		}
	}
	for _, h := range hosts {
		if name := HostOf(h); name != "" {
			names = append(names, name)
		}
	}
	names = append(names, extra...)
	var out []string
	for _, n := range NormalizeNames(names) {
		if ip := net.ParseIP(n); ip != nil && ip.IsUnspecified() {
			continue
		}
		out = append(out, n)
	}
	return out
}

// HostOf is the host part of an address: a URL ("https://relay.example:12346/x"), "host:port", or a bare host or IP. It returns "" for
// an empty string.
func HostOf(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	if strings.Contains(addr, "://") {
		if u, err := url.Parse(addr); err == nil {
			return u.Hostname()
		}
		return ""
	}
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return strings.Trim(h, "[]")
	}
	return strings.Trim(addr, "[]")
}
