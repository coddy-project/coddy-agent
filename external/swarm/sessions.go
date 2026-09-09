//go:build swarm

package swarm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

// Bounds on one aggregated answer.
const (
	defaultSessionLimit = 50
	maxSessionLimit     = 200
	maxFanoutConcurrent = 8
	// swarmMaxHops is the hard bound on chain depth. The loop guard catches a
	// cycle; this catches a chain that is merely too long to be sane. It is the
	// configuration's constant rather than a second copy, so a mount and a
	// fan-out can never disagree about how far the swarm reaches.
	swarmMaxHops = config.SwarmMaxHops
)

// SwarmPathHeader carries the relays a request has already passed through, so a
// chain that loops back on itself notices instead of spinning.
//
// It is stripped from every inbound request and rebuilt per hop. An authorised
// client could still forge one, but the only thing that buys is a refusal of
// its own request: the header can stop a fan-out, never multiply one.
const SwarmPathHeader = "X-Coddy-Swarm-Path"

// sessionRow is one aggregated session.
//
// It carries both an identity and a route, which are different things. The
// identity is the agent that owns the session plus the id that agent knows;
// that pair survives a rename, a failover, or a relay restart. The route is
// merely how this relay could reach it just now, and in a ring there may be
// several.
type sessionRow struct {
	ID        string   `json:"id"`
	Title     string   `json:"title,omitempty"`
	UpdatedAt string   `json:"updatedAt,omitempty"`
	CWD       string   `json:"cwd,omitempty"`
	AgentUUID string   `json:"agent_uuid,omitempty"`
	NodePath  []string `json:"node_path"`
	NodeName  string   `json:"node_name"`
	NodeURL   string   `json:"node_url,omitempty"`
	NodeKind  string   `json:"node_kind,omitempty"`

	// Everything the node reported that this relay does not model itself, such
	// as activity flags or a subagent link, travels through untouched.
	Extra map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON is the other half of MarshalJSON.
//
// Without it a row loses everything this relay does not model itself the moment
// it crosses a hop: activity flags, permission state, a subagent link. The
// fields are flattened on the way out, so they have to be gathered on the way
// back in.
func (r *sessionRow) UnmarshalJSON(data []byte) error {
	type alias sessionRow
	var base alias
	if err := json.Unmarshal(data, &base); err != nil {
		return err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return err
	}
	known := map[string]bool{
		"id": true, "title": true, "updatedAt": true, "cwd": true,
		"agent_uuid": true, "node_path": true, "node_name": true,
		"node_url": true, "node_kind": true,
	}
	extra := map[string]json.RawMessage{}
	for k, v := range all {
		if !known[k] {
			extra[k] = v
		}
	}
	*r = sessionRow(base)
	if len(extra) > 0 {
		r.Extra = extra
	}
	return nil
}

// MarshalJSON flattens Extra beside the known fields so a client keeps seeing
// the node's own row shape.
func (r sessionRow) MarshalJSON() ([]byte, error) {
	type alias sessionRow
	base, err := json.Marshal(alias(r))
	if err != nil {
		return nil, err
	}
	if len(r.Extra) == 0 {
		return base, nil
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(base, &merged); err != nil {
		return nil, err
	}
	for k, v := range r.Extra {
		if _, taken := merged[k]; !taken {
			merged[k] = v
		}
	}
	return json.Marshal(merged)
}

// nodeReport is one node's contribution to an aggregated answer.
type nodeReport struct {
	rows    []sessionRow
	hasMore bool
	warning string
}

func (s *Server) registerSessionRoutes() {
	s.mux.HandleFunc("GET /swarm/sessions", s.handleSessions)
}

// handleSessions merges the session lists of every reachable node.
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := parseLimit(q.Get("limit"))
	search := strings.TrimSpace(q.Get("q"))
	only := strings.TrimSpace(q.Get("node"))

	path, err := s.hopPath(r)
	if err != nil {
		// A chain that came back to this relay is the normal end of a branch in
		// a ring: whoever asked has already seen everything below here, so it
		// is reported as such rather than as a warning - a warning on every
		// request in a healthy swarm is how operators learn to ignore warnings.
		//
		// A chain that is merely too long is a different matter. Something is
		// out there and cannot be reached, and saying nothing would make it
		// vanish silently.
		out := map[string]interface{}{
			"object":   "swarm.session_list",
			"sessions": []interface{}{},
			"warnings": []string{},
			"reason":   err.Error(),
		}
		if errors.Is(err, errChainLooped) {
			out["looped"] = true
		} else {
			out["warnings"] = []string{err.Error()}
		}
		writeJSON(w, http.StatusOK, out)
		return
	}

	nodes := s.registry.Online()
	// A node the relay still knows but cannot reach has to say so. Dropping it
	// silently makes "this machine is down" indistinguishable from "this
	// machine has no work", which is the more alarming of the two.
	offline := []string{}
	for _, info := range s.registry.List() {
		if info.Online {
			continue
		}
		if only != "" && info.Name != only {
			continue
		}
		offline = append(offline, fmt.Sprintf("%s: offline since %s", info.Name, info.LastSeen))
	}
	if only != "" {
		filtered := nodes[:0]
		for _, n := range nodes {
			if n.Info.Name == only {
				filtered = append(filtered, n)
			}
		}
		nodes = filtered
	}

	deadline := time.Duration(s.cfg.Swarm.EffectiveFanoutTimeoutSeconds()) * time.Second
	reports := s.fanOut(r.Context(), nodes, fanoutParams{
		limit:    limit,
		search:   search,
		query:    q,
		hopPath:  path,
		deadline: deadline,
	})

	var rows []sessionRow
	warnings := append([]string{}, offline...)
	perNode := map[string]bool{}
	for name, rep := range reports {
		rows = append(rows, rep.rows...)
		if rep.warning != "" {
			warnings = append(warnings, rep.warning)
		}
		perNode[name] = rep.hasMore
	}

	// A ring can deliver the same agent through more than one child. The rows
	// are the same sessions, so they collapse on identity; the route kept is
	// the shortest one, because that is the one a client should use.
	rows = dedupeByIdentity(rows)
	sortSessionRows(rows)

	truncated := false
	if len(rows) > limit {
		rows = rows[:limit]
		truncated = true
	}
	if rows == nil {
		rows = []sessionRow{}
	}
	sort.Strings(warnings)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"object":    "swarm.session_list",
		"sessions":  rows,
		"warnings":  warnings,
		"node_more": perNode,
		"hasMore":   truncated || anyTrue(perNode),
	})
}

