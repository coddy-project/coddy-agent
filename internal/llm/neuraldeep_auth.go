package llm

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// NeuralDeep hub sign-in. Two flows share the stored credential:
//
//   - browser callback (CLI): a loopback server on 127.0.0.1 receives the
//     per-user key the hub issues after the user signs in with their account
//     (contract: GET {hub}/api/cli/auth/start?port&state&client);
//   - device flow (SPA settings, headless): RFC 8628 against
//     {hub}/api/cli/device/*, for machines where the browser cannot reach
//     the process's loopback interface.
//
// The key is an ordinary per-user LiteLLM key; tier, limits, and the model
// catalog are enforced by the gateway, so Coddy only stores and presents it.
const (
	// NeuralDeepHubURL is the production hub that issues code-agent keys.
	NeuralDeepHubURL = "https://hub.neuraldeep.ru"
	// EnvNeuralDeepHubURL overrides the hub for stands and tests.
	EnvNeuralDeepHubURL = "CODDY_NEURALDEEP_HUB_URL"
	// EnvNeuralDeepBaseURL overrides the pinned NeuralDeep API base for stands
	// and tests (mirrors CODDY_CODEX_BASE_URL).
	EnvNeuralDeepBaseURL = "CODDY_NEURALDEEP_BASE_URL"
	// NeuralDeepClientID names the key the hub mints for Coddy. It is in the
	// hub's client allowlist; a hub that predates it silently falls back to
	// the "coddy-cli" key name in the browser flow.
	NeuralDeepClientID = "coddy"

	neuralDeepLoginTimeout = 15 * time.Minute
)

// NeuralDeepHub returns the hub base URL, honoring the env override.
func NeuralDeepHub() string {
	if v := strings.TrimSpace(os.Getenv(EnvNeuralDeepHubURL)); v != "" {
		return strings.TrimRight(v, "/")
	}
	return NeuralDeepHubURL
}

func neuralDeepAPIBase() string {
	if v := strings.TrimSpace(os.Getenv(EnvNeuralDeepBaseURL)); v != "" {
		return strings.TrimRight(v, "/")
	}
	return neuralDeepBaseURL
}

// neuralDeepAuthFile is the stored credential. The key is the only secret;
// the rest exists so status displays and logout know where it came from.
type neuralDeepAuthFile struct {
	APIKey     string `json:"api_key"`
	Hub        string `json:"hub"`
	Client     string `json:"client"`
	KeyName    string `json:"key_name,omitempty"`
	ObtainedAt string `json:"obtained_at"`
}

// neuralDeepAuthMu serializes credential file access process-wide, mirroring
// codexAuthMu.
var neuralDeepAuthMu sync.Mutex

// SaveNeuralDeepAuth writes the credential file atomically: a temp file in
// the destination directory gets 0600 before content, then replaces the
// target via rename, so a crash never leaves a truncated credential and the
// key never exists on disk with looser permissions.
func SaveNeuralDeepAuth(path, apiKey, hub, client, keyName string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("neuraldeep auth: credential path is empty")
	}
	if strings.TrimSpace(apiKey) == "" {
		return errors.New("neuraldeep auth: refusing to store an empty key")
	}
	data, err := json.MarshalIndent(neuralDeepAuthFile{
		APIKey:     apiKey,
		Hub:        strings.TrimRight(hub, "/"),
		Client:     client,
		KeyName:    keyName,
		ObtainedAt: time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	if err != nil {
		return err
	}
	neuralDeepAuthMu.Lock()
	defer neuralDeepAuthMu.Unlock()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".neuraldeep-auth-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func loadNeuralDeepAuth(path string) (*neuralDeepAuthFile, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	neuralDeepAuthMu.Lock()
	defer neuralDeepAuthMu.Unlock()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f neuralDeepAuthFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("neuraldeep auth: parse %s: %w", path, err)
	}
	return &f, nil
}

// LoadNeuralDeepKey returns the stored hub key, or "" when no login exists.
func LoadNeuralDeepKey(path string) (string, error) {
	f, err := loadNeuralDeepAuth(path)
	if err != nil || f == nil {
		return "", err
	}
	return strings.TrimSpace(f.APIKey), nil
}

// RemoveNeuralDeepAuth deletes the credential file; a missing file is fine.
func RemoveNeuralDeepAuth(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("neuraldeep auth: credential path is empty")
	}
	neuralDeepAuthMu.Lock()
	defer neuralDeepAuthMu.Unlock()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// NeuralDeepAuthStatus reports the stored login without exposing the key.
type NeuralDeepAuthStatus struct {
	Connected bool
	Masked    string
	KeyName   string
	Hub       string
}

