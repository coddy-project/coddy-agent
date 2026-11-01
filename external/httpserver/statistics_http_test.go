//go:build http

package httpserver

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/statistics"
)

func statisticsTestServer(t *testing.T) (*Server, *session.Manager) {
	t.Helper()
	home := t.TempDir()
	cfg := &config.Config{Paths: config.Paths{Home: home, CWD: home}}
	store := &session.FileStore{Root: cfg.ResolvedSessionsRoot()}
	mgr := session.NewManager(cfg, noopSender{}, nil, slog.New(slog.DiscardHandler), home, store)
	srv := New(cfg, mgr, slog.New(slog.DiscardHandler), home)
	t.Cleanup(srv.Drain)
	return srv, mgr
}

func statisticsDo(s *Server, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	return w
}

func TestStatisticsHTTPPersistenceSettingsAndExport(t *testing.T) {
	s, m := statisticsTestServer(t)
	for _, e := range []statistics.Event{
		{ID: "t", Session: "removed", Kind: "turn", At: time.Now(), Source: "webui", Requests: 1, ActiveMS: statistics.Int(1000)},
		{ID: "c", Session: "removed", Kind: "call", At: time.Now(), Source: "webui", Model: "test", Input: statistics.Int(100), Output: statistics.Int(20), Cached: statistics.Int(50)},
	} {
		if err := m.Statistics().Append(e); err != nil {
			t.Fatal(err)
		}
	}
	w := statisticsDo(s, http.MethodPut, "/coddy/statistics/settings", `{"timezone":"Europe/Moscow","prices":{"test":{"input":2,"output":10,"cached":0.5}}}`)
	if w.Code != 200 {
		t.Fatalf("settings: %d %s", w.Code, w.Body.String())
	}
	w = statisticsDo(s, http.MethodGet, "/coddy/statistics?timezone=UTC", "")
	var out struct {
		Report   statistics.Report   `json:"report"`
		Settings statistics.Settings `json:"settings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || out.Report.Timezone != "Europe/Moscow" || out.Report.Totals.Total == nil || *out.Report.Totals.Total != 120 || out.Report.Totals.CostUSD == nil || *out.Report.Totals.CostUSD != 0.000325 {
		t.Fatalf("report: %s", w.Body.String())
	}
	for _, format := range []string{"csv", "json"} {
		w = statisticsDo(s, http.MethodGet, "/coddy/statistics/export?format="+format, "")
		if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") || !strings.Contains(w.Body.String(), "120") {
			t.Fatalf("export %s: %d %s", format, w.Code, w.Body.String())
		}
	}
	w = statisticsDo(s, http.MethodDelete, "/coddy/statistics?keep=none", "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	r, err := m.Statistics().Query(statistics.Filter{})
	if err != nil || r.Totals.Chats != 0 {
		t.Fatalf("cleanup: %+v %v", r, err)
	}
}

func TestStatisticsHTTPRejectsInvalidSettingsAndDates(t *testing.T) {
	s, _ := statisticsTestServer(t)
	for _, body := range []string{`{"timezone":"No/Such_Zone"}`, `{"prices":{"x":{"input":-1}}}`, `{"dataset":"overwrite"}`} {
		w := statisticsDo(s, http.MethodPut, "/coddy/statistics/settings", body)
		if w.Code != 400 {
			t.Fatalf("accepted %s: %d", body, w.Code)
		}
	}
	for _, path := range []string{"/coddy/statistics?from=bad", "/coddy/statistics?from=2026-11-02&to=2026-11-01", "/coddy/statistics?timezone=bad"} {
		if w := statisticsDo(s, http.MethodGet, path, ""); w.Code != 400 {
			t.Fatalf("accepted %s: %d", path, w.Code)
		}
	}
	if w := statisticsDo(s, http.MethodDelete, "/coddy/statistics?keep=invalid", ""); w.Code != 400 {
		t.Fatal("invalid retention accepted")
	}
}