type fanoutParams struct {
	limit    int
	search   string
	query    url.Values
	hopPath  []string
	deadline time.Duration
}

// fanOut asks every node for its page, in parallel but bounded.
func (s *Server) fanOut(ctx context.Context, nodes []Node, p fanoutParams) map[string]nodeReport {
	out := map[string]nodeReport{}
	if len(nodes) == 0 {
		return out
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxFanoutConcurrent)

	for _, node := range nodes {
		wg.Add(1)
		go func(node Node) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// One slow node must not hold the whole answer hostage; it degrades
			// into a warning instead.
			nodeCtx, cancel := context.WithTimeout(ctx, p.deadline)
			defer cancel()

			rep := s.askNode(nodeCtx, node, p)
			mu.Lock()
			out[node.Info.Name] = rep
			mu.Unlock()
		}(node)
	}
	wg.Wait()
	return out
}

// askNode fetches one node's page and labels it.
func (s *Server) askNode(ctx context.Context, node Node, p fanoutParams) nodeReport {
	if node.Transport == nil || !node.Transport.Alive() {
		return nodeReport{warning: fmt.Sprintf("%s: not reachable", node.Info.Name)}
	}
	target := node.Transport.TargetURL()
	if target == nil {
		return nodeReport{warning: fmt.Sprintf("%s: no usable transport", node.Info.Name)}
	}

	// A node whose own name or address answers the search matches as a whole,
	// so its sessions are fetched unfiltered rather than being narrowed by a
	// term that describes the node instead of the work.
	nodeMatches := p.search != "" && nodeMatchesSearch(node, p.search)
	search := p.search
	if nodeMatches {
		search = ""
	}

	isRelay := node.Info.Kind == swarmdto.KindRelay
	path := "/coddy/sessions"
	if isRelay {
		path = "/swarm/sessions"
	}

	q := url.Values{}
	// Every child is asked for at least what the caller wants. A shorter page
	// would silently drop rows that belonged in the merged answer.
	q.Set("limit", strconv.Itoa(p.limit))
	if search != "" {
		q.Set("q", search)
	}
	for _, pass := range []string{"include_activity", "include_scheduler", "include_subagents"} {
		if v := p.query.Get(pass); v != "" {
			q.Set(pass, v)
		}
	}

	base := strings.TrimRight(target.Path, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.Scheme+"://"+target.Host+base+path+"?"+q.Encode(), nil)
	if err != nil {
		return nodeReport{warning: fmt.Sprintf("%s: %v", node.Info.Name, err)}
	}
	if node.Token != "" {
		req.Header.Set("Authorization", "Bearer "+node.Token)
	}
	req.Header.Set("Accept", "application/json")
	if isRelay {
		req.Header.Set(SwarmPathHeader, strings.Join(p.hopPath, ","))
	}

	res, err := node.Transport.RoundTripper().RoundTrip(req)
	if err != nil {
		return nodeReport{warning: fmt.Sprintf("%s: %v", node.Info.Name, err)}
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode != http.StatusOK {
		return nodeReport{warning: fmt.Sprintf("%s: %s", node.Info.Name, res.Status)}
	}

	if isRelay {
		return relayReport(node, body)
	}
	return agentReport(node, body)
}

// agentReport turns one agent's own session list into labelled rows.
func agentReport(node Node, body []byte) nodeReport {
	var wrap struct {
		Sessions []map[string]json.RawMessage `json:"sessions"`
		HasMore  bool                         `json:"hasMore"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nodeReport{warning: fmt.Sprintf("%s: %v", node.Info.Name, err)}
	}
	rep := nodeReport{hasMore: wrap.HasMore}
	for _, raw := range wrap.Sessions {
		row := sessionRow{
			AgentUUID: node.Info.InstanceUUID,
			NodePath:  []string{node.Info.Name},
			NodeName:  node.Info.Name,
			NodeURL:   node.Info.URL,
			NodeKind:  node.Info.Kind,
			Extra:     map[string]json.RawMessage{},
		}
		for key, value := range raw {
			switch key {
			case "id":
				_ = json.Unmarshal(value, &row.ID)
			case "title":
				_ = json.Unmarshal(value, &row.Title)
			case "updatedAt":
				_ = json.Unmarshal(value, &row.UpdatedAt)
			case "cwd":
				_ = json.Unmarshal(value, &row.CWD)
			default:
				row.Extra[key] = value
			}
		}
		if row.ID == "" {
			continue
		}
		rep.rows = append(rep.rows, row)
	}
	return rep
}

// relayReport takes a child relay's already-aggregated answer and prefixes this
// hop onto every route, leaving each row's identity alone.
func relayReport(node Node, body []byte) nodeReport {
	var wrap struct {
		Sessions []sessionRow `json:"sessions"`
		Warnings []string     `json:"warnings"`
		HasMore  bool         `json:"hasMore"`
		Looped   bool         `json:"looped"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nodeReport{warning: fmt.Sprintf("%s: %v", node.Info.Name, err)}
	}
	if wrap.Looped {
		// This branch closes back on a relay already in the chain, so it holds
		// nothing new. Saying nothing is the correct answer.
		return nodeReport{}
	}
	rep := nodeReport{hasMore: wrap.HasMore}
	for _, row := range wrap.Sessions {
		if row.ID == "" {
			continue
		}
		row.NodePath = append([]string{node.Info.Name}, row.NodePath...)
		rep.rows = append(rep.rows, row)
	}
	if len(wrap.Warnings) > 0 {
		// Nested warnings are prefixed so an operator can tell which branch of
		// the swarm produced them.
		rep.warning = node.Info.Name + " -> " + strings.Join(wrap.Warnings, "; ")
	}
	return rep
}

// errChainLooped means the chain came back to this relay: in a ring that is the
// ordinary end of a branch. errChainTooDeep means it is simply longer than the
// swarm carries, which is a fault worth telling somebody about. Collapsing the
// two would let an acyclic but distant relay disappear in silence.
var (
	errChainLooped  = errors.New("chain looped")
	errChainTooDeep = errors.New("chain too deep")
)

// hopPath records this relay in the chain and refuses a request that already
// passed through it.
func (s *Server) hopPath(r *http.Request) ([]string, error) {
	raw := strings.TrimSpace(r.Header.Get(SwarmPathHeader))
	var path []string
	if raw != "" {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				path = append(path, part)
			}
		}
	}
	for _, uuid := range path {
		if uuid == s.uuid {
			return nil, fmt.Errorf("%w: back through relay %s", errChainLooped, s.relayName())
		}
	}
	if len(path) >= swarmMaxHops {
		return nil, fmt.Errorf("%w: deeper than %d relays at %s", errChainTooDeep, swarmMaxHops, s.relayName())
	}
	return append(path, s.uuid), nil
}

