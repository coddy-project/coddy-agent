//go:build swarm

// Package swarm implements the relay: a process that other coddy nodes register
// into, that proxies each of them under its own mount, that merges their
// session lists into one, and that can itself register into another relay so a
// client attached to the outermost one can drive an agent only the innermost
// one can see.
//
// It deliberately stores nothing. The registry lives in memory and is rebuilt
// by the nodes themselves through heartbeats or, for a node that dialled out,
// by the connection it holds open.
package swarm

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/netx"
	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

// Errors a caller has to tell apart, because each maps to a different status.
var (
	// ErrNameTaken means a live lease holds that name and the caller did not
	// prove it owns it. A shared pairing token is not that proof: it authorises
	// joining the swarm, not claiming somebody else's identity in it.
	ErrNameTaken = errors.New("node name is held by a live lease")
	// ErrUnknownNode means no lease by that name exists.
	ErrUnknownNode = errors.New("unknown node")
	// ErrLeaseSecret means the presented secret does not match the lease.
	ErrLeaseSecret = errors.New("lease secret does not match")
)

// NodeTransport is how the relay reaches one node. Everything above it composes
// ordinary HTTP requests and never learns which transport is in play, which is
// what lets a node behind a firewall be driven exactly like a reachable one.
type NodeTransport interface {
	// RoundTripper carries a request to the node.
	RoundTripper() http.RoundTripper
	// TargetURL is the scheme and host an outgoing request is rewritten to.
	TargetURL() *url.URL
	// Alive reports whether the transport can still carry a request.
	Alive() bool
	// Close releases the transport.
	Close() error
}

// lease is one registration. The credential fields never leave this struct.
type lease struct {
	info      swarmdto.NodeInfo
	token     string
	secret    string
	advertise *url.URL
	transport NodeTransport
	expiresAt time.Time
	// pinned marks a lease the operator asserted in configuration. Nothing
	// refreshes it, so nothing may expire it either.
	pinned bool
	// dial carries the proxy and TLS settings for reaching this node, which
	// only an operator can supply: a node cannot ask the relay to trust a
	// certificate authority or route through a proxy of its choosing.
	dial netx.Options
}

// Node is an immutable snapshot handed to the proxy.
type Node struct {
	Info      swarmdto.NodeInfo
	Token     string
	Transport NodeTransport
}

// Registry tracks the live nodes of one relay.
type Registry struct {
	mu    sync.Mutex
	nodes map[string]*lease

	ttl   time.Duration
	grace time.Duration

	// egress decides which addresses a node may make this relay dial. Without
	// it, anyone holding a pairing token could point the relay at loopback or a
	// cloud metadata endpoint and read the answer through a mount.
	egress netx.EgressPolicy

	// now and newSecret are injectable so tests can drive time and identity
	// instead of sleeping and hoping.
	now       func() time.Time
	newSecret func() (string, error)
}

// NewRegistry returns an empty registry whose leases live for ttl without a
// refresh. A node stays listed as offline for one further ttl so an operator
// can see that it existed and went away, rather than watching it vanish.
func NewRegistry(ttl time.Duration) *Registry {
	if ttl <= 0 {
		ttl = 90 * time.Second
	}
	return &Registry{
		nodes:     map[string]*lease{},
		ttl:       ttl,
		grace:     ttl,
		now:       time.Now,
		newSecret: randomSecret,
	}
}

// SetEgressPolicy installs the policy applied to every advertised address.
func (r *Registry) SetEgressPolicy(p netx.EgressPolicy) {
	r.mu.Lock()
	r.egress = p
	r.mu.Unlock()
}

// VerifyLease reports whether secret proves ownership of name, without changing
// anything. The tunnel handshake needs this before it takes over a connection:
// answering first and checking afterwards hands an accept to a caller who has
// proved nothing.
func (r *Registry) VerifyLease(name, secret string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	l, ok := r.nodes[name]
	if !ok {
		return ErrUnknownNode
	}
	if subtle.ConstantTimeCompare([]byte(l.secret), []byte(secret)) != 1 {
		return ErrLeaseSecret
	}
	return nil
}

