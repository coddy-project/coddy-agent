//go:build swarm

package swarm

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/shareguard"
	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

// The audit counters of the relay (docs/plans/remote-model-provider-phase3.md, 6): in-memory, per client label, node and outcome, for the
// three shared-model routes only, labels only: no token, no digest of a token, no request id, no content, and no alias (the relay does not
// parse bodies). They reset with the relay, which every settings save rebuilds.

// relayStatsMaxRows bounds the rows: a pairing-token holder can register and expire names, so past the cap a new node folds into "-".
const relayStatsMaxRows = 1024

// The client labels that are not an entry's name, and the outcomes.
const (
	statClientFull    = "full"
	statClientUnknown = "unknown"

	statOK        = "ok"         // the node's answer started
	statScope     = "scope"      // refused by the allowlist or the route table
	statLimit     = "limit"      // the client's slot or window
	statNodeError = "node_error" // the relay's own 502 or 504
	statGone      = "gone"       // the client went away
	statAuth      = "auth"       // a token the gate refused on a shared route
	statNoNode    = "-"
)

type relayCounters struct{ c *shareguard.Counters }

func newRelayCounters(now func() time.Time) *relayCounters {
	fold := func(k shareguard.Key) shareguard.Key { k[1] = statNoNode; return k }
	return &relayCounters{c: shareguard.NewCounters(now, relayStatsMaxRows, fold)}
}

func (r *relayCounters) add(client, node, outcome string, took time.Duration) {
	if r == nil {
		return
	}
	r.c.Add(shareguard.Key{client, node, outcome}, shareguard.Delta{Calls: 1, DurationMS: took.Milliseconds()})
}

type relayStatRow struct {
	Client        string `json:"client"`
	Node          string `json:"node"`
	Outcome       string `json:"outcome"`
	Calls         int64  `json:"calls"`
	DurationMS    int64  `json:"duration_ms"`
	MaxDurationMS int64  `json:"max_duration_ms"`
}

type relayStatsDoc struct {
	Since string         `json:"since"`
	Rows  []relayStatRow `json:"rows"`
}

func (r *relayCounters) snapshot() relayStatsDoc {
	snap := r.c.Snapshot()
	doc := relayStatsDoc{Since: snap.Since.UTC().Format(time.RFC3339), Rows: []relayStatRow{}}
	for _, row := range snap.Rows {
		doc.Rows = append(doc.Rows, relayStatRow{
			Client: row.Key[0], Node: row.Key[1], Outcome: row.Key[2],
			Calls: row.Calls, DurationMS: row.DurationMS, MaxDurationMS: row.MaxDurationMS,
		})
	}
	return doc
}

// nodeLabel is the first hop's name when this relay knows it (registered) or the client's list names it, else "-": a guessed name never
// makes a key of its own.
func (s *Server) nodeLabel(name string, p principal) string {
	if _, ok := s.registry.Node(name); ok {
		return name
	}
	if p.class == principalScoped {
		for _, entry := range p.client.Nodes {
			if first, _, _ := strings.Cut(entry, "/"); first == name {
				return name
			}
		}
	}
	return statNoNode
}

// clientLabel is the label of a principal in the counters.
func clientLabel(p principal) string {
	switch p.class {
	case principalScoped:
		return p.client.Name
	case principalFull:
		return statClientFull
	}
	return statClientUnknown
}

// sharedRequest reports whether an incoming request is a call of one of the three shared-model routes through a node mount (any hop depth),
// read the way the mount reads it. The gate uses it to count a refused token: only a shared route is counted.
func sharedRequest(r *http.Request) bool {
	escaped := r.URL.EscapedPath()
	if !strings.HasPrefix(escaped, swarmdto.MountPath) {
		return false
	}
	after := strings.TrimPrefix(escaped, swarmdto.MountPath)
	slash := strings.IndexByte(after, '/')
	if slash <= 0 {
		return false
	}
	name, err := url.PathUnescape(after[:slash])
	if err != nil {
		return false
	}
	_, route, ok := splitHops(name, after[slash:])
	return ok && sharedRoute(r.Method, route)
}
