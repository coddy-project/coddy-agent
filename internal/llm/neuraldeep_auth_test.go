package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"time"
)

// fakeNeuralDeepHub emulates the hub side of the browser callback flow: the
// start endpoint answers, like production, with an HTML page whose script
// (and fallback link) point at the loopback callback carrying state and key.
func fakeNeuralDeepHub(t *testing.T, key string, wrongStateFirst bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/cli/auth/start":
			q := r.URL.Query()
			port, state := q.Get("port"), q.Get("state")
			if q.Get("client") != "coddy" {
				t.Errorf("client = %q, want coddy", q.Get("client"))
			}
			cb := fmt.Sprintf("http://127.0.0.1:%s/cb?state=%s&key=%s", port, url.QueryEscape(state), url.QueryEscape(key))
			if wrongStateFirst {
				cb = fmt.Sprintf("http://127.0.0.1:%s/cb?state=WRONG&key=%s", port, url.QueryEscape("sk-stolen"))
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprintf(w, `<!doctype html><meta charset=utf-8><body><h2>connected</h2><a href="%s">continue</a><script>location.replace(%q)</script></body>`, cb, cb)
		case "/api/cli/whoami":
			if r.Header.Get("Authorization") != "Bearer "+key {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"email": "u@example.com", "name": "tester", "tier": "starter"})
		default:
			http.NotFound(w, r)
		}
	}))
}