// InspectNeuralDeepAuth reads the credential file into a display-safe status.
func InspectNeuralDeepAuth(path string) (NeuralDeepAuthStatus, error) {
	f, err := loadNeuralDeepAuth(path)
	if err != nil {
		return NeuralDeepAuthStatus{}, err
	}
	if f == nil || strings.TrimSpace(f.APIKey) == "" {
		return NeuralDeepAuthStatus{}, nil
	}
	return NeuralDeepAuthStatus{
		Connected: true,
		Masked:    maskNeuralDeepKey(f.APIKey),
		KeyName:   f.KeyName,
		Hub:       f.Hub,
	}, nil
}

// maskNeuralDeepKey renders sk-…last4 without revealing the key.
func maskNeuralDeepKey(key string) string {
	key = strings.TrimSpace(key)
	if len(key) <= 10 {
		return "sk-…"
	}
	return key[:5] + "…" + key[len(key)-4:]
}

var neuralDeepSecretRe = regexp.MustCompile(`sk-[A-Za-z0-9_-]+`)

// redactNeuralDeepSecrets masks hub keys wherever they may surface: upstream
// error bodies, callback URLs, log lines. Every error this file returns and
// every snippet persisted by HTTP login attempts must pass through it.
func redactNeuralDeepSecrets(s string) string {
	return neuralDeepSecretRe.ReplaceAllString(s, "sk-***")
}

// NeuralDeepLoginPrompt is handed to the caller once the loopback server is
// listening: print the URL and open the browser.
type NeuralDeepLoginPrompt struct {
	AuthURL string
}

// neuralDeepStateLen matches the hub contract: state is [A-Za-z0-9_-]{8,128}.
const neuralDeepStateBytes = 32

func newNeuralDeepState() (string, error) {
	buf := make([]byte, neuralDeepStateBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// NeuralDeepSignIn runs the browser callback flow: it listens on an ephemeral
// 127.0.0.1 port, invokes onPrompt with the hub auth URL, waits for the hub
// to redirect the user's browser to /cb with the freshly minted key, stores
// the key at authPath, and returns it.
//
// Security posture: the listener binds 127.0.0.1 only (the hub hard-codes
// that host in the callback, so nothing else can be asked for); the state is
// compared in constant time before the key is even looked at; a mismatching
// state answers 400 and keeps waiting (a local port-scanner must not be able
// to abort the login); the success page echoes no secret, loads no external
// resources, and is marked uncacheable; query strings are never logged.
func NeuralDeepSignIn(ctx context.Context, hub string, hc *http.Client, authPath string, onPrompt func(NeuralDeepLoginPrompt)) (string, error) {
	if strings.TrimSpace(authPath) == "" {
		return "", errors.New("neuraldeep auth: credential path is empty")
	}
	hub = strings.TrimRight(strings.TrimSpace(hub), "/")
	if hub == "" {
		hub = NeuralDeepHub()
	}
	state, err := newNeuralDeepState()
	if err != nil {
		return "", err
	}

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("neuraldeep auth: listen on loopback: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	ctx, cancel := context.WithTimeout(ctx, neuralDeepLoginTimeout)
	defer cancel()

	type outcome struct {
		key string
		err error
	}
	resultCh := make(chan outcome, 1)
	var once sync.Once
	deliver := func(o outcome) { once.Do(func() { resultCh <- o }) }

	mux := http.NewServeMux()
	mux.HandleFunc("/cb", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		q := r.URL.Query()
		gotState, key := q.Get("state"), q.Get("key")
		if subtle.ConstantTimeCompare([]byte(gotState), []byte(state)) != 1 {
			// Foreign or replayed request: reject it and keep waiting for the
			// real callback. 32 random bytes make brute force irrelevant.
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, neuralDeepPage("Запрос отклонён", "Проверка state не прошла. Вернитесь в терминал и попробуйте войти заново."))
			return
		}
		if strings.TrimSpace(key) == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, neuralDeepPage("Ключ не получен", "Хаб не передал ключ. Попробуйте войти заново."))
			return
		}
		if err := SaveNeuralDeepAuth(authPath, key, hub, NeuralDeepClientID, NeuralDeepClientID); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, neuralDeepPage("Не удалось сохранить ключ", html.EscapeString(redactNeuralDeepSecrets(err.Error()))))
			deliver(outcome{err: err})
			return
		}
		_, _ = io.WriteString(w, neuralDeepPage("Coddy подключён к NeuralDeep", "Можно вернуться в терминал и закрыть эту вкладку."))
		deliver(outcome{key: key})
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	authURL := fmt.Sprintf("%s/api/cli/auth/start?port=%d&state=%s&client=%s",
		hub, port, url.QueryEscape(state), url.QueryEscape(NeuralDeepClientID))
	if onPrompt != nil {
		onPrompt(NeuralDeepLoginPrompt{AuthURL: authURL})
	}

	select {
	case o := <-resultCh:
		return o.key, o.err
	case <-ctx.Done():
		return "", fmt.Errorf("neuraldeep auth: login not completed: %w", ctx.Err())
	}
}

