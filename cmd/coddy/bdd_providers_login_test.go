package main

// Godog harness for features/neuraldeep_login_flow.feature: drives
// `coddy providers login neuraldeep` against a stand-in hub that serves both
// sign-in flows, with the machine's browser and terminal faked, and inspects
// what the command printed and stored.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

const (
	loginFeatureDeviceKey  = "sk-from-the-device-flow"
	loginFeatureBrowserKey = "sk-from-the-callback"
	loginFeatureUserCode   = "BCDF-GHJK"
)

type loginFlowState struct {
	hub         *httptest.Server
	home        string
	stdout      string
	runErr      error
	opened      []string
	mu          sync.Mutex
	polls       int
	prevOpen    func(string) error
	prevBrow    func() bool
	prevHub     string
	hadHub      bool
	restoreWait func()
	browserErr  error
}

func (s *loginFlowState) reset() error {
	s.close()
	home, err := os.MkdirTemp("", "coddy-bdd-login-*")
	if err != nil {
		return err
	}
	s.home = home
	s.stdout = ""
	s.runErr = nil
	s.opened = nil
	s.polls = 0
	s.prevOpen, s.prevBrow = openBrowserFn, localBrowserAvailableFn
	s.prevHub, s.hadHub = os.LookupEnv(llm.EnvNeuralDeepHubURL)
	s.browserErr = nil
	// A sign-in that never gets its answer waits fifteen minutes in production;
	// here that would outlive `go test` and kill the package instead of failing
	// one scenario.
	s.restoreWait = llm.SetNeuralDeepLoginTimeout(20 * time.Second)
	openBrowserFn = func(url string) error {
		s.mu.Lock()
		s.opened = append(s.opened, url)
		s.mu.Unlock()
		return nil
	}
	localBrowserAvailableFn = func() bool { return false }
	return nil
}

func (s *loginFlowState) close() {
	if s.hub != nil {
		s.hub.Close()
		s.hub = nil
	}
	if s.home != "" {
		_ = os.RemoveAll(s.home)
		s.home = ""
	}
	if s.restoreWait != nil {
		s.restoreWait()
		s.restoreWait = nil
	}
	if s.prevOpen != nil {
		openBrowserFn, localBrowserAvailableFn = s.prevOpen, s.prevBrow
		s.prevOpen, s.prevBrow = nil, nil
		// The hub override is process-wide: leaving it behind would point the
		// next test in this package at a closed server.
		if s.hadHub {
			_ = os.Setenv(llm.EnvNeuralDeepHubURL, s.prevHub)
		} else {
			_ = os.Unsetenv(llm.EnvNeuralDeepHubURL)
		}
	}
}

// standInHub serves the two sign-in flows plus the catalog calls a login makes
// afterwards. The device flow answers "pending" once, so the poller has to do
// what a real client does rather than reading the key off the first reply.
func (s *loginFlowState) standInHub() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/cli/auth/start", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		cb := fmt.Sprintf("http://127.0.0.1:%s/cb?state=%s&key=%s", q.Get("port"), q.Get("state"), loginFeatureBrowserKey)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, `<html><body><a href="%s">continue</a></body></html>`, cb)
	})
	mux.HandleFunc("/api/cli/device/start", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code":               "dc-secret",
			"user_code":                 loginFeatureUserCode,
			"verification_uri":          s.hub.URL + "/device",
			"verification_uri_complete": s.hub.URL + "/device?code=" + loginFeatureUserCode,
			"interval":                  1,
			"expires_in":                900,
		})
	})
	mux.HandleFunc("/api/cli/device/token", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		s.polls++
		first := s.polls == 1
		s.mu.Unlock()
		if first {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": loginFeatureDeviceKey, "token_type": "bearer", "label": "coddy @ host",
		})
	})
	mux.HandleFunc("/api/cli/whoami", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"email": "u@e", "name": "tester", "tier": "starter"})
	})
	mux.HandleFunc("/api/cli/status", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tier": "starter",
			"models": []map[string]any{
				{"id": "qwen3.6-35b-a3b", "mode": "chat", "ctx": 262144},
			},
		})
	})
	s.hub = httptest.NewServer(mux)
	return os.Setenv(llm.EnvNeuralDeepHubURL, s.hub.URL)
}

func (s *loginFlowState) noLocalBrowser() error {
	localBrowserAvailableFn = func() bool { return false }
	return nil
}

// aLocalBrowser also plays the part of the person at the keyboard for the
// callback flow: the browser it "opens" follows the hub's link back to the
// loopback listener.
func (s *loginFlowState) aLocalBrowser() error {
	localBrowserAvailableFn = func() bool { return true }
	openBrowserFn = func(url string) error {
		s.mu.Lock()
		s.opened = append(s.opened, url)
		s.mu.Unlock()
		if strings.Contains(url, "/api/cli/auth/start") {
			go func() {
				if err := followCallbackLink(url); err != nil {
					s.mu.Lock()
					s.browserErr = err
					s.mu.Unlock()
				}
			}()
		}
		return nil
	}
	return nil
}

