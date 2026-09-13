package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/webauth"
)

// loginHash is a real argon2id hash, cheap enough for a unit test, of "pw".
func loginHash(t *testing.T, plain string) string {
	t.Helper()
	h, err := webauth.HashPasswordWith(plain, webauth.HashParams{Memory: 64, Time: 1, Threads: 1, SaltLen: 8, KeyLen: 16})
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return h
}

// yamlHash spells a hash the way a config file on disk carries it: every "$"
// doubled, which is how this format writes a literal dollar sign (expand.go).
// `coddy serve set-password` and every save from the API do the same.
func yamlHash(h string) string { return strings.ReplaceAll(h, "$", "$$") }

func writeLoginConfig(t *testing.T, body string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	f := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(f, []byte(httpAuthBaseYAML+body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(f)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return cfg
}

func TestHTTPLoginParsedFromYAML(t *testing.T) {
	hash := loginHash(t, "pw")
	cfg := writeLoginConfig(t, "httpserver:\n  login:\n    enable: true\n    user: \"operator\"\n    password_hash: \""+yamlHash(hash)+"\"\n    session_ttl_hours: 720\n")
	l := cfg.HTTPServer.Login
	if !l.IsExplicitlyEnabled() {
		t.Fatal("enable: true was not parsed")
	}
	if l.User != "operator" || l.PasswordHash != hash {
		t.Fatalf("account not parsed: user=%q hash=%q", l.User, l.PasswordHash)
	}
	if !l.HasAccount() {
		t.Fatal("a user plus a hash is an account")
	}
	if got := l.SessionTTL(); got != 720*time.Hour {
		t.Fatalf("session TTL = %v, want 720h", got)
	}
}

func TestHTTPLoginAbsentMeansOff(t *testing.T) {
	cfg := writeLoginConfig(t, "")
	l := cfg.HTTPServer.Login
	if l.Enabled != nil {
		t.Fatal("an untouched config must leave enable unset, not false")
	}
	if l.HasAccount() || l.IsExplicitlyEnabled() || l.IsExplicitlyDisabled() {
		t.Fatalf("an untouched config reports a login: %+v", l)
	}
	if got := l.SessionTTL(); got != 0 {
		t.Fatalf("session TTL without a value = %v, want 0 (browser session)", got)
	}
}

func TestHTTPLoginExplicitlyDisabled(t *testing.T) {
	cfg := writeLoginConfig(t, "httpserver:\n  login:\n    enable: false\n")
	if !cfg.HTTPServer.Login.IsExplicitlyDisabled() {
		t.Fatal("enable: false must be distinguishable from an absent key")
	}
}

func TestHTTPLoginUserExpandsEnvReference(t *testing.T) {
	t.Setenv("CODDY_TEST_LOGIN_USER", "from-env")
	cfg := writeLoginConfig(t, "httpserver:\n  login:\n    user: \"${CODDY_TEST_LOGIN_USER}\"\n")
	if got := cfg.HTTPServer.Login.User; got != "from-env" {
		t.Fatalf("login.user did not expand ${ENV}: %q", got)
	}
}

func TestHTTPLoginValidation(t *testing.T) {
	good := loginHash(t, "pw")
	cases := []struct {
		name    string
		login   config.HTTPLoginConfig
		wantErr string
	}{
		{name: "empty is valid"},
		{name: "password mode", login: config.HTTPLoginConfig{Mode: "password"}},
		{name: "unknown mode", login: config.HTTPLoginConfig{Mode: "oidc"}, wantErr: "mode"},
		{name: "negative ttl", login: config.HTTPLoginConfig{SessionTTLHours: -1}, wantErr: "session_ttl_hours"},
		{name: "plaintext hash", login: config.HTTPLoginConfig{PasswordHash: "hunter2"}, wantErr: "argon2id"},
		{name: "bcrypt hash", login: config.HTTPLoginConfig{PasswordHash: "$2y$10$abcdefghij"}, wantErr: "argon2id"},
		{name: "real hash", login: config.HTTPLoginConfig{User: "operator", PasswordHash: good}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := config.HTTPServerConfig{Login: tc.login}
			err := h.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want one naming %q", err, tc.wantErr)
			}
		})
	}
}

