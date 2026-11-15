//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
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
	// A request that names a zone is laid out in it; the saved one is what a
	// request without one gets, and a relay leans on exactly that difference.
	w = statisticsDo(s, http.MethodGet, "/coddy/statistics?timezone=UTC", "")
	var out struct {
		Report   statistics.Report   `json:"report"`
		Settings statistics.Settings `json:"settings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || out.Report.Timezone != "UTC" || out.Report.Totals.Total == nil || *out.Report.Totals.Total != 120 || out.Report.Totals.CostUSD == nil || *out.Report.Totals.CostUSD != 0.000325 {
		t.Fatalf("report: %s", w.Body.String())
	}
	w = statisticsDo(s, http.MethodGet, "/coddy/statistics", "")
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || out.Report.Timezone != "Europe/Moscow" {
		t.Fatalf("the saved zone is the default: %s", w.Body.String())
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

func TestStatisticsDirectWebUICompletionKeepsItsSurface(t *testing.T) {
	m, s, _ := testHTTPServerPersist(t)
	s.makeLLMFromYAML = func(*config.Config, string, llm.RequestOptions) (llm.Provider, error) {
		return fakeProvider{reply: "done"}, nil
	}
	w := statisticsDo(s, http.MethodPost, "/v1/responses", `{"model":"openai/gpt-4o","input":"hi","stream":false,"metadata":{"surface":"webui"}}`)
	if w.Code != 200 {
		t.Fatalf("direct completion: %d %s", w.Code, w.Body.String())
	}
	report, err := m.Statistics().Query(statistics.Filter{Source: "webui"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.Requests != 1 {
		t.Fatalf("direct web UI request was recorded under another source: %+v", report)
	}
}

// A PUT names what it changes. The screen sends both fields, but the route is
// public: a client saving one of them used to wipe the other without a word.
func TestStatisticsSettingsKeepsWhatAPutDoesNotName(t *testing.T) {
	s, _ := statisticsTestServer(t)
	if w := statisticsDo(s, http.MethodPut, "/coddy/statistics/settings", `{"timezone":"Europe/Moscow","currency":"RUB","usdToRub":90,"prices":{"test":{"input":2,"output":10,"cached":0.5}}}`); w.Code != 200 {
		t.Fatalf("settings: %d %s", w.Code, w.Body.String())
	}
	w := statisticsDo(s, http.MethodPut, "/coddy/statistics/settings", `{"timezone":"UTC"}`)
	var kept statistics.Settings
	if err := json.Unmarshal(w.Body.Bytes(), &kept); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || kept.Timezone != "UTC" || kept.Currency != "RUB" || kept.USDToRub != 90 {
		t.Fatalf("partial settings update: %d %+v", w.Code, kept)
	}
	if _, ok := kept.Prices["test"]; !ok {
		t.Fatalf("saving the time zone wiped the tariffs: %s", w.Body.String())
	}
	w = statisticsDo(s, http.MethodPut, "/coddy/statistics/settings", `{"prices":{"test":{"input":2,"output":10}}}`)
	if err := json.Unmarshal(w.Body.Bytes(), &kept); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || kept.Timezone != "UTC" || kept.Currency != "RUB" || kept.USDToRub != 90 {
		t.Fatalf("saving the tariffs wiped the time zone: %d %s", w.Code, w.Body.String())
	}
	if w := statisticsDo(s, http.MethodPut, "/coddy/statistics/settings", `{"prices":{}}`); w.Code != 200 {
		t.Fatalf("an empty tariff list is how they are dropped: %d %s", w.Code, w.Body.String())
	}
	st, err := s.mgr.Statistics().Settings()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Prices) != 0 {
		t.Fatalf("an empty tariff list left %d of them", len(st.Prices))
	}
}

// A tariff without a cache rate reads the input rate, as the embedded catalog
// does; an explicit zero is a free cache read.
func TestStatisticsTariffWithoutACacheRateReadsTheInputRate(t *testing.T) {
	s, m := statisticsTestServer(t)
	if err := m.Statistics().Append(statistics.Event{ID: "c", Session: "chat", Kind: "call", At: time.Now(), Model: "test", Input: statistics.Int(1_000_000), Output: statistics.Int(0), Cached: statistics.Int(1_000_000)}); err != nil {
		t.Fatal(err)
	}
	if w := statisticsDo(s, http.MethodPut, "/coddy/statistics/settings", `{"prices":{"test":{"input":2,"output":10}}}`); w.Code != 200 {
		t.Fatalf("settings: %d %s", w.Code, w.Body.String())
	}
	report, err := m.Statistics().Query(statistics.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.CostUSD == nil || *report.Totals.CostUSD != 2 {
		t.Fatalf("cached input was priced free: %+v", report.Totals.CostUSD)
	}
}

func TestStatisticsHTTPRejectsInvalidSettingsAndDates(t *testing.T) {
	s, _ := statisticsTestServer(t)
	for _, body := range []string{
		`{"timezone":"No/Such_Zone"}`,
		`{"prices":{"x":{"input":-1}}}`,
		`{"currency":"EUR"}`,
		`{"usdToRub":0}`,
		`{"usdToRub":-1}`,
		`{"dataset":"overwrite"}`,
	} {
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

func TestStatisticsCurrencyOpenAPI(t *testing.T) {
	spec := openAPISpec()
	paths := spec["paths"].(map[string]interface{})
	put := paths["/coddy/statistics/settings"].(map[string]interface{})["put"].(map[string]interface{})
	body := put["requestBody"].(map[string]interface{})
	content := body["content"].(map[string]interface{})
	requestSchema := content["application/json"].(map[string]interface{})["schema"].(map[string]interface{})
	requestProps := requestSchema["properties"].(map[string]interface{})
	currency := requestProps["currency"].(map[string]interface{})
	if currency["type"] != "string" {
		t.Fatalf("currency schema = %+v", currency)
	}
	allowed, ok := currency["enum"].([]string)
	if !ok || len(allowed) != 2 || allowed[0] != statistics.CurrencyUSD || allowed[1] != statistics.CurrencyRUB {
		t.Fatalf("currency enum = %#v", currency["enum"])
	}
	rate := requestProps["usdToRub"].(map[string]interface{})
	if rate["type"] != "number" || rate["minimum"] != 0 || rate["exclusiveMinimum"] != true {
		t.Fatalf("usdToRub schema = %+v", rate)
	}
	schemas := spec["components"].(map[string]interface{})["schemas"].(map[string]interface{})
	settings := schemas["StatisticsSettings"].(map[string]interface{})["properties"].(map[string]interface{})
	if settings["currency"] == nil || settings["usdToRub"] == nil {
		t.Fatalf("settings response schema is missing currency fields: %+v", settings)
	}
	if settings["currency"].(map[string]interface{})["default"] != statistics.CurrencyUSD || settings["usdToRub"].(map[string]interface{})["default"] != statistics.DefaultUSDToRubRate {
		t.Fatalf("statistics currency defaults = %+v / %+v", settings["currency"], settings["usdToRub"])
	}
	requestPrices := requestProps["prices"].(map[string]interface{})
	priceInputRef := requestPrices["additionalProperties"].(map[string]interface{})["$ref"]
	if priceInputRef != "#/components/schemas/StatisticsPriceInput" {
		t.Fatalf("price input schema = %v, want StatisticsPriceInput", priceInputRef)
	}
	priceInput := schemas["StatisticsPriceInput"].(map[string]interface{})
	required, ok := priceInput["required"].([]string)
	if !ok {
		t.Fatalf("price input required fields = %#v", priceInput["required"])
	}
	for _, key := range required {
		if key == "currency" {
			t.Fatal("price currency must be optional in requests")
		}
	}
	priceCurrency := schemas["StatisticsPrice"].(map[string]interface{})["properties"].(map[string]interface{})["currency"].(map[string]interface{})
	priceCurrencyValues, ok := priceCurrency["enum"].([]string)
	if !ok || len(priceCurrencyValues) != 2 || priceCurrencyValues[0] != statistics.CurrencyUSD || priceCurrencyValues[1] != statistics.CurrencyRUB || priceCurrency["default"] != statistics.CurrencyUSD {
		t.Fatalf("model price currency schema = %+v", priceCurrency)
	}
}

// The switch stops the collection and says so on the route the rail reads,
// so a page nobody records for is not offered.
func TestStatisticsCollectionSwitchStopsRecordingAndHidesThePage(t *testing.T) {
	s, m := statisticsTestServer(t)
	info := func() bool {
		w := statisticsDo(s, http.MethodGet, "/coddy/info", "")
		var out struct {
			Statistics *bool `json:"statistics"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.Statistics == nil {
			t.Fatalf("info: %s", w.Body.String())
		}
		return *out.Statistics
	}
	if !info() {
		t.Fatal("a fresh home records by default")
	}
	if w := statisticsDo(s, http.MethodPut, "/coddy/statistics/settings", `{"enabled":false}`); w.Code != 200 {
		t.Fatalf("switch off: %d %s", w.Code, w.Body.String())
	}
	if info() {
		t.Fatal("the page is still offered with the collection off")
	}
	store := m.Statistics()
	if _, rec := statistics.Begin(context.Background(), store, statistics.Event{Session: "chat"}, nil); rec != nil {
		t.Fatal("a turn still took a recorder")
	}
	if err := store.Append(statistics.Event{ID: "x", Kind: "call", At: time.Now(), Input: statistics.Int(10), Output: statistics.Int(2)}); err != nil {
		t.Fatal(err)
	}
	report, err := store.Query(statistics.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Totals.Total != nil {
		t.Fatalf("a switched-off home wrote a checkpoint: %+v", report.Totals)
	}
	if w := statisticsDo(s, http.MethodPut, "/coddy/statistics/settings", `{"enabled":true}`); w.Code != 200 {
		t.Fatalf("switch on: %d %s", w.Code, w.Body.String())
	}
	if !info() {
		t.Fatal("the page did not come back")
	}
}
