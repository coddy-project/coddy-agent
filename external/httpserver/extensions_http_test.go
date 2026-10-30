//go:build http

package httpserver

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/extensions"
)

func TestUIExtensionRoutes(t *testing.T) {
	cfg := &config.Config{}
	cfg.Paths.Home = t.TempDir()
	s := &Server{mux: http.NewServeMux()}
	s.cfgAt.Store(cfg)
	s.registerExtensionRoutes()
	request := func(method, path string, body []byte) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		w := httptest.NewRecorder()
		s.authGate(s.mux).ServeHTTP(w, r)
		return w
	}
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, body := range map[string]string{"manifest.json": `{"api":1,"id":"demo","name":"Demo","version":"1","permissions":["ui.page"],"ui":{"entry":"main.js","compat_styles":["legacy.css"]}}`, "main.js": "export function activate() {}", "legacy.css": ".legacy {}"} {
		f, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	w := request("POST", "/coddy/extensions", b.Bytes())
	if w.Code != 201 {
		t.Fatalf("install: %d %s", w.Code, w.Body)
	}
	var row extensions.Entry
	if err := json.Unmarshal(w.Body.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	if row.Enabled {
		t.Fatal("installed enabled")
	}
	url := "/coddy/extensions/demo/assets/main.js?sha256=" + row.SHA256
	if w = request("GET", url, nil); w.Code != 409 {
		t.Fatalf("disabled: %d", w.Code)
	}
	if w = request("PUT", "/coddy/extensions/demo", []byte(`{"enabled":true,"sha256":"`+row.SHA256+`"}`)); w.Code != 400 {
		t.Fatalf("risk not required: %d", w.Code)
	}
	if w = request("PUT", "/coddy/extensions/demo", []byte(`{"enabled":true,"accept_risk":true,"sha256":"`+row.SHA256+`"}`)); w.Code != 200 {
		t.Fatalf("enable: %d %s", w.Code, w.Body)
	}
	if w = request("GET", url, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "activate") || w.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("asset: %d %s", w.Code, w.Body)
	}
	if w = request("GET", "/coddy/extensions", nil); w.Code != 200 {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	var catalog struct {
		Items []extensions.Entry `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Items) != 1 || strings.Join(catalog.Items[0].Permissions, ",") != "ui.page" || strings.Join(catalog.Items[0].UI.CompatStyles, ",") != "legacy.css" {
		t.Fatalf("catalog item: %s", w.Body)
	}
	s.SetExtraAuthTokens([]string{"secret"})
	if w = request("GET", "/coddy/extensions", nil); w.Code != 401 {
		t.Fatalf("unprotected catalog: %d", w.Code)
	}
	if w = request("GET", url, nil); w.Code != 401 {
		t.Fatalf("unprotected asset: %d", w.Code)
	}
}

func TestUIExtensionEvents(t *testing.T) {
	cfg := &config.Config{}
	cfg.Paths.Home = t.TempDir()
	s := &Server{mux: http.NewServeMux()}
	s.cfgAt.Store(cfg)
	s.events = newServerEventsHub()
	s.registerExtensionRoutes()
	request := func(method, path string, body []byte) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		w := httptest.NewRecorder()
		s.authGate(s.mux).ServeHTTP(w, r)
		return w
	}
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, body := range map[string]string{"manifest.json": `{"api":1,"id":"demo","name":"Demo","version":"1","ui":{"entry":"main.js"}}`, "main.js": "export function activate() {}"} {
		f, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	w := request("POST", "/coddy/extensions", b.Bytes())
	if w.Code != 201 {
		t.Fatalf("install: %d %s", w.Code, w.Body)
	}
	var row extensions.Entry
	if err := json.Unmarshal(w.Body.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	eventBody := []byte(`{"sha256":"` + row.SHA256 + `","type":"open-file","data":{"path":"a.ts"}}`)
	// A disabled package publishes nothing.
	if w = request("POST", "/coddy/extensions/demo/events", eventBody); w.Code != 409 {
		t.Fatalf("disabled publish: %d %s", w.Code, w.Body)
	}
	if w = request("PUT", "/coddy/extensions/demo", []byte(`{"enabled":true,"sha256":"`+row.SHA256+`","accept_risk":true}`)); w.Code != 200 {
		t.Fatalf("enable: %d %s", w.Code, w.Body)
	}
	frames, unsubscribe := s.events.subscribe()
	defer unsubscribe()
	if w = request("POST", "/coddy/extensions/demo/events", eventBody); w.Code != 204 {
		t.Fatalf("publish: %d %s", w.Code, w.Body)
	}
	var frame []byte
	select {
	case frame = <-frames:
	default:
		t.Fatal("no frame published")
	}
	if !bytes.HasPrefix(frame, []byte("event: extension_event\ndata: ")) || !bytes.HasSuffix(frame, []byte("\n\n")) {
		t.Fatalf("frame shape: %q", frame)
	}
	var payload struct {
		Object    string `json:"object"`
		Extension string `json:"extension"`
		Type      string `json:"type"`
		Data      struct {
			Path string `json:"path"`
		} `json:"data"`
	}
	if err := json.Unmarshal(bytes.TrimSuffix(bytes.TrimPrefix(frame, []byte("event: extension_event\ndata: ")), []byte("\n\n")), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Object != "coddy.extension_event" || payload.Extension != "demo" || payload.Type != "open-file" || payload.Data.Path != "a.ts" {
		t.Fatalf("payload: %+v", payload)
	}
	// Event types follow the contribution id shape; a type that breaks it is a 400.
	if w = request("POST", "/coddy/extensions/demo/events", []byte(`{"sha256":"`+row.SHA256+`","type":"Open File"}`)); w.Code != 400 {
		t.Fatalf("bad type: %d %s", w.Code, w.Body)
	}
	// The digest gate applies: a wrong digest is unavailable.
	if w = request("POST", "/coddy/extensions/demo/events", []byte(`{"sha256":"deadbeef","type":"ping"}`)); w.Code != 409 {
		t.Fatalf("wrong digest: %d %s", w.Code, w.Body)
	}
}