// neuralDeepPage renders a minimal self-contained page for the loopback
// server. No external resources, no secrets; title and body are authored by
// this process (already escaped by callers when dynamic).
func neuralDeepPage(title, body string) string {
	return fmt.Sprintf(
		"<!doctype html><meta charset=utf-8><title>%s</title>"+
			"<body style=\"font-family:system-ui;background:#08090C;color:#eaeaea;text-align:center;padding:64px 20px\">"+
			"<h2 style=\"color:#00FF88\">%s</h2><p>%s</p></body>",
		html.EscapeString(title), html.EscapeString(title), body)
}

// NeuralDeepDeviceLogin is one pending device authorization.
type NeuralDeepDeviceLogin struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	Interval                int    `json:"interval"`
	ExpiresIn               int    `json:"expires_in"`
}

// StartNeuralDeepDeviceLogin begins the RFC 8628 flow for client "coddy".
func StartNeuralDeepDeviceLogin(ctx context.Context, hub string, hc *http.Client, deviceLabel string) (*NeuralDeepDeviceLogin, error) {
	hub = strings.TrimRight(strings.TrimSpace(hub), "/")
	if hub == "" {
		hub = NeuralDeepHub()
	}
	if hc == nil {
		hc = &http.Client{}
	}
	body, _ := json.Marshal(map[string]string{
		"client":       NeuralDeepClientID,
		"device_label": deviceLabel,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hub+"/api/cli/device/start", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, neuralDeepHTTPError("device start", resp)
	}
	var login NeuralDeepDeviceLogin
	if err := json.NewDecoder(resp.Body).Decode(&login); err != nil {
		return nil, fmt.Errorf("neuraldeep auth: device start decode: %w", err)
	}
	if login.DeviceCode == "" {
		return nil, errors.New("neuraldeep auth: device start returned no device_code")
	}
	return &login, nil
}

// PollNeuralDeepDeviceToken polls the token endpoint once. It returns the key
// when approved, ("", nil) while pending, and an error on a terminal state.
// slowDown reports that the server asked to widen the polling interval.
func PollNeuralDeepDeviceToken(ctx context.Context, hub string, hc *http.Client, deviceCode string) (key string, slowDown bool, err error) {
	hub = strings.TrimRight(strings.TrimSpace(hub), "/")
	if hc == nil {
		hc = &http.Client{}
	}
	body, _ := json.Marshal(map[string]string{"device_code": deviceCode})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hub+"/api/cli/device/token", strings.NewReader(string(body)))
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = resp.Body.Close() }()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var ok struct {
			AccessToken string `json:"access_token"`
		}
		if err := json.Unmarshal(payload, &ok); err != nil {
			return "", false, fmt.Errorf("neuraldeep auth: device token decode: %w", err)
		}
		if strings.TrimSpace(ok.AccessToken) == "" {
			return "", false, errors.New("neuraldeep auth: device token response had no access_token")
		}
		return ok.AccessToken, false, nil
	}
	var rfcErr struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(payload, &rfcErr)
	switch rfcErr.Error {
	case "authorization_pending":
		return "", false, nil
	case "slow_down":
		return "", true, nil
	case "access_denied":
		return "", false, errors.New("neuraldeep auth: the user denied this device login")
	case "expired_token":
		return "", false, errors.New("neuraldeep auth: the device code expired before approval")
	default:
		return "", false, fmt.Errorf("neuraldeep auth: device token: HTTP %d: %s",
			resp.StatusCode, redactNeuralDeepSecrets(strings.TrimSpace(string(payload))))
	}
}

