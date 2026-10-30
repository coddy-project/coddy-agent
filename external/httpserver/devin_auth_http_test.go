//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/devinfake"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/cucumber/godog"
)

func newDevinHTTPTestServer(t *testing.T, home string) *Server {
	t.Helper()
	cfg := &config.Config{Paths: config.Paths{Home: home, CWD: home, ConfigPath: filepath.Join(home, "config.yaml")}, Providers: []config.ProviderConfig{{Name: "devin", Type: "devin"}}}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), home, nil)
	srv := New(cfg, mgr, slog.Default(), home)
	t.Cleanup(func() { srv.cancelDevinAuthLogins(""); srv.bgWG.Wait() })
	return srv
}

func devinHTTPTestEnv(t *testing.T, base string) {
	t.Helper()
	t.Setenv(llm.EnvDevinAPIServerURL, base)
	t.Setenv(llm.EnvDevinWebappURL, base)
	t.Setenv(llm.EnvDevinAPIURL, base)
	t.Setenv(llm.EnvDevinCLICredentials, filepath.Join(t.TempDir(), "absent.toml"))
	t.Setenv("DEVIN_API_KEY", "")
	restore := llm.SetDevinLoginTimeout(5 * time.Second)
	t.Cleanup(restore)
}

func devinAuthRequest(t *testing.T, srv *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	return w
}

func startDevinHTTPLogin(t *testing.T, srv *Server, proxy string) codexAuthLoginResponse {
	t.Helper()
	body := "{}"
	if proxy != "" {
		body = fmt.Sprintf(`{"proxy":%q}`, proxy)
	}
	w := devinAuthRequest(t, srv, "POST", "/coddy/providers/devin/devin-auth/device", body)
	if w.Code != http.StatusOK {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	var start codexAuthLoginResponse
	if err := json.Unmarshal(w.Body.Bytes(), &start); err != nil {
		t.Fatal(err)
	}
	if start.LoginID == "" || start.VerificationURL == "" || start.Status != "pending" {
		t.Fatalf("incomplete start: %+v", start)
	}
	if strings.Contains(w.Body.String(), "session-token") {
		t.Fatal("the start response exposed a session token")
	}
	return start
}

func waitDevinHTTPLogin(t *testing.T, srv *Server, loginID string) codexAuthLoginResponse {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w := devinAuthRequest(t, srv, "GET", "/coddy/providers/devin/devin-auth/device/"+loginID, "")
		var state codexAuthLoginResponse
		if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		if state.Status == "completed" || state.Status == "failed" {
			if strings.Contains(w.Body.String(), "session-token") {
				t.Fatal("the poll response exposed a session token")
			}
			return state
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Devin login did not finish")
	return codexAuthLoginResponse{}
}

func TestDevinAuthHTTPCallbackFlow(t *testing.T) {
	stand := httptest.NewServer(devinfake.New(devinfake.Options{}))
	defer stand.Close()
	devinHTTPTestEnv(t, stand.URL)
	home := t.TempDir()
	srv := newDevinHTTPTestServer(t, home)
	start := startDevinHTTPLogin(t, srv, "")

	res, err := http.Get(start.VerificationURL)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	state := waitDevinHTTPLogin(t, srv, start.LoginID)
	if state.Status != "completed" || !state.Connected {
		t.Fatalf("login = %+v", state)
	}
	status := devinAuthRequest(t, srv, "GET", "/coddy/providers/devin/devin-auth", "")
	var account devinAuthStatusResponse
	if err := json.Unmarshal(status.Body.Bytes(), &account); err != nil {
		t.Fatal(err)
	}
	if !account.Connected || account.Source != llm.DevinSourceCoddy || account.Email != "dev@example.com" {
		t.Fatalf("account = %+v", account)
	}
	if _, err := os.Stat(config.DevinAuthPath(home, "devin")); err != nil {
		t.Fatalf("stored credential: %v", err)
	}

	removed := devinAuthRequest(t, srv, "DELETE", "/coddy/providers/devin/devin-auth", "")
	if removed.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", removed.Code, removed.Body)
	}
	if err := json.Unmarshal(removed.Body.Bytes(), &account); err != nil {
		t.Fatal(err)
	}
	if account.Connected || account.Source != "none" {
		t.Fatalf("after delete = %+v", account)
	}
}

func TestDevinAuthHTTPPasteFlowAndRejectedInput(t *testing.T) {
	stand := httptest.NewServer(devinfake.New(devinfake.Options{}))
	defer stand.Close()
	devinHTTPTestEnv(t, stand.URL)
	srv := newDevinHTTPTestServer(t, t.TempDir())
	start := startDevinHTTPLogin(t, srv, "")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Get(start.VerificationURL)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	callback := res.Header.Get("Location")
	if callback == "" {
		t.Fatal("Devin stand did not redirect to the callback")
	}
	path := "/coddy/providers/devin/devin-auth/device/" + start.LoginID + "/paste"
	bad := devinAuthRequest(t, srv, "POST", path, `{"url":"not-a-callback"}`)
	if bad.Code != http.StatusAccepted {
		t.Fatalf("bad paste: %d %s", bad.Code, bad.Body)
	}
	deadline := time.Now().Add(time.Second)
	for {
		poll := devinAuthRequest(t, srv, "GET", "/coddy/providers/devin/devin-auth/device/"+start.LoginID, "")
		if strings.Contains(poll.Body.String(), "whole address") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("rejected paste was not reported: %s", poll.Body)
		}
		time.Sleep(10 * time.Millisecond)
	}
	good := devinAuthRequest(t, srv, "POST", path, fmt.Sprintf(`{"url":%q}`, callback))
	if good.Code != http.StatusAccepted {
		t.Fatalf("good paste: %d %s", good.Code, good.Body)
	}
	if state := waitDevinHTTPLogin(t, srv, start.LoginID); state.Status != "completed" {
		t.Fatalf("login = %+v", state)
	}
}