func TestHTTPLoginMalformedHashIsRefusedAtLoad(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "config.yaml")
	body := httpAuthBaseYAML + "httpserver:\n  login:\n    user: operator\n    password_hash: \"not-a-hash\"\n"
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(f)
	if err == nil || !strings.Contains(err.Error(), "argon2id") {
		t.Fatalf("a plaintext password_hash must be refused at load, got %v", err)
	}
}

func TestHTTPLoginNormalizeTrims(t *testing.T) {
	h := config.HTTPServerConfig{Login: config.HTTPLoginConfig{
		Mode: "  Password ", User: "  operator  ", PasswordHash: "  $argon2id$x  ",
	}}
	h.Normalize()
	if h.Login.Mode != "password" || h.Login.User != "operator" || h.Login.PasswordHash != "$argon2id$x" {
		t.Fatalf("normalize left whitespace or case: %+v", h.Login)
	}
}

func TestHTTPLoginHashRedactedInJSONDTO(t *testing.T) {
	hash := loginHash(t, "pw")
	cfg := writeLoginConfig(t, "httpserver:\n  login:\n    enable: true\n    user: operator\n    password_hash: \""+yamlHash(hash)+"\"\n    session_ttl_hours: 12\n")
	dto := config.ConfigToJSONDTO(cfg)
	if dto.HTTPServer.Login.PasswordHash != "" {
		t.Fatalf("GET /coddy/config must not expose password_hash, got %q", dto.HTTPServer.Login.PasswordHash)
	}
	if !dto.HTTPServer.LoginConfigured {
		t.Fatal("login_configured should be reported in the DTO")
	}
	if dto.HTTPServer.Login.User != "operator" {
		t.Fatalf("login user should round-trip, got %q", dto.HTTPServer.Login.User)
	}
	if dto.HTTPServer.Login.SessionTTLHours != 12 {
		t.Fatalf("session_ttl_hours should round-trip, got %d", dto.HTTPServer.Login.SessionTTLHours)
	}
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), hash) {
		t.Fatalf("serialized config leaked the password hash: %s", raw)
	}
}

func TestHTTPLoginHashPreservedOnRedactedEdit(t *testing.T) {
	hash := loginHash(t, "pw")
	cur := writeLoginConfig(t, "httpserver:\n  login:\n    enable: true\n    user: operator\n    password_hash: \""+yamlHash(hash)+"\"\n")
	// What the settings screen sends back: the redacted view plus an unrelated edit.
	body := `{"providers":[{"name":"openai","type":"openai","api_key":"k"}],` +
		`"models":[{"model":"openai/gpt-4o","max_tokens":1024,"temperature":0.2}],` +
		`"agent":{"model":"openai/gpt-4o","max_turns":7},` +
		`"httpserver":{"login_configured":true,"login":{"enable":true,"user":"operator"}}}`
	next, err := config.ParseConfigJSONPreservingSecrets([]byte(body), cur.Paths, cur)
	if err != nil {
		t.Fatalf("preserve parse: %v", err)
	}
	if next.HTTPServer.Login.PasswordHash != hash {
		t.Fatalf("password hash not preserved across a redacted save: %q", next.HTTPServer.Login.PasswordHash)
	}
	if !next.HTTPServer.Login.IsExplicitlyEnabled() {
		t.Fatal("enable: true lost across a redacted save")
	}
	if next.Agent.MaxTurns != 7 {
		t.Fatalf("unrelated edit lost: max_turns=%d", next.Agent.MaxTurns)
	}
}