func (s *Server) relayName() string {
	if name := strings.TrimSpace(s.cfg.Swarm.Name); name != "" {
		return name
	}
	return s.uuid[:8]
}

// dedupeByIdentity collapses the same session arriving by several routes.
func dedupeByIdentity(rows []sessionRow) []sessionRow {
	if len(rows) < 2 {
		return rows
	}
	best := map[string]int{}
	out := make([]sessionRow, 0, len(rows))
	for _, row := range rows {
		key := row.AgentUUID + "\x00" + row.ID
		if row.AgentUUID == "" {
			// Without an agent identity there is nothing safe to collapse on:
			// two nodes may legitimately use the same session id.
			out = append(out, row)
			continue
		}
		idx, seen := best[key]
		if !seen {
			best[key] = len(out)
			out = append(out, row)
			continue
		}
		if len(row.NodePath) < len(out[idx].NodePath) {
			out[idx] = row
		}
	}
	return out
}

// sortSessionRows orders by recency, then deterministically, so two clients
// asking the same relay see the same order and a cached route stays valid.
func sortSessionRows(rows []sessionRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].UpdatedAt != rows[j].UpdatedAt {
			return rows[i].UpdatedAt > rows[j].UpdatedAt
		}
		pi, pj := strings.Join(rows[i].NodePath, "/"), strings.Join(rows[j].NodePath, "/")
		if pi != pj {
			return pi < pj
		}
		return rows[i].ID < rows[j].ID
	})
}

func nodeMatchesSearch(node Node, search string) bool {
	needle := strings.ToLower(search)
	return strings.Contains(strings.ToLower(node.Info.Name), needle) ||
		strings.Contains(strings.ToLower(node.Info.URL), needle)
}

func parseLimit(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return defaultSessionLimit
	}
	if n > maxSessionLimit {
		return maxSessionLimit
	}
	return n
}

func anyTrue(m map[string]bool) bool {
	for _, v := range m {
		if v {
			return true
		}
	}
	return false
}