// CompleteNeuralDeepDeviceLogin polls a started device login until the user
// approves or a terminal state arrives, then persists the key at authPath and
// returns it. Polling follows the server cadence; slow_down widens the
// interval per RFC 8628.
func CompleteNeuralDeepDeviceLogin(ctx context.Context, hub string, hc *http.Client, login *NeuralDeepDeviceLogin, authPath, deviceLabel string) (string, error) {
	if strings.TrimSpace(authPath) == "" {
		return "", errors.New("neuraldeep auth: credential path is empty")
	}
	hub = strings.TrimRight(strings.TrimSpace(hub), "/")
	if hub == "" {
		hub = NeuralDeepHub()
	}
	interval := time.Duration(login.Interval) * time.Second
	for {
		key, slowDown, err := PollNeuralDeepDeviceToken(ctx, hub, hc, login.DeviceCode)
		if err != nil {
			return "", err
		}
		if key != "" {
			if err := SaveNeuralDeepAuth(authPath, key, hub, NeuralDeepClientID, deviceLabel); err != nil {
				return "", err
			}
			return key, nil
		}
		if slowDown {
			interval += 5 * time.Second
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("neuraldeep auth: login not completed: %w", ctx.Err())
		case <-time.After(interval):
		}
	}
}

// NeuralDeepDeviceSignIn runs the whole device flow: start, prompt, poll,
// persist. The CLI's --device path and headless machines use this one-call
// form; the HTTP surface starts and completes separately so the SPA can show
// the user code while the wait runs in the background.
func NeuralDeepDeviceSignIn(ctx context.Context, hub string, hc *http.Client, authPath, deviceLabel string, onPrompt func(NeuralDeepDeviceLogin)) (string, error) {
	if strings.TrimSpace(authPath) == "" {
		return "", errors.New("neuraldeep auth: credential path is empty")
	}
	hub = strings.TrimRight(strings.TrimSpace(hub), "/")
	if hub == "" {
		hub = NeuralDeepHub()
	}
	ctx, cancel := context.WithTimeout(ctx, neuralDeepLoginTimeout)
	defer cancel()

	login, err := StartNeuralDeepDeviceLogin(ctx, hub, hc, deviceLabel)
	if err != nil {
		return "", err
	}
	if onPrompt != nil {
		onPrompt(*login)
	}
	return CompleteNeuralDeepDeviceLogin(ctx, hub, hc, login, authPath, deviceLabel)
}

// NeuralDeepWhoami is the hub's lightweight identity answer.
type NeuralDeepWhoami struct {
	Email string `json:"email"`
	Name  string `json:"name"`
	Tier  string `json:"tier"`
}

// FetchNeuralDeepWhoami asks the hub who owns the key (greeting after login).
func FetchNeuralDeepWhoami(ctx context.Context, hub, key string, hc *http.Client) (*NeuralDeepWhoami, error) {
	var who NeuralDeepWhoami
	if err := neuralDeepGetJSON(ctx, hub, "/api/cli/whoami", key, hc, &who); err != nil {
		return nil, err
	}
	return &who, nil
}

// NeuralDeepStatusModel is one chat model the key can use.
type NeuralDeepStatusModel struct {
	ID  string `json:"id"`
	Ctx int    `json:"ctx"`
}

// NeuralDeepStatus is the subset of /api/cli/status Coddy consumes.
type NeuralDeepStatus struct {
	Tier   string                  `json:"tier"`
	Models []NeuralDeepStatusModel `json:"models"`
}