func randomSecret() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("swarm: lease secret entropy: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

// Register claims or refreshes a name.
//
// The first caller to claim a free name gets a secret back and is the only one
// who can refresh or replace that registration while it lives. Anyone else
// presenting the same pairing token is refused, so one shared credential cannot
// be used to take over a peer's identity, redirect its traffic, or read the
// prompts meant for it.
func (r *Registry) Register(req swarmdto.RegisterRequest) (swarmdto.RegisterResponse, error) {
	return r.RegisterWithDial(req, netx.Options{})
}

// RegisterWithDial is Register for a node the operator configured by hand, who
// may also have said how to reach it: through a proxy, or past a private
// certificate authority. A node that registers itself cannot supply those - it
// would be asking the relay to trust an authority of its choosing.
func (r *Registry) RegisterWithDial(req swarmdto.RegisterRequest, dial netx.Options) (swarmdto.RegisterResponse, error) {
	if err := req.Validate(); err != nil {
		return swarmdto.RegisterResponse{}, err
	}
	var advertise *url.URL
	var pinned []netip.Addr
	if req.Transport == swarmdto.TransportDirect {
		u, err := swarmdto.ValidateAdvertiseURL(req.AdvertiseURL)
		if err != nil {
			return swarmdto.RegisterResponse{}, err
		}
		advertise = u
		// A node reached through a proxy is resolved by that proxy, not here:
		// its name may not exist in this network at all. Configuring a proxy is
		// something only the operator can do, and it is that decision - not the
		// node's claim - that the relay is trusting.
		if strings.TrimSpace(dial.Proxy) == "" {
			r.mu.Lock()
			policy := r.egress
			r.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			addrs, rerr := policy.Resolve(ctx, u.Hostname())
			cancel()
			if rerr != nil {
				return swarmdto.RegisterResponse{}, fmt.Errorf("advertise url %q: %w", req.AdvertiseURL, rerr)
			}
			pinned = addrs
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.reapLocked()

	now := r.now()
	existing, held := r.nodes[req.Name]
	if held {
		if req.LeaseSecret == "" || subtle.ConstantTimeCompare([]byte(existing.secret), []byte(req.LeaseSecret)) != 1 {
			return swarmdto.RegisterResponse{}, ErrNameTaken
		}
		// A renewal from the owner. The generation moves so a proxy holding an
		// older snapshot can tell its transport is stale.
		existing.info.Generation++
		existing.info.Kind = req.Kind
		existing.info.Transport = req.Transport
		existing.info.InstanceUUID = req.InstanceUUID
		existing.info.Version = req.Version
		existing.info.Labels = req.Labels
		existing.info.LastSeen = now.UTC().Format(time.RFC3339)
		existing.advertise = advertise
		existing.info.URL = advertiseString(advertise)
		if req.Token != "" {
			existing.token = req.Token
		}
		switch req.Transport {
		case swarmdto.TransportDirect:
			// A node that switched back to being reachable no longer needs the
			// connection it used to hold.
			existing.closeTransportLocked()
			if dial != (netx.Options{}) {
				existing.dial = dial
			}
			t, terr := newDirectTransport(advertise, existing.dial, pinned)
			if terr != nil {
				return swarmdto.RegisterResponse{}, terr
			}
			existing.transport = t
			existing.expiresAt = now.Add(r.ttl)
		default:
			// A tunnel lease follows its connection, not a clock. Putting it
			// back on the clock here would let a dead tunnel read as online for
			// a whole TTL, and would advertise a node with no way to reach it.
			if existing.transport != nil && existing.transport.Alive() {
				existing.expiresAt = time.Time{}
			} else {
				// No live connection yet: the lease is held on the clock only
				// long enough for the node to dial in.
				existing.closeTransportLocked()
				existing.expiresAt = now.Add(r.ttl)
			}
		}
		return swarmdto.RegisterResponse{
			NodeID:      req.Name,
			LeaseSecret: existing.secret,
			TTLSeconds:  int(r.ttl / time.Second),
			Generation:  existing.info.Generation,
		}, nil
	}

	secret, err := r.newSecret()
	if err != nil {
		return swarmdto.RegisterResponse{}, err
	}
	l := &lease{
		info: swarmdto.NodeInfo{
			Name:         req.Name,
			Kind:         req.Kind,
			Transport:    req.Transport,
			URL:          advertiseString(advertise),
			InstanceUUID: req.InstanceUUID,
			Version:      req.Version,
			Labels:       req.Labels,
			LastSeen:     now.UTC().Format(time.RFC3339),
			Generation:   1,
		},
		token:     req.Token,
		secret:    secret,
		advertise: advertise,
		dial:      dial,
		expiresAt: now.Add(r.ttl),
	}
	if req.Transport == swarmdto.TransportDirect {
		t, terr := newDirectTransport(advertise, l.dial, pinned)
		if terr != nil {
			return swarmdto.RegisterResponse{}, terr
		}
		l.transport = t
	}
	r.nodes[req.Name] = l
	return swarmdto.RegisterResponse{
		NodeID:      req.Name,
		LeaseSecret: secret,
		TTLSeconds:  int(r.ttl / time.Second),
		Generation:  1,
	}, nil
}

// AttachTransport binds a dialled-out connection to an existing lease. The node
// proves ownership with the secret it got when it registered, so a stranger who
// merely reaches the relay cannot hijack the route to somebody else's agent.
func (r *Registry) AttachTransport(name, secret string, t NodeTransport) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reapLocked()

	l, ok := r.nodes[name]
	if !ok {
		return ErrUnknownNode
	}
	if subtle.ConstantTimeCompare([]byte(l.secret), []byte(secret)) != 1 {
		return ErrLeaseSecret
	}
	// Replacing a transport is how a reconnect lands. The old connection is
	// closed rather than left half-open, so requests in flight on it fail at
	// once instead of hanging until some timeout notices.
	l.closeTransportLocked()
	l.transport = t
	l.info.Transport = swarmdto.TransportTunnel
	l.info.URL = ""
	l.advertise = nil
	l.info.Generation++
	l.info.LastSeen = r.now().UTC().Format(time.RFC3339)
	// A held connection is itself the liveness signal, so the lease follows it
	// rather than a heartbeat clock.
	l.expiresAt = time.Time{}
	return nil
}

// DetachTransport drops a tunnel that has gone away. The lease survives briefly
// so the node shows as offline instead of silently disappearing mid-reconnect.
func (r *Registry) DetachTransport(name string, t NodeTransport) {
	r.mu.Lock()
	defer r.mu.Unlock()
	l, ok := r.nodes[name]
	if !ok || l.transport != t {
		return
	}
	l.closeTransportLocked()
	l.expiresAt = r.now()
}

// Node returns a snapshot for proxying.
func (r *Registry) Node(name string) (Node, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reapLocked()
	l, ok := r.nodes[name]
	if !ok {
		return Node{}, false
	}
	info := l.info
	info.Online = l.onlineAt(r.now())
	return Node{Info: info, Token: l.token, Transport: l.transport}, true
}

// List returns every known node, online first and then by name, as the public
// view that carries no credential.
func (r *Registry) List() []swarmdto.NodeInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reapLocked()
	now := r.now()
	out := make([]swarmdto.NodeInfo, 0, len(r.nodes))
	for _, l := range r.nodes {
		info := l.info
		info.Online = l.onlineAt(now)
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Online != out[j].Online {
			return out[i].Online
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Online returns the nodes an aggregated call should fan out to.
func (r *Registry) Online() []Node {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reapLocked()
	now := r.now()
	var out []Node
	for _, l := range r.nodes {
		if !l.onlineAt(now) {
			continue
		}
		info := l.info
		info.Online = true
		out = append(out, Node{Info: info, Token: l.token, Transport: l.transport})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Info.Name < out[j].Info.Name })
	return out
}

// Pin marks a lease as operator-asserted: nothing refreshes it, so it must not
// expire. This is how a node listed in the configuration differs from one that
// registered itself — the operator said it exists, and only the operator can
// say it no longer does.
func (r *Registry) Pin(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if l, ok := r.nodes[name]; ok {
		l.pinned = true
	}
}

// Delete removes a lease outright. This is the administrative takeover path for
// a name whose owner is gone but whose lease has not expired yet.
func (r *Registry) Delete(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	l, ok := r.nodes[name]
	if !ok {
		return false
	}
	l.closeTransportLocked()
	delete(r.nodes, name)
	return true
}

// Len reports how many leases are known, offline ones included.
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reapLocked()
	return len(r.nodes)
}

// reapLocked drops leases that went past the offline grace period.
func (r *Registry) reapLocked() {
	now := r.now()
	for name, l := range r.nodes {
		if l.pinned {
			continue
		}
		if l.expiresAt.IsZero() {
			// A tunnel lease: it lives as long as its connection does.
			if l.transport != nil && l.transport.Alive() {
				continue
			}
			l.closeTransportLocked()
			l.expiresAt = now
			continue
		}
		if now.After(l.expiresAt.Add(r.grace)) {
			l.closeTransportLocked()
			delete(r.nodes, name)
		}
	}
}

func (l *lease) onlineAt(now time.Time) bool {
	if l.pinned {
		return true
	}
	if l.expiresAt.IsZero() {
		return l.transport != nil && l.transport.Alive()
	}
	return now.Before(l.expiresAt)
}

func (l *lease) closeTransportLocked() {
	if l.transport != nil {
		_ = l.transport.Close()
		l.transport = nil
	}
}

func advertiseString(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.String()
}
