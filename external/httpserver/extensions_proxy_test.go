//go:build http

package httpserver

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/extensions"
	"github.com/cucumber/godog"
)

type extensionProxyFixture struct {
	s      *Server
	server *httptest.Server
	store  *extensions.Store
	entry  extensions.Entry
}

func newExtensionProxyFixture(t *testing.T) *extensionProxyFixture {
	t.Helper()
	cfg := &config.Config{}
	cfg.Paths.Home = t.TempDir()
	store := extensions.NewStore(cfg.Paths.Home)
	var archive bytes.Buffer
	z := zip.NewWriter(&archive)
	for name, data := range map[string]string{"manifest.json": `{"api":1,"id":"demo","name":"Demo","version":"1","ui":{"entry":"main.js"}}`, "main.js": "export function activate() {}"} {
		f, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(f, data); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	entry, err := store.Install(archive.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	entry, err = store.SetEnabled(entry.ID, entry.SHA256, true)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{mux: http.NewServeMux()}
	s.cfgAt.Store(cfg)
	s.SetExtraAuthTokens([]string{"coddy-secret"})
	s.registerExtensionRoutes()
	server := httptest.NewServer(s.authGate(s.mux))
	t.Cleanup(server.Close)
	return &extensionProxyFixture{s: s, server: server, store: store, entry: entry}
}

func (f *extensionProxyFixture) request(t *testing.T, body extensionProxyRequest, authorized bool) (int, extensionProxyResponse) {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r, err := http.NewRequest(http.MethodPost, f.server.URL+"/coddy/extensions/demo/fetch", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Cookie", "coddy-browser-secret=private")
	if authorized {
		r.Header.Set("Authorization", "Bearer coddy-secret")
	}
	response, err := f.server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var result extensionProxyResponse
	if response.StatusCode == http.StatusOK {
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
	}
	if response.Header.Get("Set-Cookie") != "" {
		t.Fatal("target cookie escaped the JSON envelope")
	}
	return response.StatusCode, result
}

func TestExtensionProxyFeature(t *testing.T) {
	var f *extensionProxyFixture
	var target *httptest.Server
	var receivedHeader http.Header
	var receivedBody []byte
	var calls atomic.Int32
	var status int
	var result extensionProxyResponse
	suite := godog.TestSuite{Name: "ui_extension_proxy", Options: &godog.Options{
		Paths: []string{"../../features/ui_extension_proxy.feature"}, Format: "pretty", TestingT: t, Strict: true,
	}, ScenarioInitializer: func(sc *godog.ScenarioContext) {
		sc.Step(`^an approved extension and a local API without CORS$`, func() {
			f = newExtensionProxyFixture(t)
			target = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				receivedHeader = r.Header.Clone()
				receivedBody, _ = io.ReadAll(r.Body)
				w.Header().Set("X-Target", "local")
				w.Header().Set("Set-Cookie", "target-secret=hidden")
				w.Header().Set("Connection", "X-Hop")
				w.Header().Set("X-Hop", "private")
				w.WriteHeader(http.StatusTeapot)
				_, _ = w.Write([]byte{0, 255, 1})
			}))
			t.Cleanup(target.Close)
		})
		sc.Step(`^the extension posts binary data through the server proxy$`, func() {
			status, result = f.request(t, extensionProxyRequest{SHA256: f.entry.SHA256, URL: target.URL, Method: "POST", Headers: map[string]string{"Authorization": "Bearer target", "X-Explicit": "yes"}, Body: []byte{255, 0, 128}}, true)
		})
		sc.Step(`^the API receives the explicit headers and original bytes only$`, func() error {
			if calls.Load() != 1 || !bytes.Equal(receivedBody, []byte{255, 0, 128}) || receivedHeader.Get("Authorization") != "Bearer target" || receivedHeader.Get("Cookie") != "" || receivedHeader.Get("Origin") != "" || receivedHeader.Get("X-Explicit") != "yes" {
				return fmt.Errorf("unexpected target request: headers=%v body=%v", receivedHeader, receivedBody)
			}
			return nil
		})
		sc.Step(`^the extension receives the API status headers and binary response$`, func() error {
			if status != 200 || result.Status != 418 || result.Headers["X-Target"][0] != "local" || len(result.Headers["Set-Cookie"]) != 0 || len(result.Headers["X-Hop"]) != 0 || !bytes.Equal(result.Body, []byte{0, 255, 1}) {
				return fmt.Errorf("unexpected proxy response: status=%d result=%+v", status, result)
			}
			return nil
		})
		sc.Step(`^the extension is disabled$`, func() error {
			_, err := f.store.SetEnabled("demo", f.entry.SHA256, false)
			return err
		})
		sc.Step(`^another proxy request is refused before reaching the API$`, func() error {
			status, _ = f.request(t, extensionProxyRequest{SHA256: f.entry.SHA256, URL: target.URL}, true)
			if status != 409 || calls.Load() != 1 {
				return fmt.Errorf("disabled request: status=%d calls=%d", status, calls.Load())
			}
			return nil
		})
	}}
	if suite.Run() != 0 {
		t.Fatal("extension proxy feature failed")
	}
}

