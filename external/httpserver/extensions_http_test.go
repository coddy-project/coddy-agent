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
	s.SetExtraAuthTokens([]string{"secret"})
	if w = request("GET", "/coddy/extensions", nil); w.Code != 401 {
		t.Fatalf("unprotected catalog: %d", w.Code)
	}
	if w = request("GET", url, nil); w.Code != 401 {
		t.Fatalf("unprotected asset: %d", w.Code)
	}
}