func TestDevinAuthHTTPUsesUnsavedProxy(t *testing.T) {
	stand := httptest.NewServer(devinfake.New(devinfake.Options{}))
	defer stand.Close()
	standURL, _ := url.Parse(stand.URL)
	var calls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !r.URL.IsAbs() {
			t.Error("Devin request bypassed its proxy")
			http.Error(w, "absolute URL required", http.StatusBadRequest)
			return
		}
		calls.Add(1)
		out := r.Clone(r.Context())
		out.URL.Scheme = standURL.Scheme
		out.URL.Host = standURL.Host
		out.RequestURI = ""
		res, err := http.DefaultTransport.RoundTrip(out)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer func() { _ = res.Body.Close() }()
		for key, values := range res.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(res.StatusCode)
		_, _ = io.Copy(w, res.Body)
	}))
	defer proxy.Close()
	devinHTTPTestEnv(t, "http://devin.invalid")
	srv := newDevinHTTPTestServer(t, t.TempDir())
	start := startDevinHTTPLogin(t, srv, proxy.URL)
	proxyURL, _ := url.Parse(proxy.URL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Get(start.VerificationURL)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	callback := res.Header.Get("Location")
	pasted := devinAuthRequest(t, srv, "POST", "/coddy/providers/devin/devin-auth/device/"+start.LoginID+"/paste", fmt.Sprintf(`{"url":%q}`, callback))
	if pasted.Code != http.StatusAccepted {
		t.Fatalf("paste: %d %s", pasted.Code, pasted.Body)
	}
	if state := waitDevinHTTPLogin(t, srv, start.LoginID); state.Status != "completed" {
		t.Fatalf("login = %+v", state)
	}
	if calls.Load() == 0 {
		t.Fatal("no Devin request used the form's proxy")
	}
}

func TestDevinAuthHTTPLogoutCancelsPendingLogin(t *testing.T) {
	stand := httptest.NewServer(devinfake.New(devinfake.Options{}))
	defer stand.Close()
	devinHTTPTestEnv(t, stand.URL)
	home := t.TempDir()
	srv := newDevinHTTPTestServer(t, home)
	start := startDevinHTTPLogin(t, srv, "")
	removed := devinAuthRequest(t, srv, "DELETE", "/coddy/providers/devin/devin-auth", "")
	if removed.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", removed.Code, removed.Body)
	}
	state := waitDevinHTTPLogin(t, srv, start.LoginID)
	if state.Status != "failed" || state.Connected {
		t.Fatalf("cancelled login = %+v", state)
	}
	if _, err := os.Stat(config.DevinAuthPath(home, "devin")); !os.IsNotExist(err) {
		t.Fatalf("cancelled login stored a credential: %v", err)
	}
}

func TestDevinLoginFeature(t *testing.T) {
	var srv *Server
	var start codexAuthLoginResponse
	suite := godog.TestSuite{
		Name: "devin_login",
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/devin_login.feature"},
			Tags:     "~@ui",
			TestingT: t,
			Strict:   true,
		},
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^a Devin account server and a provider settings server$`, func() {
				stand := httptest.NewServer(devinfake.New(devinfake.Options{}))
				t.Cleanup(stand.Close)
				devinHTTPTestEnv(t, stand.URL)
				srv = newDevinHTTPTestServer(t, t.TempDir())
			})
			sc.Step(`^the form starts Devin sign-in$`, func() {
				start = startDevinHTTPLogin(t, srv, "")
			})
			sc.Step(`^the browser receives a verification page$`, func() {
				if start.VerificationURL == "" {
					t.Fatal("the start response has no verification page")
				}
			})
			sc.Step(`^the login completes from the pasted callback address$`, func() {
				client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
				res, err := client.Get(start.VerificationURL)
				if err != nil {
					t.Fatal(err)
				}
				_ = res.Body.Close()
				callback := res.Header.Get("Location")
				path := "/coddy/providers/devin/devin-auth/device/" + start.LoginID + "/paste"
				pasted := devinAuthRequest(t, srv, "POST", path, fmt.Sprintf(`{"url":%q}`, callback))
				if pasted.Code != http.StatusAccepted {
					t.Fatalf("paste: %d %s", pasted.Code, pasted.Body)
				}
				if state := waitDevinHTTPLogin(t, srv, start.LoginID); state.Status != "completed" {
					t.Fatalf("login = %+v", state)
				}
			})
			sc.Step(`^the form signs out of Devin$`, func() {
				removed := devinAuthRequest(t, srv, "DELETE", "/coddy/providers/devin/devin-auth", "")
				if removed.Code != http.StatusOK {
					t.Fatalf("delete: %d %s", removed.Code, removed.Body)
				}
			})
			sc.Step(`^the provider has no stored Devin login$`, func() {
				path := config.DevinAuthPath(srv.activeCfg().Paths.Home, "devin")
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("stored credential still exists: %v", err)
				}
			})
		},
	}
	if suite.Run() != 0 {
		t.Fatal("devin_login feature failed")
	}
}