func TestExtensionProxyGuards(t *testing.T) {
	f := newExtensionProxyFixture(t)
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	base := extensionProxyRequest{SHA256: f.entry.SHA256, URL: target.URL}
	for _, tc := range []struct {
		name string
		edit func(*extensionProxyRequest)
		code int
	}{
		{"digest", func(r *extensionProxyRequest) { r.SHA256 = "wrong" }, 409},
		{"missing digest", func(r *extensionProxyRequest) { r.SHA256 = "" }, 409},
		{"scheme", func(r *extensionProxyRequest) { r.URL = "file:///etc/passwd" }, 400},
		{"userinfo", func(r *extensionProxyRequest) { r.URL = "http://user:password@localhost" }, 400},
		{"fragment", func(r *extensionProxyRequest) { r.URL += "#fragment" }, 400},
		{"method", func(r *extensionProxyRequest) { r.Method = "CONNECT" }, 400},
		{"get body", func(r *extensionProxyRequest) { r.Body = []byte("body") }, 400},
		{"body limit", func(r *extensionProxyRequest) { r.Method = "POST"; r.Body = make([]byte, extensionRequestLimit+1) }, 413},
		{"header injection", func(r *extensionProxyRequest) { r.Headers = map[string]string{"X-Data": "value\r\nInjected: yes"} }, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := base
			tc.edit(&body)
			if status, _ := f.request(t, body, true); status != tc.code {
				t.Fatalf("got %d, want %d", status, tc.code)
			}
		})
	}
	for _, name := range []string{"Cookie", "Host", "Connection", "Proxy-Authorization", "Origin", "X-Forwarded-For", "Content-Length", "Sec-Fetch-Site"} {
		t.Run(name, func(t *testing.T) {
			body := base
			body.Headers = map[string]string{name: "forbidden"}
			if status, _ := f.request(t, body, true); status != 400 {
				t.Fatalf("forbidden header accepted: %d", status)
			}
		})
	}
	if status, _ := f.request(t, base, false); status != 401 {
		t.Fatalf("unauthenticated: %d", status)
	}
	if calls.Load() != 0 {
		t.Fatalf("rejected calls reached the target: %d", calls.Load())
	}
	if status, result := f.request(t, base, true); status != 200 || result.Status != 204 || len(result.Body) != 0 {
		t.Fatalf("empty response: %d %+v", status, result)
	}
}

func TestExtensionProxyRedirectsLimitsAndTLS(t *testing.T) {
	f := newExtensionProxyFixture(t)
	var redirected atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer destination.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/large" {
			_, _ = io.CopyN(w, strings.NewReader(strings.Repeat("x", extensionResponseLimit+1)), extensionResponseLimit+1)
			return
		}
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer target.Close()
	status, result := f.request(t, extensionProxyRequest{SHA256: f.entry.SHA256, URL: target.URL, Headers: map[string]string{"Authorization": "secret"}}, true)
	if status != 200 || result.Status != 302 || result.Headers["Location"][0] != destination.URL || redirected.Load() != 0 {
		t.Fatalf("redirect followed or lost: %d %+v", status, result)
	}
	if status, _ := f.request(t, extensionProxyRequest{SHA256: f.entry.SHA256, URL: target.URL + "/large"}, true); status != 502 {
		t.Fatalf("unbounded response: %d", status)
	}
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer tls.Close()
	if status, _ := f.request(t, extensionProxyRequest{SHA256: f.entry.SHA256, URL: tls.URL}, true); status != 502 {
		t.Fatalf("untrusted certificate accepted: %d", status)
	}
}

func TestExtensionProxyDisableCancelsInFlight(t *testing.T) {
	f := newExtensionProxyFixture(t)
	started := make(chan struct{})
	stopped := make(chan struct{})
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	defer target.Close()
	finished := make(chan int, 1)
	go func() {
		status, _ := f.request(t, extensionProxyRequest{SHA256: f.entry.SHA256, URL: target.URL}, true)
		finished <- status
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("proxy did not start")
	}
	data := fmt.Sprintf(`{"sha256":%q,"enabled":false}`, f.entry.SHA256)
	r, err := http.NewRequest(http.MethodPut, f.server.URL+"/coddy/extensions/demo", strings.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer coddy-secret")
	response, err := f.server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("disable: %d", response.StatusCode)
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("disable did not cancel the target request")
	}
	select {
	case status := <-finished:
		if status != 409 {
			t.Fatalf("cancelled response: %d", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("proxy did not finish")
	}
}

func TestExtensionProxyDeadlineCancelsTarget(t *testing.T) {
	f := newExtensionProxyFixture(t)
	stopped := make(chan struct{})
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		close(stopped)
	}))
	defer target.Close()
	data, err := json.Marshal(extensionProxyRequest{SHA256: f.entry.SHA256, URL: target.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	r := httptest.NewRequest(http.MethodPost, "/coddy/extensions/demo/fetch", bytes.NewReader(data)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.s.mux.ServeHTTP(w, r)
	if w.Code != http.StatusGatewayTimeout {
		t.Fatalf("timeout status: %d", w.Code)
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("deadline did not cancel the target request")
	}
}