func TestHTTPLoginDisableSurvivesRedactedEdit(t *testing.T) {
	hash := loginHash(t, "pw")
	cur := writeLoginConfig(t, "httpserver:\n  login:\n    enable: true\n    user: operator\n    password_hash: \""+yamlHash(hash)+"\"\n")
	// Turning the form off from the API must not be undone by the hash the same
	// save preserves.
	body := `{"providers":[{"name":"openai","type":"openai","api_key":"k"}],` +
		`"models":[{"model":"openai/gpt-4o","max_tokens":1024,"temperature":0.2}],` +
		`"agent":{"model":"openai/gpt-4o"},` +
		`"httpserver":{"login":{"enable":false,"user":"operator"}}}`
	next, err := config.ParseConfigJSONPreservingSecrets([]byte(body), cur.Paths, cur)
	if err != nil {
		t.Fatalf("preserve parse: %v", err)
	}
	if !next.HTTPServer.Login.IsExplicitlyDisabled() {
		t.Fatal("enable: false did not survive the save")
	}
	if next.HTTPServer.Login.PasswordHash != hash {
		t.Fatal("the account should stay on file when the form is only switched off")
	}
}

func TestHTTPLoginHashRedactedInConfigRead(t *testing.T) {
	// `config_get` and the `-t` report print the file with secrets masked; the
	// password hash must be one of them.
	hash := loginHash(t, "pw")
	dir := t.TempDir()
	f := filepath.Join(dir, "config.yaml")
	body := httpAuthBaseYAML + "httpserver:\n  login:\n    user: operator\n    password_hash: \"" + yamlHash(hash) + "\"\n"
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := config.ReadConfigPath(config.Paths{ConfigPath: f, Home: dir, CWD: dir}, "/")
	if err != nil {
		t.Fatalf("read config path: %v", err)
	}
	encoded, err := json.Marshal(got.Value)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), hash) {
		t.Fatalf("config_get leaked the password hash:\n%s", encoded)
	}
	if !got.Redacted {
		t.Fatal("the read did not report that it redacted something")
	}
}

func TestHTTPLoginHashSurvivesWriteAndReload(t *testing.T) {
	// The hash is full of "$", which the load-time expansion would read as
	// environment references. Whatever writes a config must escape it, or the
	// operator's own password stops working on the next restart.
	hash := loginHash(t, "pw")
	dir := t.TempDir()
	f := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(f, []byte(httpAuthBaseYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(f)
	if err != nil {
		t.Fatal(err)
	}
	enable := true
	cfg.HTTPServer.Login = config.HTTPLoginConfig{Enabled: &enable, User: "operator", PasswordHash: hash}

	out, err := config.MarshalConfigYAMLForFile(cfg, f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "\n    password_hash: \""+hash+"\"") {
		t.Fatalf("the hash was written unescaped, so the next load will mangle it:\n%s", out)
	}
	if err := os.WriteFile(f, out, 0o600); err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.Load(f)
	if err != nil {
		t.Fatalf("reload a config we just wrote: %v", err)
	}
	if got := reloaded.HTTPServer.Login.PasswordHash; got != hash {
		t.Fatalf("hash changed across write+reload:\n got %q\nwant %q", got, hash)
	}
	if ok, err := webauth.VerifyPassword(reloaded.HTTPServer.Login.PasswordHash, "pw"); err != nil || !ok {
		t.Fatalf("the reloaded hash no longer verifies the password: ok=%v err=%v", ok, err)
	}
}

func TestHTTPLoginHashFromEnvReferenceSurvivesReload(t *testing.T) {
	// The other supported spelling: the hash lives in the environment and the
	// file only points at it.
	hash := loginHash(t, "pw")
	t.Setenv("CODDY_TEST_LOGIN_HASH", hash)
	cfg := writeLoginConfig(t, "httpserver:\n  login:\n    enable: true\n    user: operator\n    password_hash: \"${CODDY_TEST_LOGIN_HASH}\"\n")
	if got := cfg.HTTPServer.Login.PasswordHash; got != hash {
		t.Fatalf("password_hash did not expand ${ENV}: %q", got)
	}
}
