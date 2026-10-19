package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestOpenCodeLoginAccountDefault(t *testing.T) {
	var calls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !r.URL.IsAbs() {
			t.Error("login bypassed the configured proxy")
		}
		calls.Add(1)
		switch r.URL.Path {
		case "/console/auth/device/code":
			_, _ = w.Write([]byte(`{"device_code":"private-code","user_code":"USER","verification_uri_complete":"/console/device","expires_in":600,"interval":0}`))
		case "/console/auth/device/token":
			_, _ = w.Write([]byte(`{"access_token":"access","refresh_token":"refresh","expires_in":600,"org_id":"org"}`))
		case "/console/api/user":
			_, _ = w.Write([]byte(`{"id":"u","email":"u@example.com"}`))
		case "/console/api/orgs":
			_, _ = w.Write([]byte(`[{"id":"org","name":"Test Org"}]`))
		case "/inference/go/v1/models":
			if r.Header.Get("Authorization") != "Bearer access" || r.Header.Get("x-org-id") != "org" {
				t.Error("login config apply lost the account credential")
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"glm-5.3"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer proxy.Close()
	t.Setenv(llm.EnvOpenCodeConsoleURL, "http://opencode.invalid/console")
	previous := localBrowserAvailableFn
	localBrowserAvailableFn = func() bool { return false }
	t.Cleanup(func() { localBrowserAvailableFn = previous })
	root := t.TempDir()
	cfg := &config.Config{}
	cfg.Paths.Home = root
	prov := &config.ProviderConfig{Name: "opencode", Type: "opencode", Proxy: proxy.URL}
	if err := opencodeLogin(cfg, prov, "", true, ""); err != nil {
		t.Fatal(err)
	}
	status, err := llm.InspectOpenCodeAuth(config.OpenCodeAuthPath(root, prov.Name))
	if err != nil || !status.Connected || !status.Account || status.Organization != "Test Org" || calls.Load() != 4 {
		t.Fatal("CLI did not complete account device login")
	}
	if !strings.Contains(opencodeCredentialSummary(cfg, prov), "Test Org") {
		t.Fatal("providers list lost the account organization")
	}
	data, _ := os.ReadFile(config.OpenCodeAuthPath(root, prov.Name))
	var stored map[string]any
	if err := json.Unmarshal(data, &stored); err != nil || stored["api_key"] != nil {
		t.Fatal("account was stored as an API key")
	}
	if _, err := os.Stat(filepath.Join(root, "config.yaml")); !os.IsNotExist(err) {
		t.Fatal("--no-config wrote configuration")
	}
	// The default flow also publishes models using the stored account token,
	// without copying a token into YAML.
	proxyJSON, _ := json.Marshal(proxy.URL)
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("providers:\n  - name: opencode\n    type: opencode\n    proxy: "+string(proxyJSON)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.LoadFromCLI(config.CLIPaths{Home: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(llm.EnvOpenCodeBaseURL, "http://opencode.invalid/inference/go/v1")
	if err := opencodeLogin(cfg, cfg.FindProvider("opencode"), "", false, ""); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadFromCLI(config.CLIPaths{Home: root})
	if err != nil || loaded.FindModelEntry("opencode/glm-5.3") == nil || loaded.Agent.Model != "opencode/glm-5.3" || calls.Load() != 9 {
		t.Fatal("account login did not publish its catalog")
	}
	yaml, _ := os.ReadFile(filepath.Join(root, "config.yaml"))
	if strings.Contains(string(yaml), "refresh") || strings.Contains(string(yaml), "access") {
		t.Fatal("login copied tokens into config.yaml")
	}
}

func TestOpenCodeServiceAccountPrompt(t *testing.T) {
	previous := opencodeKeyInput
	opencodeKeyInput = strings.NewReader("service-key\n")
	t.Cleanup(func() { opencodeKeyInput = previous })
	cfg := &config.Config{}
	cfg.Paths.Home = t.TempDir()
	prov := &config.ProviderConfig{Name: "opencode", Type: "opencode", Proxy: "invalid-proxy"}
	if err := opencodeLogin(cfg, prov, "-", true, ""); err != nil {
		t.Fatal(err)
	}
	key, err := llm.LoadOpenCodeKey(config.OpenCodeAuthPath(cfg.Paths.Home, prov.Name))
	if err != nil || key != "service-key" {
		t.Fatal("service key prompt stopped working")
	}
}

func TestOpenCodeLogoutRemovesUnreadableCredential(t *testing.T) {
	cfg := &config.Config{}
	cfg.Paths.Home = t.TempDir()
	prov := &config.ProviderConfig{Name: "opencode", Type: "opencode"}
	path := config.OpenCodeAuthPath(cfg.Paths.Home, prov.Name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("invalid JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := opencodeLogout(cfg, prov); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("logout retained a damaged credential")
	}
}
