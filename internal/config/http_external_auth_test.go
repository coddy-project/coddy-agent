package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExternalAuthConfig(t *testing.T) {
	for _, tc := range []struct {
		value string
		valid bool
	}{
		{"https://example.com/logout?next=ok", true}, {"http://example.com/", true},
		{"/logout", false}, {"https://user@example.com/", false}, {"https://example.com/#fragment", false},
		{"https://example.com/\n", false}, {"https:///path", false},
	} {
		if got := ValidLogoutURL(tc.value); got != tc.valid {
			t.Errorf("ValidLogoutURL(%q) = %v", tc.value, got)
		}
	}
	l := HTTPLoginConfig{OIDC: HTTPOIDCConfig{Enable: true}}
	if l.Validate() == nil {
		t.Fatal("incomplete enabled OIDC accepted")
	}
	l.OIDC = HTTPOIDCConfig{Enable: true, Issuer: "https://issuer.example", ClientID: "client", ClientSecret: "secret-value", RedirectURL: "https://app.example/coddy/auth/oidc/callback", RequiredRole: "operator"}
	l.LogoutURL = "https://logout.example/private"
	if err := l.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{HTTPServer: HTTPServerConfig{Login: l}}
	b, err := json.Marshal(ConfigToJSONDTO(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), l.LogoutURL) || strings.Contains(string(b), l.OIDC.ClientSecret) {
		t.Fatal("hidden values disclosed")
	}
}

func TestExternalAuthSettingsPreserveAndClear(t *testing.T) {
	current := &Config{HTTPServer: HTTPServerConfig{Login: HTTPLoginConfig{
		LogoutURL: "https://example.com/logout", OIDC: HTTPOIDCConfig{ClientSecret: "hidden-secret"},
	}}}
	raw, err := json.Marshal(ConfigToJSONDTO(current))
	if err != nil {
		t.Fatal(err)
	}
	next, err := ParseConfigJSONPreservingSecrets(raw, Paths{}, current)
	if err != nil {
		t.Fatal(err)
	}
	if next.HTTPServer.Login.LogoutURL != current.HTTPServer.Login.LogoutURL || next.HTTPServer.Login.OIDC.ClientSecret != "hidden-secret" {
		t.Fatal("hidden values not preserved")
	}
	var dto ConfigJSON
	if err := json.Unmarshal(raw, &dto); err != nil {
		t.Fatal(err)
	}
	dto.HTTPServer.Login.ClearLogoutURL = true
	dto.HTTPServer.Login.OIDC.ClearClientSecret = true
	raw, err = json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	next, err = ParseConfigJSONPreservingSecrets(raw, Paths{}, current)
	if err != nil {
		t.Fatal(err)
	}
	if next.HTTPServer.Login.LogoutURL != "" || next.HTTPServer.Login.OIDC.ClientSecret != "" {
		t.Fatal("explicit clear failed")
	}
}

const oidcRecoveryConfig = `httpserver:
  login:
    oidc:
      enable: true
      issuer: https://issuer.example
      client_id: client
      client_secret: fixture-secret
      redirect_url: https://app.example/coddy/auth/oidc/callback
      required_role: operator
`