// FetchNeuralDeepStatus returns tier and the tier's chat-model catalog.
func FetchNeuralDeepStatus(ctx context.Context, hub, key string, hc *http.Client) (*NeuralDeepStatus, error) {
	var st NeuralDeepStatus
	if err := neuralDeepGetJSON(ctx, hub, "/api/cli/status", key, hc, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// RevokeNeuralDeepKey asks the hub to revoke the key itself (honest logout).
func RevokeNeuralDeepKey(ctx context.Context, hub, key string, hc *http.Client) error {
	hub = strings.TrimRight(strings.TrimSpace(hub), "/")
	if hub == "" {
		hub = NeuralDeepHub()
	}
	if hc == nil {
		hc = &http.Client{}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hub+"/api/cli/revoke", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return neuralDeepHTTPError("revoke", resp)
	}
	return nil
}

func neuralDeepGetJSON(ctx context.Context, hub, path, key string, hc *http.Client, out any) error {
	hub = strings.TrimRight(strings.TrimSpace(hub), "/")
	if hub == "" {
		hub = NeuralDeepHub()
	}
	if hc == nil {
		hc = &http.Client{}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hub+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return neuralDeepHTTPError(strings.TrimPrefix(path, "/api/cli/"), resp)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func neuralDeepHTTPError(op string, resp *http.Response) error {
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("neuraldeep auth: %s: HTTP %d: %s",
		op, resp.StatusCode, redactNeuralDeepSecrets(strings.TrimSpace(string(snippet))))
}

// ApplyNeuralDeepLoginToConfig makes a fresh login usable without hand-editing
// YAML: it appends the provider entry and the tier's chat models (never
// touching entries the user already has) through the staged-commit machinery
// (validate, snapshot, atomic write), and sets agent.model only when it is
// empty. It returns human-readable names of everything it added; an empty
// slice means the config already covered the login.
func ApplyNeuralDeepLoginToConfig(ctx context.Context, cfg *config.Config, name, hub, key string, hc *http.Client) ([]string, error) {
	st, err := FetchNeuralDeepStatus(ctx, hub, key, hc)
	if err != nil {
		return nil, fmt.Errorf("fetch the tier model catalog: %w", err)
	}
	var cmds []config.UCICommand
	var added []string
	if cfg.FindProvider(name) == nil {
		provJSON, _ := json.Marshal(map[string]string{"name": name, "type": "neuraldeep"})
		cmds = append(cmds, config.UCICommand{Op: config.UCIOpSet, Path: fmt.Sprintf("providers[name=%s]", name), Value: string(provJSON)})
		added = append(added, "provider "+name)
	}
	for _, m := range st.Models {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		ref := name + "/" + id
		if cfg.FindModelEntry(ref) != nil {
			continue
		}
		entry := map[string]any{"model": ref}
		if m.Ctx > 0 {
			entry["max_context_tokens"] = m.Ctx
		}
		entryJSON, _ := json.Marshal(entry)
		cmds = append(cmds, config.UCICommand{Op: config.UCIOpSet, Path: fmt.Sprintf("models[model=%s]", ref), Value: string(entryJSON)})
		added = append(added, "model "+ref)
	}
	if strings.TrimSpace(cfg.Agent.Model) == "" && len(st.Models) > 0 {
		def := name + "/" + st.Models[0].ID
		cmds = append(cmds, config.UCICommand{Op: config.UCIOpSet, Path: "agent.model", Value: def})
		added = append(added, "agent.model "+def)
	}
	if len(cmds) == 0 {
		return nil, nil
	}
	if _, err := config.CommitUCICommands(cfg.Paths, cmds); err != nil {
		return nil, err
	}
	return added, nil
}

// NeuralDeepAuthNotice is one startup credential report line for a
// neuraldeep provider (mirrors CodexAuthNotice).
type NeuralDeepAuthNotice struct {
	Provider string
	Warning  bool
	Message  string
}

// NeuralDeepAuthNotices reports, for every neuraldeep provider in the config,
// whether requests will authenticate and from which source - mirroring
// CodexAuthNotices so `coddy` surfaces a missing login at startup. Providers
// fully configured with a plain api_key stay quiet.
func NeuralDeepAuthNotices(cfg *config.Config) []NeuralDeepAuthNotice {
	if cfg == nil {
		return nil
	}
	var out []NeuralDeepAuthNotice
	for i := range cfg.Providers {
		prov := &cfg.Providers[i]
		if prov.Type != "neuraldeep" {
			continue
		}
		authPath := config.NeuralDeepAuthPath(cfg.Paths.Home, prov.Name)
		explicit := strings.TrimSpace(prov.EffectiveAPIKey()) != ""
		st, err := InspectNeuralDeepAuth(authPath)
		switch {
		case err != nil:
			out = append(out, NeuralDeepAuthNotice{Provider: prov.Name, Warning: true,
				Message: "NeuralDeep login unreadable: " + redactNeuralDeepSecrets(err.Error())})
		case explicit && st.Connected:
			out = append(out, NeuralDeepAuthNotice{Provider: prov.Name, Warning: true,
				Message: fmt.Sprintf("explicit api_key overrides the NeuralDeep login (%s); remove api_key to use the login", st.Masked)})
		case explicit:
			// A plain api_key is fully configured; nothing to report.
		case st.Connected:
			out = append(out, NeuralDeepAuthNotice{Provider: prov.Name,
				Message: fmt.Sprintf("signed in to NeuralDeep (%s)", st.Masked)})
		default:
			out = append(out, NeuralDeepAuthNotice{Provider: prov.Name, Warning: true,
				Message: fmt.Sprintf("no api_key and no NeuralDeep login; run `coddy providers login %s` or sign in from Settings", prov.Name)})
		}
	}
	return out
}

// LogNeuralDeepAuthNotices writes the startup credential report for
// neuraldeep providers. Nothing is logged when none is configured.
func LogNeuralDeepAuthNotices(log *slog.Logger, cfg *config.Config) {
	if log == nil {
		return
	}
	for _, n := range NeuralDeepAuthNotices(cfg) {
		if n.Warning {
			log.Warn("neuraldeep credential", "provider", n.Provider, "detail", n.Message)
			continue
		}
		log.Info("neuraldeep credential", "provider", n.Provider, "detail", n.Message)
	}
}
