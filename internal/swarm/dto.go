package swarm

import (
	"fmt"
	"strings"
)

// Kinds of swarm node. A relay is a node like any other from its parent's point
// of view, which is what lets a chain compose without a special case per depth.
const (
	KindAgent = "agent"
	KindRelay = "relay"
)

// Transports a relay can use to reach a node.
const (
	// TransportDirect dials the node's advertised URL. Requires the node to be
	// reachable from the relay.
	TransportDirect = "direct"
	// TransportTunnel reuses a connection the node opened to the relay, which
	// is the only option when the node sits in a contour that accepts no
	// inbound connections.
	TransportTunnel = "tunnel"
)

// MountPath is the route prefix under which a relay proxies one node.
const MountPath = "/swarm/nodes/"

// RegisterRequest is the document a node sends to join a relay. The same
// document renews the lease, so a heartbeat is just a repeat carrying the
// secret the relay minted the first time.
type RegisterRequest struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Transport    string `json:"transport"`
	AdvertiseURL string `json:"advertise_url,omitempty"`
	InstanceUUID string `json:"instance_uuid"`
	// Token is the node's own bearer credential, which the relay presents when
	// it dials back. It never leaves the registry.
	Token string `json:"token,omitempty"`
	// LeaseSecret proves ownership of an existing name. Empty on a first claim.
	LeaseSecret string            `json:"lease_secret,omitempty"`
	Version     string            `json:"version,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// Validate rejects a registration the relay could not act on.
func (r RegisterRequest) Validate() error {
	if err := ValidateNodeName(r.Name); err != nil {
		return err
	}
	switch r.Kind {
	case KindAgent, KindRelay:
	default:
		return fmt.Errorf("unknown node kind %q", r.Kind)
	}
	if strings.TrimSpace(r.InstanceUUID) == "" {
		return fmt.Errorf("instance uuid is required")
	}
	switch r.Transport {
	case TransportDirect:
		if _, err := ValidateAdvertiseURL(r.AdvertiseURL); err != nil {
			return err
		}
	case TransportTunnel:
		// The connection is the address, so an advertised URL would be noise
		// at best and a contradiction at worst.
	default:
		return fmt.Errorf("unknown transport %q", r.Transport)
	}
	return nil
}

// RegisterResponse tells the node what it joined and how to stay joined.
type RegisterResponse struct {
	NodeID      string `json:"node_id"`
	LeaseSecret string `json:"lease_secret"`
	TTLSeconds  int    `json:"ttl_seconds"`
	Generation  uint64 `json:"generation"`
}

// NodeInfo is the public view of a registry entry. It carries no credential
// field at all, so no future edit can leak one by forgetting to redact.
type NodeInfo struct {
	Name         string            `json:"name"`
	Kind         string            `json:"kind"`
	Transport    string            `json:"transport"`
	URL          string            `json:"url,omitempty"`
	InstanceUUID string            `json:"instance_uuid"`
	Version      string            `json:"version,omitempty"`
	Online       bool              `json:"online"`
	LastSeen     string            `json:"last_seen,omitempty"`
	Generation   uint64            `json:"generation"`
	Labels       map[string]string `json:"labels,omitempty"`
}

// Info is what a relay answers on /swarm/info. It is public so a client can
// tell a relay from a plain agent before it holds any credential, and it
// reports whether the registry is still filling up after a restart, so
// "nothing registered" is distinguishable from "just started".
type Info struct {
	Swarm           bool   `json:"swarm"`
	Name            string `json:"name"`
	UUID            string `json:"uuid"`
	Version         string `json:"version"`
	NodeCount       int    `json:"node_count"`
	StartedAt       string `json:"started_at"`
	RegistryWarming bool   `json:"registry_warming"`
}

// SessionRef addresses one session inside a swarm: the path of node names from
// the relay a client is attached to down to the agent that owns the session,
// plus the id that agent knows it by.
//
// Session ids are chosen per node and can collide across nodes, so the pair is
// the only safe identity for an aggregated surface.
type SessionRef struct {
	NodePath []string
	ID       string
}

// ParseSessionRef reads a reference written as "node/node/.../id".
//
// A node name can never contain the separator, so the last segment is always
// the session id and everything before it is the path. That rule is what keeps
// a reference unambiguous however deep the chain gets.
func ParseSessionRef(s string) (SessionRef, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return SessionRef{}, fmt.Errorf("session reference is empty")
	}
	parts := strings.Split(trimmed, "/")
	id := parts[len(parts)-1]
	path := parts[:len(parts)-1]
	if err := ValidateSessionID(id); err != nil {
		return SessionRef{}, err
	}
	for _, node := range path {
		if err := ValidateNodeName(node); err != nil {
			return SessionRef{}, fmt.Errorf("session reference %q: %w", s, err)
		}
	}
	if len(path) == 0 {
		return SessionRef{ID: id}, nil
	}
	return SessionRef{NodePath: path, ID: id}, nil
}

// String renders the reference back into its wire form.
func (r SessionRef) String() string {
	if len(r.NodePath) == 0 {
		return r.ID
	}
	return strings.Join(r.NodePath, "/") + "/" + r.ID
}

// MountPrefix is the path a client prepends to every request for this session.
// Each hop contributes its own mount segment, so a two-hop reference yields a
// prefix that walks through the intermediate relay rather than collapsing it.
func (r SessionRef) MountPrefix() string {
	return NodePathPrefix(r.NodePath)
}

// NodePathPrefix builds the mount prefix that reaches the last node of path.
func NodePathPrefix(path []string) string {
	if len(path) == 0 {
		return ""
	}
	var b strings.Builder
	for _, node := range path {
		b.WriteString(MountPath)
		b.WriteString(node)
	}
	return b.String()
}

// ValidateSessionID mirrors the folder-safe alphabet a session id must use on
// the node that owns it. The swarm never invents ids; it only carries them.
func ValidateSessionID(id string) error {
	if id == "" {
		return fmt.Errorf("session id is required")
	}
	if len(id) > 256 {
		return fmt.Errorf("session id is longer than 256 characters")
	}
	if !nodeNamePattern.MatchString(id) {
		return fmt.Errorf("session id %q: only letters, digits, underscore and hyphen are allowed", id)
	}
	return nil
}
