//go:build http

package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/cucumber/godog"
)

// requireOpenCodeClientID holds a device-flow request to the contract the
// gateway mints Coddy's keys under: every grant names the client. A grant that
// stops sending it breaks account login in a way no stand would otherwise
// notice. The body is put back on the request, so a later reader still sees
// it. The value is written out rather than read from the llm package, so
// renaming the constant cannot quietly rename what Coddy announces.
func requireOpenCodeClientID(t *testing.T, r *http.Request, wantGrant string) {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Errorf("%s: read body: %v", r.URL.Path, err)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Errorf("%s: decode body: %v", r.URL.Path, err)
		return
	}
	if body["client_id"] != "coddy" {
		t.Errorf("%s: client_id = %v, want \"coddy\"", r.URL.Path, body["client_id"])
	}
	if wantGrant != "" {
		if got, _ := body["grant_type"].(string); got != wantGrant {
			t.Errorf("%s: grant_type = %q, want %q", r.URL.Path, got, wantGrant)
		}
	}
}

func openCodeAuthStand(t *testing.T, delay <-chan struct{}) (*Server, *httptest.Server, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !r.URL.IsAbs() {
			t.Error("OpenCode account request bypassed its proxy")
		}
		calls.Add(1)
		switch r.URL.Path {
		case "/console/auth/device/code":
			requireOpenCodeClientID(t, r, "")
			_, _ = io.WriteString(w, `{"device_code":"private-device","user_code":"USER-CODE","verification_uri_complete":"/console/device","expires_in":600,"interval":0}`)
		case "/console/auth/device/token":
			requireOpenCodeClientID(t, r, "")
			if delay != nil {
				select {
				case <-delay:
				case <-r.Context().Done():
					return
				}
			}
			_, _ = io.WriteString(w, `{"access_token":"private-access","refresh_token":"private-refresh","expires_in":600,"org_id":"org"}`)
		case "/console/api/user":
			_, _ = io.WriteString(w, `{"id":"user","email":"user@example.com"}`)
		case "/console/api/orgs":
			_, _ = io.WriteString(w, `[{"id":"org","name":"Test Org"}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(proxy.Close)
	t.Setenv(llm.EnvOpenCodeConsoleURL, "http://opencode.invalid/console")
	t.Setenv("OPENAI_API_KEY", "")
	srv := newNeuralDeepTestServer(t, t.TempDir())
	// Settings has switched an existing OpenAI row to OpenCode without Save.
	srv.activeCfg().Providers = []config.ProviderConfig{{Name: "openai", Type: "openai", Proxy: "http://127.0.0.1:1", APIKey: "other-provider-key"}}
	t.Cleanup(func() { srv.cancelOpenCodeAuthLogins(""); srv.bgWG.Wait() })
	return srv, proxy, calls
}

func openCodeAuthRequest(t *testing.T, srv *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	return w
}

func TestOpenCodeLoginFeature(t *testing.T) {
	var srv *Server
	var proxy *httptest.Server
	var calls *atomic.Int32
	var start codexAuthLoginResponse
	const path = "/coddy/providers/openai/opencode-auth"
	suite := godog.TestSuite{Name: "opencode_login", Options: &godog.Options{Paths: []string{"../../features/opencode_login.feature"}, Tags: "~@ui", Format: "pretty", TestingT: t, Strict: true}, ScenarioInitializer: func(sc *godog.ScenarioContext) {
		sc.Step(`^an OpenCode account issuer and a provider settings server$`, func() { srv, proxy, calls = openCodeAuthStand(t, nil) })
		sc.Step(`^the form starts OpenCode sign-in with its current proxy$`, func() error {
			body, _ := json.Marshal(map[string]string{"api_base": "https://opencode.ai/inference/v1", "proxy": proxy.URL})
			w := openCodeAuthRequest(t, srv, "POST", path+"/device", string(body))
			if w.Code != 200 {
				return fmt.Errorf("start: %d %s", w.Code, w.Body)
			}
			return json.Unmarshal(w.Body.Bytes(), &start)
		})
		sc.Step(`^the browser receives a verification page and a user code$`, func() error {
			if start.LoginID == "" || start.UserCode != "USER-CODE" || start.VerificationURL != "http://opencode.invalid/console/device" {
				return fmt.Errorf("incomplete device prompt")
			}
			return nil
		})
		sc.Step(`^the login completes with the selected organization$`, func() error {
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				w := openCodeAuthRequest(t, srv, "GET", path+"/device/"+start.LoginID, "")
				var state codexAuthLoginResponse
				_ = json.Unmarshal(w.Body.Bytes(), &state)
				if state.Status == "completed" {
					status := openCodeAuthRequest(t, srv, "GET", path, "")
					var account openCodeAuthStatusResponse
					_ = json.Unmarshal(status.Body.Bytes(), &account)
					if !account.Connected || account.Organization != "Test Org" || account.Source != "oauth" || calls.Load() != 4 {
						return fmt.Errorf("wrong account status or proxy request count")
					}
					if strings.Contains(status.Body.String(), "private-") {
						return fmt.Errorf("status exposed a secret")
					}
					return nil
				}
				if state.Status == "failed" {
					return fmt.Errorf("login failed: %s", state.Error)
				}
				time.Sleep(10 * time.Millisecond)
			}
			return fmt.Errorf("login did not finish")
		})
		sc.Step(`^the form signs out of OpenCode$`, func() error {
			w := openCodeAuthRequest(t, srv, "DELETE", path, "")
			if w.Code != 200 {
				return fmt.Errorf("logout: %d", w.Code)
			}
			return nil
		})
		sc.Step(`^the provider has no stored login$`, func() error {
			status, err := llm.InspectOpenCodeAuth(config.OpenCodeAuthPath(srv.activeCfg().Paths.Home, "openai"))
			if err != nil {
				return err
			}
			if status.Connected {
				return fmt.Errorf("logout retained credentials")
			}
			return nil
		})
	}}
	if suite.Run() != 0 {
		t.Fatal("OpenCode login feature failed")
	}
}

func TestOpenCodeAuthHTTPValidationAndCancellation(t *testing.T) {
	gate := make(chan struct{})
	defer close(gate)
	srv, proxy, _ := openCodeAuthStand(t, gate)
	const path = "/coddy/providers/openai/opencode-auth"
	for _, body := range []string{`{"proxy":"invalid"}`, `{"api_base":"https://elsewhere.invalid"}`, `{`} {
		w := openCodeAuthRequest(t, srv, "POST", path+"/device", body)
		if w.Code != 400 {
			t.Fatalf("invalid body status=%d", w.Code)
		}
	}
	r := httptest.NewRequest("POST", path+"/device", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatal("non-JSON sign-in was accepted")
	}
	body, _ := json.Marshal(map[string]string{"proxy": proxy.URL})
	w = openCodeAuthRequest(t, srv, "POST", path+"/device", string(body))
	if w.Code != 200 {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	var start codexAuthLoginResponse
	_ = json.Unmarshal(w.Body.Bytes(), &start)
	if other := openCodeAuthRequest(t, srv, "GET", "/coddy/providers/other/opencode-auth/device/"+start.LoginID, ""); other.Code != 404 {
		t.Fatal("another provider read the login attempt")
	}
	openCodeAuthRequest(t, srv, "DELETE", path, "")
	srv.bgWG.Wait()
	if _, err := os.Stat(config.OpenCodeAuthPath(srv.activeCfg().Paths.Home, "openai")); !os.IsNotExist(err) {
		t.Fatal("cancelled login restored a credential")
	}
	// A wait whose context is already cancelled asks Console nothing and stores
	// nothing; TestOpenCodeAuthSignOutBeforeStorageStoresNothing holds the
	// boundary where the tokens are issued and storage has not happened yet.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	login, err := llm.StartOpenCodeDeviceLogin(context.Background(), proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := login.WaitWith(ctx, llm.OpenCodeDefaultAPIBase(), func(context.Context, []byte) error { t.Error("cancelled login persisted"); return nil }); err == nil {
		t.Fatal("cancelled login succeeded")
	}
}

// openCodeAccountProxy is a forward proxy that is Console itself, with a hook
// that runs before each answer: a test parks or cancels an attempt there.
func openCodeAccountProxy(t *testing.T, before func(path string)) (*Server, *httptest.Server) {
	t.Helper()
	var devices atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var n int32
		if r.URL.Path == "/console/auth/device/code" {
			// Numbered on arrival, so a request the hook parks keeps the
			// number of the attempt it belongs to.
			n = devices.Add(1)
		}
		before(r.URL.Path)
		switch r.URL.Path {
		case "/console/auth/device/code":
			_, _ = fmt.Fprintf(w, `{"device_code":"device-%d","user_code":"CODE-%d","verification_uri_complete":"/console/device","expires_in":600,"interval":0}`, n, n)
		case "/console/auth/device/token":
			_, _ = io.WriteString(w, `{"access_token":"private-access","refresh_token":"private-refresh","expires_in":600,"org_id":"org"}`)
		case "/console/api/user":
			_, _ = io.WriteString(w, `{"email":"user@example.com"}`)
		case "/console/api/orgs":
			_, _ = io.WriteString(w, `[{"id":"org","name":"Test Org"}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(proxy.Close)
	t.Setenv(llm.EnvOpenCodeConsoleURL, "http://opencode.invalid/console")
	srv := newNeuralDeepTestServer(t, t.TempDir())
	srv.activeCfg().Providers = []config.ProviderConfig{{Name: "oc", Type: "opencode", Proxy: proxy.URL}}
	t.Cleanup(func() { srv.cancelOpenCodeAuthLogins(""); srv.bgWG.Wait() })
	return srv, proxy
}

func openCodeLoginState(srv *Server, loginID string) codexAuthLoginResponse {
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/coddy/providers/oc/opencode-auth/device/"+loginID, nil))
	var state codexAuthLoginResponse
	_ = json.Unmarshal(w.Body.Bytes(), &state)
	return state
}

// A second sign-in of the same provider supersedes the first even while the
// first still waits for Console to issue its code: the first answers 409, and
// only the second attempt can store the login.
func TestOpenCodeAuthDeviceStartSupersededWhileContactingConsole(t *testing.T) {
	firstAsked := make(chan struct{})
	release := make(chan struct{})
	var asked atomic.Int32
	srv, _ := openCodeAccountProxy(t, func(path string) {
		if path == "/console/auth/device/code" && asked.Add(1) == 1 {
			close(firstAsked)
			<-release
		}
	})
	defer close(release)
	const path = "/coddy/providers/oc/opencode-auth/device"
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- openCodeAuthRequest(t, srv, "POST", path, "{}") }()
	<-firstAsked
	second := openCodeAuthRequest(t, srv, "POST", path, "{}")
	var start codexAuthLoginResponse
	if err := json.Unmarshal(second.Body.Bytes(), &start); second.Code != 200 || err != nil || start.UserCode != "CODE-2" {
		t.Fatalf("second start: %d %s", second.Code, second.Body)
	}
	select {
	case w := <-first:
		if w.Code != http.StatusConflict {
			t.Fatalf("superseded start: %d %s, want 409", w.Code, w.Body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the superseded start did not return while Console held it")
	}
	deadline := time.Now().Add(3 * time.Second)
	for openCodeLoginState(srv, start.LoginID).Status != "completed" {
		if time.Now().After(deadline) {
			t.Fatalf("surviving login: %+v", openCodeLoginState(srv, start.LoginID))
		}
		time.Sleep(10 * time.Millisecond)
	}
	srv.codexAuthMu.Lock()
	attempts := len(srv.opencodeAuthLogins)
	srv.codexAuthMu.Unlock()
	if attempts != 1 {
		t.Fatalf("%d attempts kept, want the surviving one", attempts)
	}
}

// A sign-out that lands after Console issued the tokens but before they are
// stored wins: the attempt checks its cancellation under the lock sign-out
// takes, so the credential the user just removed never comes back.
func TestOpenCodeAuthSignOutBeforeStorageStoresNothing(t *testing.T) {
	var srv *Server
	orgsAnswered := make(chan struct{})
	srv, _ = openCodeAccountProxy(t, func(path string) {
		if path == "/console/api/orgs" {
			// The tokens are issued and the account is known; the attempt
			// is about to store them and has to take this lock first.
			srv.codexAuthMu.Lock()
			close(orgsAnswered)
		}
	})
	w := openCodeAuthRequest(t, srv, "POST", "/coddy/providers/oc/opencode-auth/device", "{}")
	var start codexAuthLoginResponse
	if err := json.Unmarshal(w.Body.Bytes(), &start); w.Code != 200 || err != nil {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	<-orgsAnswered
	// Let the attempt read the answer and reach the lock, then sign out the
	// way DELETE does, while the attempt waits for it.
	time.Sleep(200 * time.Millisecond)
	for _, attempt := range srv.opencodeAuthLogins {
		attempt.cancel()
	}
	srv.codexAuthMu.Unlock()
	srv.bgWG.Wait()
	if _, err := os.Stat(config.OpenCodeAuthPath(srv.activeCfg().Paths.Home, "oc")); !os.IsNotExist(err) {
		t.Fatal("a cancelled attempt stored its credential")
	}
	if state := openCodeLoginState(srv, start.LoginID); state.Status != "failed" || state.Connected {
		t.Fatalf("cancelled attempt reads %+v", state)
	}
}
