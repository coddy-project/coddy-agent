package swarm

import (
	"net/url"
	"sync"
)

// JoinStatus is one registration of this process into a parent relay, as a
// status surface reports it: no name, URL path or credential, only the relay's
// host and what the registration is.
type JoinStatus struct {
	// Relay is the host, and the port when the URL names one, of the parent
	// relay.
	Relay string
	// Kind is what this process registered as, Transport how the relay
	// reaches it.
	Kind, Transport string
	// Online is whether the last registration attempt succeeded.
	Online bool
}

// The swarm state of this process that status surfaces read: the join sets
// that run and the registry of the relay it runs, if any. Both are published
// by the code that owns them (StartJoins, the relay subsystem), so a reader in
// an untagged package needs nothing of the tagged relay.
var (
	statusMu   sync.Mutex
	joinSets   = map[*JoinSet]struct{}{}
	relayNodes func() []NodeInfo
)

func registerJoinSet(s *JoinSet) {
	statusMu.Lock()
	joinSets[s] = struct{}{}
	statusMu.Unlock()
}

func unregisterJoinSet(s *JoinSet) {
	statusMu.Lock()
	delete(joinSets, s)
	statusMu.Unlock()
}

// Joins reports the registrations of every join set this process runs.
func Joins() []JoinStatus {
	statusMu.Lock()
	sets := make([]*JoinSet, 0, len(joinSets))
	for s := range joinSets {
		sets = append(sets, s)
	}
	statusMu.Unlock()
	var out []JoinStatus
	for _, s := range sets {
		for _, c := range s.Clients() {
			// An agent registration that leaves its name to the relay of the
			// same process is not a registration of its own.
			if c.Yielding() {
				continue
			}
			out = append(out, JoinStatus{
				Relay: relayHost(c.opts.RelayURL), Kind: c.opts.Kind, Transport: c.Transport(), Online: c.Online(),
			})
		}
	}
	return out
}

// relayRegisters reports whether a running relay join set of this process
// registers name into relayURL.
func relayRegisters(relayURL, name string) bool {
	statusMu.Lock()
	defer statusMu.Unlock()
	for s := range joinSets {
		for _, c := range s.clients {
			if c.opts.Kind == KindRelay && c.opts.RelayURL == relayURL && c.opts.Name == name {
				return true
			}
		}
	}
	return false
}

// relayHost is the host[:port] of a relay URL, without the user information
// or the path a URL may carry.
func relayHost(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Host
	}
	return ""
}

// PublishRelay makes the registry of the relay this process runs readable to
// status surfaces through nodes, until withdraw runs.
func PublishRelay(nodes func() []NodeInfo) (withdraw func()) {
	statusMu.Lock()
	relayNodes = nodes
	statusMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			statusMu.Lock()
			relayNodes = nil
			statusMu.Unlock()
		})
	}
}

// RelayNodes reports the nodes the relay of this process knows, online or
// not, and whether the process runs a relay at all.
func RelayNodes() ([]NodeInfo, bool) {
	statusMu.Lock()
	nodes := relayNodes
	statusMu.Unlock()
	if nodes == nil {
		return nil, false
	}
	return nodes(), true
}
