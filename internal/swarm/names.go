// Package swarm holds what a swarm relay and the nodes joining it must agree
// on: the registration documents, the rules for a node name, and the client
// that keeps a node registered. It carries no build tag so the HTTP server can
// join a relay without dragging the relay implementation into its build.
package swarm

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// MaxNodeNameLen bounds a node name. The name is a URL path segment on every
// hop of a chain, so it stays short enough that a deep path is still a sane URL.
const MaxNodeNameLen = 64

// nodeNamePattern is deliberately the alphabet session ids already use: letters,
// digits, underscore and hyphen. Anything that could act as a path separator,
// a relative segment, or an escape is outside it.
var nodeNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// reservedNodeNames are the path segments the relay's own API occupies. A node
// claiming one of them would shadow a route rather than a peer.
var reservedNodeNames = map[string]bool{
	"nodes":    true,
	"info":     true,
	"register": true,
	"tunnel":   true,
	"sessions": true,
	"topology": true,
	"events":   true,
	"swarm":    true,
}

// ValidateNodeName reports whether name may address a node in a swarm.
func ValidateNodeName(name string) error {
	if name == "" {
		return fmt.Errorf("node name is required")
	}
	if len(name) > MaxNodeNameLen {
		return fmt.Errorf("node name is longer than %d characters", MaxNodeNameLen)
	}
	if !nodeNamePattern.MatchString(name) {
		return fmt.Errorf("node name %q: only letters, digits, underscore and hyphen are allowed", name)
	}
	if reservedNodeNames[strings.ToLower(name)] {
		return fmt.Errorf("node name %q is reserved by the swarm API", name)
	}
	return nil
}

// ValidateAdvertiseURL parses the address a relay would dial to reach a node
// and rejects everything that is not a bare origin. Credentials, a query, or a
// fragment in that position are either a mistake or an attempt to smuggle
// something past the proxy, and neither is worth supporting.
//
// Reachability of the host itself is a separate question the relay answers with
// its own policy, because that decision depends on where the relay is bound.
func ValidateAdvertiseURL(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("advertise url is required for the direct transport")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("advertise url %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("advertise url %q: scheme must be http or https", raw)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("advertise url %q: host is required", raw)
	}
	if u.User != nil {
		return nil, fmt.Errorf("advertise url %q: credentials are not allowed", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("advertise url %q: query and fragment are not allowed", raw)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u, nil
}
