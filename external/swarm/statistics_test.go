//go:build swarm

package swarm

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/statistics"
	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

type statisticsTransport struct {
	body   string
	status int
	t      *testing.T
}

func (s statisticsTransport) RoundTripper() http.RoundTripper { return s }
func (s statisticsTransport) TargetURL() *url.URL {
	return &url.URL{Scheme: "http", Host: "statistics.test"}
}
func (s statisticsTransport) Alive() bool  { return true }
func (s statisticsTransport) Close() error { return nil }
func (s statisticsTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Header.Get("Authorization") != "Bearer node-secret" || r.URL.Path != "/coddy/statistics/events" {
		s.t.Errorf("incorrect authenticated mount request: %s", r.URL.Path)
	}
	return &http.Response{StatusCode: s.status, Status: http.StatusText(s.status), Body: io.NopCloser(strings.NewReader(s.body)), Header: make(http.Header)}, nil
}

func TestStatisticsSwarmDeduplicatesRoutesAndReportsUnavailableNodes(t *testing.T) {
	s, err := New(&config.Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	e := statistics.Event{ID: "one", Dataset: "shared", Session: "s", At: at, Kind: "call", Input: statistics.Int(100), Output: statistics.Int(20)}
	body, _ := json.Marshal(map[string]any{"dataset": "shared", "events": []statistics.Event{e}, "import": statistics.ImportStatus{State: "complete"}})
	for _, name := range []string{"a", "b"} {
		s.registry.nodes[name] = &lease{info: swarmdto.NodeInfo{Name: name, Kind: swarmdto.KindAgent}, token: "node-secret", expiresAt: at.Add(time.Hour), transport: statisticsTransport{body: string(body), status: 200, t: t}}
	}
	s.registry.nodes["old"] = &lease{info: swarmdto.NodeInfo{Name: "old", Kind: swarmdto.KindAgent}, token: "node-secret", expiresAt: at.Add(time.Hour), transport: statisticsTransport{body: "not found", status: 404, t: t}}
	s.registry.nodes["offline"] = &lease{info: swarmdto.NodeInfo{Name: "offline", Kind: swarmdto.KindAgent}, expiresAt: at.Add(-time.Second)}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/swarm/statistics?timezone=Europe%2FMoscow", nil))
	var out statisticsEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || out.Report.Totals.Total == nil || *out.Report.Totals.Total != 120 || out.Report.Totals.Chats != 1 || len(out.Nodes) != 1 || len(out.Warnings) != 2 || out.Nodes[0].Path[0] != "a" {
		t.Fatalf("incorrect swarm summary: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "node-secret") {
		t.Fatal("credential in statistics")
	}
}