func TestExternalAuthRecoveryPolicy(t *testing.T) {
	paths := Paths{ConfigPath: filepath.Join(t.TempDir(), "config.yaml")}
	p := paths.ConfigPath
	write := func(path, value string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	assertFile := func(path, want string) {
		t.Helper()
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("unexpected contents of %s: %v", path, err)
		}
	}
	refuse := func(bad, backup string) {
		t.Helper()
		write(p, bad)
		write(BackupPath(p), backup)
		if _, err := LoadWithPaths(paths); err == nil || !strings.Contains(err.Error(), "automatic config recovery disabled") {
			t.Fatalf("wanted protected recovery error, got %v", err)
		}
		assertFile(p, bad)
		assertFile(BackupPath(p), backup)
	}
	write(p, oidcRecoveryConfig)
	if cfg, err := LoadWithPaths(paths); err != nil || !cfg.HTTPServer.Login.OIDC.Enable {
		t.Fatalf("OIDC load: %v", err)
	}
	refuse("invalid: [", "{}\n")
	refuse("agent:\n  max_turns: -1\n", oidcRecoveryConfig)

	// A good manual edit may turn OIDC off; it does not unprotect the path.
	write(p, "{}\n")
	if _, err := LoadWithPaths(paths); err == nil || !strings.Contains(err.Error(), "explicitly to false") {
		t.Fatalf("implicit disable: %v", err)
	}
	write(p, "httpserver:\n  login:\n    oidc:\n      enable: false\n")
	if cfg, err := LoadWithPaths(paths); err != nil || cfg.HTTPServer.Login.OIDC.Enable {
		t.Fatalf("explicit disable: %v", err)
	}
	refuse("invalid: [", "{}\n")
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfigFile(paths, false); err == nil || !strings.Contains(err.Error(), "automatic config recovery disabled") {
		t.Fatalf("protected missing primary used defaults: %v", err)
	}

	// The legacy backup filename is shared in a directory, but the marker isn't.
	other := filepath.Join(filepath.Dir(p), "other.yaml")
	write(other, "httpserver: [\n")
	if _, err := LoadWithPaths(Paths{ConfigPath: other}); err != nil {
		t.Fatalf("legacy recovery: %v", err)
	}
	assertFile(other, "{}\n")
}

func TestExternalAuthRecoveryRefusesUnseenOIDCBackup(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	for path, data := range map[string]string{p: "bad: [", BackupPath(p): oidcRecoveryConfig} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := LoadWithPaths(Paths{ConfigPath: p}); err == nil || !strings.Contains(err.Error(), "backup enables OIDC") {
		t.Fatalf("unseen OIDC backup: %v", err)
	}
	if got, err := os.ReadFile(p); err != nil || string(got) != "bad: [" {
		t.Fatalf("primary was changed: %v", err)
	}
}

func TestExternalAuthRecoveryValidatesBeforeChangingFiles(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	paths := Paths{ConfigPath: p}
	backup := BackupPath(p)
	const legacy = "agent:\n  max_turns: 4\n"
	for path, body := range map[string]string{p: oidcRecoveryConfig, backup: legacy} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(p+oidcRecoverySuffix, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWithPaths(paths); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("load accepted OIDC without a writable marker: %v", err)
	}
	if err := ProtectConfigYAMLWrite(paths, []byte(oidcRecoveryConfig)); err == nil {
		t.Fatal("save accepted OIDC without a writable marker")
	}
	if got, err := os.ReadFile(backup); err != nil || string(got) != legacy {
		t.Fatalf("backup changed before protection was recorded: %v", err)
	}
}

func TestExternalAuthRecoveryWatcherRecordsBeforeInstalling(t *testing.T) {
	w, _, _ := watchFixture(t, watchBaseYAML)
	w.Poll()
	p := w.Paths.ConfigPath
	w.Install = func(cfg *Config) {
		got, err := os.ReadFile(p + oidcRecoverySuffix)
		if err != nil || len(got) == 0 || strings.Contains(string(got), "fixture-secret") {
			t.Fatalf("missing marker or credentials in marker: %v", err)
		}
		if !cfg.HTTPServer.Login.OIDC.Enable {
			t.Fatal("watcher lost OIDC settings")
		}
	}
	rewriteWatched(t, w, oidcRecoveryConfig+watchBaseYAML)
	if !w.Poll() {
		t.Fatal("watcher did not install OIDC edit")
	}
}