// browseLikeAUser fetches the auth URL and follows the HTML fallback link the
// way a browser executes location.replace. Production answers with an HTML
// page, not a 302, so the test walks the same contract.
func browseLikeAUser(t *testing.T, authURL string) *http.Response {
	t.Helper()
	resp, err := http.Get(authURL)
	if err != nil {
		t.Fatalf("open auth url: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	m := regexp.MustCompile(`href="([^"]+)"`).FindSubmatch(body)
	if m == nil {
		t.Fatalf("no callback link in hub page: %s", body)
	}
	cbResp, err := http.Get(string(m[1]))
	if err != nil {
		t.Fatalf("follow callback: %v", err)
	}
	t.Cleanup(func() { _ = cbResp.Body.Close() })
	return cbResp
}

func TestNeuralDeepSignInStoresKeyFromCallback(t *testing.T) {
	hub := fakeNeuralDeepHub(t, "sk-hub-key", false)
	defer hub.Close()
	authPath := filepath.Join(t.TempDir(), "providers", "nd", "neuraldeep-auth.json")

	done := make(chan struct{})
	key, err := NeuralDeepSignIn(context.Background(), hub.URL, hub.Client(), authPath, func(p NeuralDeepLoginPrompt) {
		go func() {
			defer close(done)
			resp := browseLikeAUser(t, p.AuthURL)
			if resp.StatusCode != http.StatusOK {
				t.Errorf("callback status = %d, want 200", resp.StatusCode)
			}
		}()
	})
	if err != nil {
		t.Fatalf("NeuralDeepSignIn: %v", err)
	}
	<-done
	if key != "sk-hub-key" {
		t.Fatalf("key = %q, want sk-hub-key", key)
	}

	data, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("auth file: %v", err)
	}
	var f struct {
		APIKey string `json:"api_key"`
		Hub    string `json:"hub"`
		Client string `json:"client"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("auth file json: %v", err)
	}
	if f.APIKey != "sk-hub-key" || f.Client != "coddy" || f.Hub != hub.URL {
		t.Fatalf("auth file = %+v", f)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(authPath); fi.Mode().Perm() != 0o600 {
			t.Fatalf("auth file mode = %v, want 0600", fi.Mode().Perm())
		}
		if di, _ := os.Stat(filepath.Dir(authPath)); di.Mode().Perm() != 0o700 {
			t.Fatalf("auth dir mode = %v, want 0700", di.Mode().Perm())
		}
	}

	st, err := InspectNeuralDeepAuth(authPath)
	if err != nil {
		t.Fatalf("InspectNeuralDeepAuth: %v", err)
	}
	if !st.Connected || st.Masked == "" || strings.Contains(st.Masked, "sk-hub-key") {
		t.Fatalf("status = %+v, want connected with a masked key", st)
	}
}

func TestNeuralDeepSignInWrongStateKeepsWaiting(t *testing.T) {
	// A callback with a foreign state must answer 400, write nothing, and keep
	// the listener alive: otherwise any local process can abort a login (DoS)
	// or plant its own key.
	hub := fakeNeuralDeepHub(t, "sk-real", false)
	defer hub.Close()
	wrongHub := fakeNeuralDeepHub(t, "sk-planted", true)
	defer wrongHub.Close()
	authPath := filepath.Join(t.TempDir(), "nd", "neuraldeep-auth.json")

	key, err := NeuralDeepSignIn(context.Background(), hub.URL, hub.Client(), authPath, func(p NeuralDeepLoginPrompt) {
		go func() {
			// First attempt: same port, wrong state (the wrongHub start page
			// links to state=WRONG). Must be rejected without ending the wait.
			u, _ := url.Parse(p.AuthURL)
			q := u.Query()
			wrongURL := wrongHub.URL + "/api/cli/auth/start?" + url.Values{
				"port": {q.Get("port")}, "state": {q.Get("state")}, "client": {"coddy"},
			}.Encode()
			resp := browseLikeAUser(t, wrongURL)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("wrong-state callback status = %d, want 400", resp.StatusCode)
			}
			if _, err := os.Stat(authPath); !os.IsNotExist(err) {
				t.Errorf("auth file must not exist after a rejected callback")
			}
			// Second attempt: the honest flow completes.
			_ = browseLikeAUser(t, p.AuthURL)
		}()
	})
	if err != nil {
		t.Fatalf("NeuralDeepSignIn: %v", err)
	}
	if key != "sk-real" {
		t.Fatalf("key = %q, want sk-real (the planted key must lose)", key)
	}
}

func TestNeuralDeepSignInContextTimeout(t *testing.T) {
	hub := fakeNeuralDeepHub(t, "sk-late", false)
	defer hub.Close()
	authPath := filepath.Join(t.TempDir(), "nd", "neuraldeep-auth.json")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	_, err := NeuralDeepSignIn(ctx, hub.URL, hub.Client(), authPath, func(NeuralDeepLoginPrompt) {})
	if err == nil {
		t.Fatal("want a timeout error when nobody completes the login")
	}
	if _, statErr := os.Stat(authPath); !os.IsNotExist(statErr) {
		t.Fatal("auth file must not exist after a timed-out login")
	}
}

func TestNeuralDeepAuthFileLifecycle(t *testing.T) {
	authPath := filepath.Join(t.TempDir(), "providers", "nd", "neuraldeep-auth.json")

	if st, err := InspectNeuralDeepAuth(authPath); err != nil || st.Connected {
		t.Fatalf("missing file: status=%+v err=%v, want disconnected, no error", st, err)
	}
	if err := SaveNeuralDeepAuth(authPath, "sk-abcdef123456", "https://hub.example", "coddy", "coddy"); err != nil {
		t.Fatalf("SaveNeuralDeepAuth: %v", err)
	}
	key, err := LoadNeuralDeepKey(authPath)
	if err != nil || key != "sk-abcdef123456" {
		t.Fatalf("LoadNeuralDeepKey = %q, %v", key, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(authPath))
	for _, e := range entries {
		if strings.Contains(e.Name(), "tmp") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
	if err := RemoveNeuralDeepAuth(authPath); err != nil {
		t.Fatalf("RemoveNeuralDeepAuth: %v", err)
	}
	if _, err := os.Stat(authPath); !os.IsNotExist(err) {
		t.Fatal("auth file still present after remove")
	}
	if err := RemoveNeuralDeepAuth(authPath); err != nil {
		t.Fatalf("second remove must be a no-op, got %v", err)
	}
}

// shrinkNeuralDeepPolling makes the RFC pacing test-friendly: the production
// floor guards against busy-looping, the slow_down step follows the RFC.
func shrinkNeuralDeepPolling(t *testing.T) {
	t.Helper()
	prevFloor, prevStep := neuralDeepPollFloor, neuralDeepSlowDownStep
	neuralDeepPollFloor, neuralDeepSlowDownStep = 5*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { neuralDeepPollFloor, neuralDeepSlowDownStep = prevFloor, prevStep })
}

func TestNeuralDeepDeviceSignInPollsUntilApproved(t *testing.T) {
	shrinkNeuralDeepPolling(t)
	var polls int
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/cli/device/start":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["client"] != "coddy" {
				t.Errorf("device client = %q, want coddy", body["client"])
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code": "dev-1", "user_code": "BCDF-GHJK",
				"verification_uri":          "http://hub/app/device",
				"verification_uri_complete": "http://hub/app/device?code=BCDF-GHJK",
				"interval":                  0, "expires_in": 900,
			})
		case "/api/cli/device/token":
			polls++
			w.Header().Set("Content-Type", "application/json")
			switch polls {
			case 1:
				w.WriteHeader(http.StatusBadRequest)
				_, _ = fmt.Fprint(w, `{"error":"authorization_pending"}`)
			case 2:
				w.WriteHeader(http.StatusBadRequest)
				_, _ = fmt.Fprint(w, `{"error":"slow_down"}`)
			default:
				_, _ = fmt.Fprint(w, `{"access_token":"sk-device","token_type":"bearer","label":"coddy @ host"}`)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer hub.Close()

	authPath := filepath.Join(t.TempDir(), "nd", "neuraldeep-auth.json")
	var prompt NeuralDeepDeviceLogin
	key, err := NeuralDeepDeviceSignIn(context.Background(), hub.URL, hub.Client(), authPath, "coddy @ host",
		func(l NeuralDeepDeviceLogin) { prompt = l })
	if err != nil {
		t.Fatalf("NeuralDeepDeviceSignIn: %v", err)
	}
	if key != "sk-device" {
		t.Fatalf("key = %q, want sk-device", key)
	}
	if prompt.UserCode != "BCDF-GHJK" || prompt.VerificationURIComplete == "" {
		t.Fatalf("prompt = %+v, want user code and verification URI", prompt)
	}
	if polls < 3 {
		t.Fatalf("polls = %d, want pending and slow_down handled before success", polls)
	}
	if key, _ := LoadNeuralDeepKey(authPath); key != "sk-device" {
		t.Fatalf("auth file key = %q, want sk-device", key)
	}
}

func TestNeuralDeepDeviceSignInDenied(t *testing.T) {
	shrinkNeuralDeepPolling(t)
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/cli/device/start":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code": "dev-2", "user_code": "XXXX-YYYY",
				"verification_uri": "http://hub/app/device", "interval": 0, "expires_in": 900,
			})
		case "/api/cli/device/token":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, `{"error":"access_denied"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer hub.Close()

	authPath := filepath.Join(t.TempDir(), "nd", "neuraldeep-auth.json")
	_, err := NeuralDeepDeviceSignIn(context.Background(), hub.URL, hub.Client(), authPath, "", func(NeuralDeepDeviceLogin) {})
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("err = %v, want access denied", err)
	}
	if _, statErr := os.Stat(authPath); !os.IsNotExist(statErr) {
		t.Fatal("auth file must not exist after a denied login")
	}
}

func TestNeuralDeepWhoamiAndRevoke(t *testing.T) {
	var revoked bool
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/cli/whoami":
			_ = json.NewEncoder(w).Encode(map[string]string{"email": "u@e", "name": "tester", "tier": "pro"})
		case "/api/cli/status":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tier": "pro",
				"models": []map[string]any{
					{"id": "qwen3.6-35b-a3b", "mode": "chat", "ctx": 262144},
					{"id": "gpt-oss-120b", "mode": "chat", "ctx": 131072},
				},
			})
		case "/api/cli/revoke":
			if r.Method != http.MethodPost {
				t.Errorf("revoke method = %s, want POST", r.Method)
			}
			revoked = true
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "revoked": "coddy"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer hub.Close()

	who, err := FetchNeuralDeepWhoami(context.Background(), hub.URL, "sk-k", hub.Client())
	if err != nil || who.Name != "tester" || who.Tier != "pro" {
		t.Fatalf("whoami = %+v, %v", who, err)
	}
	st, err := FetchNeuralDeepStatus(context.Background(), hub.URL, "sk-k", hub.Client())
	if err != nil || len(st.Models) != 2 || st.Models[0].ID != "qwen3.6-35b-a3b" {
		t.Fatalf("status = %+v, %v", st, err)
	}
	if err := RevokeNeuralDeepKey(context.Background(), hub.URL, "sk-k", hub.Client()); err != nil || !revoked {
		t.Fatalf("revoke: %v (revoked=%v)", err, revoked)
	}
}

func TestRedactNeuralDeepSecrets(t *testing.T) {
	in := `list models: HTTP 401: {"detail":"bad key sk-abc123XYZ"} for /cb?state=x&key=sk-abc123XYZ`
	out := redactNeuralDeepSecrets(in)
	if strings.Contains(out, "sk-abc123XYZ") {
		t.Fatalf("secret survived redaction: %s", out)
	}
	if !strings.Contains(out, "sk-***") {
		t.Fatalf("redaction marker missing: %s", out)
	}
}

func TestNewProviderNeuralDeepFallsBackToAuthFile(t *testing.T) {
	var gotAuth string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "hi"}, "finish_reason": "stop"}},
		})
	}))
	defer api.Close()
	t.Setenv(EnvNeuralDeepBaseURL, api.URL)

	authPath := filepath.Join(t.TempDir(), "nd", "neuraldeep-auth.json")
	if err := SaveNeuralDeepAuth(authPath, "sk-from-file", "https://hub.example", "coddy", "coddy"); err != nil {
		t.Fatal(err)
	}
	p, err := NewProvider(ProviderInput{Type: "neuraldeep", Model: "m", AuthPath: authPath, RetryMax: 0})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if _, err := p.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if gotAuth != "Bearer sk-from-file" {
		t.Fatalf("Authorization = %q, want the auth-file key", gotAuth)
	}
}

func TestListModelsNeuralDeepFallsBackToAuthFile(t *testing.T) {
	var gotAuth string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		_, _ = fmt.Fprint(w, `{"data":[{"id":"qwen3.6-35b-a3b"},{"id":"gpt-oss-120b"}]}`)
	}))
	defer api.Close()
	t.Setenv(EnvNeuralDeepBaseURL, api.URL)

	authPath := filepath.Join(t.TempDir(), "nd", "neuraldeep-auth.json")
	if err := SaveNeuralDeepAuth(authPath, "sk-file", "https://hub.example", "coddy", "coddy"); err != nil {
		t.Fatal(err)
	}
	models, err := ListModels(context.Background(), ProviderInput{Type: "neuraldeep", AuthPath: authPath})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 || gotAuth != "Bearer sk-file" {
		t.Fatalf("models=%v auth=%q, want 2 models fetched with the file key", models, gotAuth)
	}
}

func TestApplyNeuralDeepLoginRecordsTheMirrorEndpoint(t *testing.T) {
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/cli/status" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tier":   "starter",
			"models": []map[string]any{{"id": "qwen3.6-35b-a3b", "ctx": 262144}},
		})
	}))
	defer hub.Close()

	mirror := "https://api.neuraldeep.tech/v1"
	for _, tc := range []struct {
		name       string
		apiBase    string
		wantRecord bool
	}{
		{"the mirror is recorded on the new row", mirror, true},
		{"the default deployment stays implicit", "", false},
		{"an unknown endpoint is not recorded", "https://example.invalid/v1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			cfgPath := filepath.Join(home, "config.yaml")
			cfg := &config.Config{}
			cfg.Paths = config.Paths{Home: home, ConfigPath: cfgPath}

			if _, err := ApplyNeuralDeepLoginToConfig(context.Background(), cfg, "neuraldeep", hub.URL, tc.apiBase, "sk-k", hub.Client()); err != nil {
				t.Fatalf("ApplyNeuralDeepLoginToConfig: %v", err)
			}
			raw, err := os.ReadFile(cfgPath)
			if err != nil {
				t.Fatalf("read config: %v", err)
			}
			if got := strings.Contains(string(raw), mirror); got != tc.wantRecord {
				t.Fatalf("api_base %q recorded = %v, want %v; config:\n%s", tc.apiBase, got, tc.wantRecord, raw)
			}
		})
	}
}

func TestNeuralDeepAuthNoticesFlagEndpointProblems(t *testing.T) {
	home := t.TempDir()
	authPath := config.NeuralDeepAuthPath(home, "neuraldeep")
	if err := SaveNeuralDeepAuth(authPath, "sk-abcdefgh1234", NeuralDeepHubURL, NeuralDeepClientID, NeuralDeepClientID); err != nil {
		t.Fatalf("SaveNeuralDeepAuth: %v", err)
	}
	cfg := &config.Config{Providers: []config.ProviderConfig{
		{Name: "neuraldeep", Type: "neuraldeep", APIBase: "https://example.invalid/v1"},
	}}
	cfg.Paths = config.Paths{Home: home}

	messages := func() string {
		var joined string
		for _, n := range NeuralDeepAuthNotices(cfg) {
			joined += n.Message + "\n"
		}
		return joined
	}

	if got := messages(); !strings.Contains(got, "is not a NeuralDeep endpoint") {
		t.Errorf("an unrecognized api_base must be reported, got:\n%s", got)
	}

	// The stored key came from the default hub, so pointing the row at the
	// mirror leaves it signed in to the wrong deployment.
	cfg.Providers[0].APIBase = "https://api.neuraldeep.tech/v1"
	if got := messages(); !strings.Contains(got, "but requests go to https://api.neuraldeep.tech/v1") {
		t.Errorf("a hub/endpoint mismatch must be reported, got:\n%s", got)
	}

	// Hub and endpoint agree: no endpoint complaints at all.
	cfg.Providers[0].APIBase = ""
	for _, n := range NeuralDeepAuthNotices(cfg) {
		if strings.Contains(n.Message, "endpoint") {
			t.Errorf("unexpected endpoint notice: %s", n.Message)
		}
	}
}

func TestApplyNeuralDeepLoginSkipsUnsafeModelIDs(t *testing.T) {
	// Model ids are interpolated into UCI config paths; a hostile hub must
	// not be able to smuggle selector syntax through the catalog.
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/cli/status" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tier": "starter",
			"models": []map[string]any{
				{"id": "qwen3.6-35b-a3b", "ctx": 262144},
				{"id": "evil]x.hack=1", "ctx": 1},
				{"id": "also/bad", "ctx": 1},
				{"id": "", "ctx": 1},
			},
		})
	}))
	defer hub.Close()

	home := t.TempDir()
	cfgPath := filepath.Join(home, "config.yaml")
	cfg := &config.Config{}
	cfg.Paths = config.Paths{Home: home, ConfigPath: cfgPath}

	added, err := ApplyNeuralDeepLoginToConfig(context.Background(), cfg, "neuraldeep", hub.URL, "", "sk-k", hub.Client())
	if err != nil {
		t.Fatalf("ApplyNeuralDeepLoginToConfig: %v", err)
	}
	joined := strings.Join(added, ",")
	if !strings.Contains(joined, "model neuraldeep/qwen3.6-35b-a3b") {
		t.Fatalf("safe model missing from %q", joined)
	}
	if strings.Contains(joined, "evil") || strings.Contains(joined, "also/bad") {
		t.Fatalf("unsafe model ids must be skipped, got %q", joined)
	}
}

// TestApplyNeuralDeepLoginMovesAnExistingRowToTheLoginEndpoint: a login made
// with --api-base against a row that names another deployment leaves the row
// on the login's endpoint, otherwise the freshly minted key would be sent to
// the hub that never issued it.
func TestApplyNeuralDeepLoginMovesAnExistingRowToTheLoginEndpoint(t *testing.T) {
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/cli/status" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tier":   "starter",
			"models": []map[string]any{{"id": "qwen3.6-35b-a3b", "ctx": 262144}},
		})
	}))
	defer hub.Close()

	mirror := "https://api.neuraldeep.tech/v1"
	for _, tc := range []struct {
		name     string
		rowBase  string
		apiBase  string
		wantBase string
		wantNote bool
	}{
		{"default row moves to the mirror", "", mirror, mirror, true},
		{"mirror row moves back to the default", mirror, neuralDeepBaseURL, "", true},
		{"a row already on the login endpoint is untouched", mirror, mirror, mirror, false},
		{"no endpoint given leaves the row alone", mirror, "", mirror, false},
		{"an unrecognized row value is replaced by the login endpoint", "https://example.invalid/v1", mirror, mirror, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			cfgPath := filepath.Join(home, "config.yaml")
			yaml := "providers:\n  - name: neuraldeep\n    type: neuraldeep\n"
			if tc.rowBase != "" {
				yaml += "    api_base: " + tc.rowBase + "\n"
			}
			if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.LoadFromCLI(config.CLIPaths{Home: home})
			if err != nil {
				t.Fatalf("load config: %v", err)
			}

			added, err := ApplyNeuralDeepLoginToConfig(context.Background(), cfg, "neuraldeep", hub.URL, tc.apiBase, "sk-k", hub.Client())
			if err != nil {
				t.Fatalf("ApplyNeuralDeepLoginToConfig: %v", err)
			}
			var noted bool
			for _, a := range added {
				if strings.Contains(a, "api_base") {
					noted = true
				}
			}
			if noted != tc.wantNote {
				t.Fatalf("api_base change reported = %v, want %v; added %v", noted, tc.wantNote, added)
			}
			reloaded, err := config.LoadFromCLI(config.CLIPaths{Home: home})
			if err != nil {
				t.Fatalf("reload config: %v", err)
			}
			prov := reloaded.FindProvider("neuraldeep")
			if prov == nil || prov.APIBase != tc.wantBase {
				t.Fatalf("row after login = %+v, want api_base %q", prov, tc.wantBase)
			}
		})
	}
}