// followCallbackLink plays the browser for the callback flow: it opens the
// hub page and follows the link back to the loopback listener. Every failure
// is reported, because a callback that never lands shows up as a sign-in that
// waits rather than as a step that fails.
func followCallbackLink(authURL string) error {
	resp, err := getWithRetries(authURL)
	if err != nil {
		return fmt.Errorf("open the hub page: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	m := regexp.MustCompile(`href="([^"]+)"`).FindSubmatch(body)
	if m == nil {
		return fmt.Errorf("no callback link in the hub page: %s", body)
	}
	cb, err := getWithRetries(string(m[1]))
	if err != nil {
		return fmt.Errorf("follow the callback: %w", err)
	}
	_ = cb.Body.Close()
	return nil
}

func getWithRetries(url string) (*http.Response, error) {
	var resp *http.Response
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		resp, err = http.Get(url) //nolint:gosec // the stand-in hub and this flow's own loopback listener
		if err == nil {
			return resp, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, err
}

func (s *loginFlowState) runLogin(extra ...string) error {
	args := append([]string{"login", "neuraldeep", "--home", s.home}, extra...)
	out, err := captureStdout(func() { s.runErr = runProviders(args) })
	if err != nil {
		return err
	}
	s.stdout = out
	return nil
}

func (s *loginFlowState) runSignIn() error        { return s.runLogin() }
func (s *loginFlowState) runSignInBrowser() error { return s.runLogin("--browser") }

// captureStdout runs fn with os.Stdout replaced by a pipe and returns what it
// printed. The sign-in's whole contract on a headless machine is what it puts
// on this stream.
func captureStdout(fn func()) (string, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}
	prev := os.Stdout
	os.Stdout = w
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()
	fn()
	os.Stdout = prev
	_ = w.Close()
	<-done
	_ = r.Close()
	return buf.String(), nil
}

func (s *loginFlowState) printsVerificationPageAndCode() error {
	if s.runErr != nil {
		return fmt.Errorf("sign-in failed: %w", s.runErr)
	}
	want := s.hub.URL + "/device?code=" + loginFeatureUserCode
	if !strings.Contains(s.stdout, want) {
		return fmt.Errorf("output does not name the verification page %q:\n%s", want, s.stdout)
	}
	if !strings.Contains(s.stdout, loginFeatureUserCode) {
		return fmt.Errorf("output does not print the code %q:\n%s", loginFeatureUserCode, s.stdout)
	}
	return nil
}

func (s *loginFlowState) noBrowserWasOpened() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.opened) != 0 {
		return fmt.Errorf("opened %v on a machine with no browser", s.opened)
	}
	return nil
}

func (s *loginFlowState) verificationPageWasOpened() error {
	if s.runErr != nil {
		return fmt.Errorf("sign-in failed: %w", s.runErr)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.opened {
		if strings.Contains(u, "/device?code="+loginFeatureUserCode) {
			return nil
		}
	}
	return fmt.Errorf("verification page was not opened, opened %v", s.opened)
}

func (s *loginFlowState) storedKeyIs(want string) error {
	s.mu.Lock()
	browserErr := s.browserErr
	s.mu.Unlock()
	if browserErr != nil {
		return fmt.Errorf("the stand-in browser never completed the callback: %w", browserErr)
	}
	if s.runErr != nil {
		return fmt.Errorf("sign-in failed: %w", s.runErr)
	}
	path := config.NeuralDeepAuthPath(s.home, "neuraldeep")
	key, err := llm.LoadNeuralDeepKey(path)
	if err != nil {
		return fmt.Errorf("read stored credential: %w", err)
	}
	if key != want {
		return fmt.Errorf("stored key = %q, want %q", key, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		return fmt.Errorf("credential mode = %v, want 0600", mode)
	}
	return nil
}

func (s *loginFlowState) storedDeviceKey() error  { return s.storedKeyIs(loginFeatureDeviceKey) }
func (s *loginFlowState) storedBrowserKey() error { return s.storedKeyIs(loginFeatureBrowserKey) }

func (s *loginFlowState) configGainedProviderAndModels() error {
	cfg, err := config.LoadFromCLI(config.CLIPaths{Home: s.home})
	if err != nil {
		return err
	}
	if prov := cfg.FindProvider("neuraldeep"); prov == nil || prov.Type != "neuraldeep" {
		return fmt.Errorf("provider not written to config: %+v", cfg.Providers)
	}
	if cfg.FindModelEntry("neuraldeep/qwen3.6-35b-a3b") == nil {
		return fmt.Errorf("tier models not written, models = %+v", cfg.Models)
	}
	return nil
}

func initializeLoginFlowScenario(sc *godog.ScenarioContext) {
	s := &loginFlowState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a stand-in NeuralDeep hub that serves both sign-in flows$`, s.standInHub)
	sc.Step(`^this machine has no local browser$`, s.noLocalBrowser)
	sc.Step(`^this machine has a local browser$`, s.aLocalBrowser)
	sc.Step(`^I run the terminal sign-in to NeuralDeep$`, s.runSignIn)
	sc.Step(`^I run the terminal sign-in to NeuralDeep with --browser$`, s.runSignInBrowser)
	sc.Step(`^the sign-in prints the verification page and the code to confirm$`, s.printsVerificationPageAndCode)
	sc.Step(`^no browser was opened$`, s.noBrowserWasOpened)
	sc.Step(`^the verification page was opened in the browser$`, s.verificationPageWasOpened)
	sc.Step(`^the stored key is the one the device flow issued$`, s.storedDeviceKey)
	sc.Step(`^the stored key is the one the browser callback issued$`, s.storedBrowserKey)
	sc.Step(`^the config gains the neuraldeep provider and its tier models$`, s.configGainedProviderAndModels)
}

func TestNeuralDeepLoginFlowFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "neuraldeep_login_flow",
		ScenarioInitializer: initializeLoginFlowScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/neuraldeep_login_flow.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("neuraldeep_login_flow feature failed")
	}
}
