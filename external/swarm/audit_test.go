//go:build swarm

package swarm

// The audit counters of the relay (docs/plans/remote-model-provider-phase3.md, 6): counted outcomes per client label and node, never
// content and never a token, read with the full class only.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func relayStats(t *testing.T, base string) (string, []relayStatRow) {
	t.Helper()
	res, body := do(t, http.MethodGet, base+"/swarm/stats", fullToken)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /swarm/stats = %d %s", res.StatusCode, body)
	}
	var doc struct {
		Since string         `json:"since"`
		Rows  []relayStatRow `json:"rows"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	return body, doc.Rows
}

func rowOf(rows []relayStatRow, client, node, outcome string) (relayStatRow, bool) {
	for _, r := range rows {
		if r.Client == client && r.Node == node && r.Outcome == outcome {
			return r, true
		}
	}
	return relayStatRow{}, false
}

func TestRelayCountersCountEveryOutcomeOnTheSharedRoutes(t *testing.T) {
	g := newGateNode(t)
	close(g.hold)
	clock := &fakeTime{now: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}
	wide := config.SwarmClient{Name: "wide", Token: "wide-secret", Scope: config.ScopeSharedModels, Nodes: []string{"*"}}
	_, ts := limitRelay(t, g, clock, limited("acme", "acme-secret", 0, 60, 1), wide)

	post(t, ts.URL+relayCompletions, "acme-secret")                                    // ok
	post(t, ts.URL+relayCompletions, "acme-secret")                                    // limit (window)
	do(t, http.MethodGet, ts.URL+"/swarm/nodes/other/coddy/llm/models", "acme-secret") // scope: not on the list
	do(t, http.MethodGet, ts.URL+"/swarm/nodes/nas02/coddy/sessions", "acme-secret")   // a route that is not one of the three: not counted
	do(t, http.MethodPost, ts.URL+relayCompletions, "made-up-token")                   // refused token on a shared route
	post(t, ts.URL+relayCompletions, fullToken)                                        // the full class
	do(t, http.MethodGet, ts.URL+"/swarm/nodes/ghost/coddy/llm/models", "wide-secret") // a guessed name: node label "-"
	do(t, http.MethodGet, ts.URL+"/swarm/nodes/ghost2/coddy/llm/models", "wide-secret")

	_, rows := relayStats(t, ts.URL)
	want := []struct{ client, node, outcome string }{
		{"acme", "nas02", "ok"},
		{"acme", "nas02", "limit"},
		{"acme", "-", "scope"},
		{"unknown", "-", "auth"},
		{"full", "nas02", "ok"},
	}
	for _, w := range want {
		if r, ok := rowOf(rows, w.client, w.node, w.outcome); !ok || r.Calls != 1 {
			t.Errorf("row %v: %+v (found %v), want one call", w, r, ok)
		}
	}
	// A route outside the three is never counted.
	for _, r := range rows {
		if r.Client == "acme" && r.Outcome == "scope" && r.Calls != 1 {
			t.Errorf("a route outside the table was counted: %+v", r)
		}
	}
	// A guessed node name never makes a key of its own: both names fold into one row labelled "-".
	if r, ok := rowOf(rows, "wide", "-", "scope"); !ok || r.Calls != 2 {
		t.Errorf("guessed names: %+v %v", r, ok)
	}
	for _, r := range rows {
		if r.Node == "ghost" || r.Node == "ghost2" || r.Node == "other" {
			t.Errorf("a guessed name is a label: %+v", r)
		}
	}
}

func TestRelayStatsAreForTheFullClassOnly(t *testing.T) {
	g := newGateNode(t)
	_, ts := limitRelay(t, g, nil, limited("acme", "acme-secret", 0, 0, 0))
	if res, _ := do(t, http.MethodGet, ts.URL+"/swarm/stats", "acme-secret"); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("a scoped client reads the stats: %d", res.StatusCode)
	}
	if res, _ := do(t, http.MethodGet, ts.URL+"/swarm/stats", ""); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("no credential: %d", res.StatusCode)
	}
	// A parent cannot read a child's counters through a mount: the route is not carried.
	if mountAllows(http.MethodGet, "/swarm/stats") {
		t.Error("/swarm/stats is carried by a node mount")
	}
}

func TestRelayStatsKeySetAndNeedleScan(t *testing.T) {
	g := newGateNode(t)
	close(g.hold)
	_, ts := limitRelay(t, g, nil, limited("acme", "acme-secret", 0, 0, 0))
	post(t, ts.URL+relayCompletions, "acme-secret")
	body, _ := relayStats(t, ts.URL)
	for _, bad := range []string{"acme-secret", fullToken, "node-secret", "Bearer"} {
		if strings.Contains(body, bad) {
			t.Errorf("the stats document carries %q: %s", bad, body)
		}
	}
	var top map[string]json.RawMessage
	_ = json.Unmarshal([]byte(body), &top)
	if k := keysOf(top); !reflect.DeepEqual(k, []string{"rows", "since"}) {
		t.Errorf("top-level keys %v", k)
	}
	var rows []map[string]json.RawMessage
	_ = json.Unmarshal(top["rows"], &rows)
	want := []string{"calls", "client", "duration_ms", "max_duration_ms", "node", "outcome"}
	if len(rows) == 0 || !reflect.DeepEqual(keysOf(rows[0]), want) {
		t.Errorf("row keys %v, want %v", rows, want)
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// The rows are bounded: past the cap a new node folds into "-", and no count is lost.
func TestRelayCountersAreBounded(t *testing.T) {
	c := newRelayCounters(time.Now)
	for i := 0; i < relayStatsMaxRows*2; i++ {
		c.add("acme", fmt.Sprintf("node%d", i), "ok", time.Millisecond)
	}
	snap := c.snapshot()
	if len(snap.Rows) > relayStatsMaxRows+2 {
		t.Fatalf("%d rows past the cap of %d", len(snap.Rows), relayStatsMaxRows)
	}
	var total int64
	for _, r := range snap.Rows {
		total += r.Calls
	}
	if total != int64(relayStatsMaxRows*2) {
		t.Fatalf("calls %d, a fold must lose none", total)
	}
}