func TestExternalAuthMalformedValuesAreRedacted(t *testing.T) {
	for _, body := range []string{
		"httpserver:\n  login:\n    logout_url: 123456789012345\n",
		"httpserver:\n  login:\n    oidc:\n      client_secret: [secret-in-sequence]\n",
		"httpserver:\n  login:\n    logout_url: {secret-in-map: true}\n",
		"httpserver:\n  login:\n    logout_url: {secret-in-map: 1, secret-in-map: 2}\n",
		"httpserver:\n  login:\n    logout_url: *secret-in-map\n",
	} {
		for _, forbidden := range []string{"123456789012345", "secret-in-sequence", "secret-in-map"} {
			_, err := ParseConfigYAML([]byte(body), Paths{})
			if err != nil && strings.Contains(err.Error(), forbidden) {
				t.Fatalf("loader leaks %s: %v", forbidden, err)
			}
			for _, f := range checkConfigBytes([]byte(body), Paths{}) {
				if strings.Contains(f.Message, forbidden) || strings.Contains(f.Fix, forbidden) {
					t.Fatalf("check leaks %s: %+v", forbidden, f)
				}
			}
		}
	}
}

func TestExternalAuthEnvReferencesSurviveSettingsSave(t *testing.T) {
	t.Setenv("TEST_EXTERNAL_LOGOUT", "https://example.com/logout")
	t.Setenv("TEST_EXTERNAL_SECRET", "secret-value")
	original := []byte("httpserver:\n  login:\n    logout_url: ${TEST_EXTERNAL_LOGOUT} # keep\n    oidc:\n      client_secret: ${TEST_EXTERNAL_SECRET}\n")
	cur, err := ParseConfigYAML(original, Paths{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(ConfigToJSONDTO(cur))
	if err != nil {
		t.Fatal(err)
	}
	next, err := ParseConfigJSONPreservingSecrets(b, Paths{}, cur)
	if err != nil {
		t.Fatal(err)
	}
	out, err := MarshalConfigYAMLPreservingComments(next, original)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"${TEST_EXTERNAL_LOGOUT}", "${TEST_EXTERNAL_SECRET}", "# keep"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("lost %s", want)
		}
	}
}

func TestOIDCURLRulesOnLoadAndSave(t *testing.T) {
	for _, tc := range []struct{ field, base, suffix string }{
		{"redirect_url", "https://app.example/coddy/auth/oidc/callback", "?"},
		{"redirect_url", "https://app.example/coddy/auth/oidc/callback", "?private-value"},
		{"redirect_url", "https://app.example/coddy/auth/oidc/callback", "/private-value"},
		{"redirect_url", "https://app.example/coddy/auth/oidc/callback", "#"},
		{"issuer", "https://issuer.example", "?"},
		{"issuer", "https://issuer.example", "?private-value"},
	} {
		t.Run(tc.field+tc.suffix, func(t *testing.T) {
			valid, err := ParseConfigYAML([]byte(oidcRecoveryConfig), Paths{})
			if err != nil {
				t.Fatal(err)
			}
			bad := strings.Replace(oidcRecoveryConfig, tc.base, tc.base+tc.suffix, 1)
			check := func(err error) {
				t.Helper()
				if err == nil || !strings.Contains(err.Error(), "httpserver.login.oidc."+tc.field) || strings.Contains(err.Error(), "private-value") || strings.Contains(err.Error(), "fixture-secret") {
					t.Fatalf("wanted safe field diagnostic, got %v", err)
				}
			}
			_, err = ParseConfigYAML([]byte(bad), Paths{})
			check(err)
			dto := ConfigToJSONDTO(valid)
			if tc.field == "issuer" {
				dto.HTTPServer.Login.OIDC.Issuer += tc.suffix
			} else {
				dto.HTTPServer.Login.OIDC.RedirectURL += tc.suffix
			}
			raw, err := json.Marshal(dto)
			if err != nil {
				t.Fatal(err)
			}
			_, err = ParseConfigJSONPreservingSecrets(raw, Paths{}, valid)
			check(err)
		})
	}
}
