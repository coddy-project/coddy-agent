//go:build http

package httpserver

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/webauth"
)

type enrollmentGatedReader struct {
	started chan struct{}
	resume  chan struct{}
	reader  io.Reader
}

func (g *enrollmentGatedReader) Read(p []byte) (int, error) {
	close(g.started)
	<-g.resume
	return g.reader.Read(p)
}

func TestEnrollmentSetupRequiresSessionAndSameOrigin(t *testing.T) {
	srv := newLoginServer(t, configuredLogin(t))
	cookie := sessionCookieOf(t, signIn(t, srv, loginTestUser, loginTestPassword))
	for _, tc := range []struct {
		name   string
		cookie bool
		origin string
		want   int
	}{
		{"anonymous", false, "same-origin", http.StatusUnauthorized},
		{"cross-origin", true, "cross-site", http.StatusForbidden},
		{"same-origin", true, "same-origin", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/coddy/auth/totp/setup", strings.NewReader(`{}`))
			r.Header.Set("Sec-Fetch-Site", tc.origin)
			if tc.cookie {
				r.AddCookie(cookie)
			}
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			if tc.want == http.StatusOK {
				var result map[string]string
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result["secret"] == "" || !strings.HasPrefix(result["uri"], "otpauth://totp/") || !strings.HasPrefix(result["qr_png"], "data:image/png;base64,") {
					t.Fatalf("invalid setup response: %v", result)
				}
				pngData, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(result["qr_png"], "data:image/png;base64,"))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := png.Decode(bytes.NewReader(pngData)); err != nil {
					t.Fatalf("invalid PNG: %v", err)
				}
			}
		})
	}
}

func TestEnrollmentLifecycle(t *testing.T) {
	srv := newLoginServer(t, configuredLogin(t))
	c := srv.activeCfg()
	c.Providers = []config.ProviderConfig{{Name: "openai", Type: "openai", APIKey: "k"}}
	c.Paths.ConfigPath = filepath.Join(t.TempDir(), "config.yaml")
	raw, err := config.MarshalConfigYAMLForFile(c, c.Paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.Paths.ConfigPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	post := func(path, body string) *httptest.ResponseRecorder {
		t.Helper()
		pol := srv.loginPolicyNow()
		token, _, err := srv.sessions.Issue(pol.account.user, pol.account.fingerprint(), pol.account.ttl)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		r.AddCookie(sessionCookie(r, token, 0))
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		return w
	}
	const secret = "3132333435363738393031323334353637383930"
	invalid := post("/coddy/auth/totp/enable", `{"secret":"`+secret+`","code":"bad"}`)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid: %d %s", invalid.Code, invalid.Body.String())
	}
	if srv.activeCfg().HTTPServer.Login.TOTPSecret != "" {
		t.Fatal("failed enrollment changed configuration")
	}
	before, err := os.ReadFile(c.Paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(before, []byte(secret)) {
		t.Fatal("failed enrollment persisted secret")
	}
	code, err := webauth.TOTPCode(secret, srv.authNow())
	if err != nil {
		t.Fatal(err)
	}
	enabled := post("/coddy/auth/totp/enable", `{"secret":"`+secret+`","code":"`+code+`"}`)
	if enabled.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", enabled.Code, enabled.Body.String())
	}
	var first struct {
		Codes      []string `json:"codes"`
		Generation string   `json:"backup_codes_generation"`
	}
	if err := json.Unmarshal(enabled.Body.Bytes(), &first); err != nil || len(first.Codes) != 10 {
		t.Fatalf("codes: %v %v", first.Codes, err)
	}
	if srv.activeCfg().HTTPServer.Login.TOTPSecret == secret || srv.activeCfg().HTTPServer.Login.TOTPSecret == "" {
		t.Fatal("Base16 secret was not converted to Base32")
	}
	stored, err := os.ReadFile(c.Paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range first.Codes {
		if bytes.Contains(stored, []byte(code)) {
			t.Fatal("plaintext backup code persisted")
		}
	}
	firstGeneration := srv.activeCfg().HTTPServer.Login.BackupCodes.Generation
	if first.Generation == "" || first.Generation != firstGeneration {
		t.Fatalf("enable generation mismatch: %q", first.Generation)
	}
	for _, code := range first.Codes {
		if strings.Contains(first.Generation, code) {
			t.Fatal("generation contains a code")
		}
	}
	for _, hash := range srv.activeCfg().HTTPServer.Login.BackupCodes.Hashes {
		if strings.Contains(first.Generation, hash) {
			t.Fatal("generation contains a hash")
		}
	}
	rotated := post("/coddy/auth/backup-codes/regenerate", `{}`)
	if rotated.Code != http.StatusOK {
		t.Fatalf("rotate: %d %s", rotated.Code, rotated.Body.String())
	}
	var second struct {
		Codes      []string `json:"codes"`
		Generation string   `json:"backup_codes_generation"`
	}
	if err := json.Unmarshal(rotated.Body.Bytes(), &second); err != nil || len(second.Codes) != 10 || second.Codes[0] == first.Codes[0] {
		t.Fatal("rotation did not produce fresh codes")
	}
	if second.Generation == "" || second.Generation != srv.activeCfg().HTTPServer.Login.BackupCodes.Generation || second.Generation == first.Generation {
		t.Fatal("rotation response generation mismatch")
	}
	if srv.activeCfg().HTTPServer.Login.BackupCodes.Generation == firstGeneration {
		t.Fatal("backup generation did not rotate")
	}
	disabled := post("/coddy/auth/totp/disable", `{}`)
	if disabled.Code != http.StatusOK || srv.activeCfg().HTTPServer.Login.TOTPSecret != "" || srv.activeCfg().HTTPServer.Login.BackupCodes.Generation != "" {
		t.Fatalf("disable: %d %s", disabled.Code, disabled.Body.String())
	}
}

func TestBackupGenerationFingerprint(t *testing.T) {
	state, codes, err := webauth.GenerateBackupCodes(2)
	if err != nil {
		t.Fatal(err)
	}
	srv := newLoginServer(t, configuredLogin(t))
	srv.activeCfg().HTTPServer.Login.BackupCodes = state
	initial := srv.loginPolicyNow().account.fingerprint()
	store := webauth.NewBackupStore(state)
	if !store.Consume(codes[0]) {
		t.Fatal("code not consumed")
	}
	srv.activeCfg().HTTPServer.Login.BackupCodes = store.Snapshot()
	if srv.loginPolicyNow().account.fingerprint() != initial {
		t.Fatal("consumption changed fingerprint")
	}
	next, _, err := webauth.GenerateBackupCodes(2)
	if err != nil {
		t.Fatal(err)
	}
	srv.activeCfg().HTTPServer.Login.BackupCodes = next
	if srv.loginPolicyNow().account.fingerprint() == initial {
		t.Fatal("rotation did not change fingerprint")
	}
}

func TestEnrollmentSetupRejectsInvalidSecrets(t *testing.T) {
	srv := newLoginServer(t, configuredLogin(t))
	cookie := sessionCookieOf(t, signIn(t, srv, loginTestUser, loginTestPassword))
	for _, secret := range []string{"invalid!", "1234", "0x1234"} {
		body, err := json.Marshal(map[string]string{"secret": secret})
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "/coddy/auth/totp/setup", bytes.NewReader(body))
		r.AddCookie(cookie)
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("secret %q: status %d", secret, w.Code)
		}
	}
}

func TestEnrollmentRejectsSessionInvalidatedDuringRequest(t *testing.T) {
	srv := newLoginServer(t, configuredLogin(t))
	cookie := sessionCookieOf(t, signIn(t, srv, loginTestUser, loginTestPassword))
	secret, err := webauth.GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	code, err := webauth.TOTPCode(secret, srv.authNow())
	if err != nil {
		t.Fatal(err)
	}
	reader := &enrollmentGatedReader{started: make(chan struct{}), resume: make(chan struct{}), reader: strings.NewReader(`{"secret":"` + secret + `","code":"` + code + `"}`)}
	r := httptest.NewRequest(http.MethodPost, "/coddy/auth/totp/enable", reader)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); srv.Handler().ServeHTTP(w, r) }()
	<-reader.started
	next := *srv.activeCfg()
	next.HTTPServer.Login.PasswordHash = loginTestHash(t, "different")
	if err := srv.mgr.InstallConfig(&next); err != nil {
		t.Fatal(err)
	}
	close(reader.resume)
	<-done
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("stale session: status %d, body %s", w.Code, w.Body.String())
	}
}

func TestLoginSettingsPasswordValidateAndPut(t *testing.T) {
	srv := newLoginServer(t, configuredLogin(t))
	c := srv.activeCfg()
	c.Providers = []config.ProviderConfig{{Name: "openai", Type: "openai", APIKey: "k"}}
	c.Paths.ConfigPath = filepath.Join(t.TempDir(), "config.yaml")
	yaml, err := config.MarshalConfigYAMLForFile(c, c.Paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.Paths.ConfigPath, yaml, 0600); err != nil {
		t.Fatal(err)
	}
	cookie := sessionCookieOf(t, signIn(t, srv, loginTestUser, loginTestPassword))
	dto := config.ConfigToJSONDTO(c)
	dto.HTTPServer.Login.Password = "new secret"
	body, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []struct{ method, path string }{{http.MethodPost, "/coddy/config/validate"}, {http.MethodPut, "/coddy/config"}} {
		r := httptest.NewRequest(endpoint.method, endpoint.path, bytes.NewReader(body))
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d %s", endpoint.path, w.Code, w.Body.String())
		}
	}
	yaml, err = os.ReadFile(c.Paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(yaml, []byte("new secret")) {
		t.Fatal("plaintext password persisted")
	}
	ok, err := webauth.VerifyPassword(srv.activeCfg().HTTPServer.Login.PasswordHash, "new secret")
	if err != nil || !ok {
		t.Fatal("new password not installed")
	}
	raw, err := json.Marshal(config.ConfigToJSONDTO(srv.activeCfg()))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("new secret")) || bytes.Contains(raw, []byte(srv.activeCfg().HTTPServer.Login.PasswordHash)) {
		t.Fatal("password or hash returned")
	}
}

func TestEnrollmentInstallFailureRestoresOriginalFile(t *testing.T) {
	srv := newLoginServer(t, configuredLogin(t))
	c := srv.activeCfg()
	c.Providers = []config.ProviderConfig{{Name: "openai", Type: "openai", APIKey: "k"}}
	c.Paths.ConfigPath = filepath.Join(t.TempDir(), "config.yaml")
	original, err := config.MarshalConfigYAMLForFile(c, c.Paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	original = append([]byte("# CRLF\r\n# LF\n# CR\r"), original...)
	if err := os.WriteFile(c.Paths.ConfigPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	cookie := sessionCookieOf(t, signIn(t, srv, loginTestUser, loginTestPassword))
	srv.mgr.SetConfigAdjuster(func(*config.Config) error { return errors.New("injected install failure") })
	secret, err := webauth.GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	code, err := webauth.TOTPCode(secret, srv.authNow())
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/coddy/auth/totp/enable", strings.NewReader(`{"secret":"`+secret+`","code":"`+code+`"}`))
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"codes"`) {
		t.Fatal("failed installation returned codes")
	}
	after, err := os.ReadFile(c.Paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, original) {
		t.Error("install failure changed configuration file")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(c.Paths.ConfigPath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("restored configuration mode = %o, want 600", info.Mode().Perm())
		}
	}
	if srv.activeCfg().HTTPServer.Login.TOTPSecret != "" || srv.activeCfg().HTTPServer.Login.BackupCodes.Generation != "" {
		t.Error("install failure changed live authentication")
	}
}

func TestEnrollmentRejectsInvalidRenderedYAMLWithoutRecovery(t *testing.T) {
	t.Setenv("CODDY_ENROLLMENT_MISSING_MODEL", "")
	srv := newLoginServer(t, configuredLogin(t))
	c := srv.activeCfg()
	c.Providers = []config.ProviderConfig{{Name: "openai", Type: "openai", APIKey: "k"}}
	c.Paths.ConfigPath = filepath.Join(t.TempDir(), "config.yaml")
	backup, err := config.MarshalConfigYAMLForFile(c, c.Paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.WriteBackup(c.Paths.ConfigPath, backup); err != nil {
		t.Fatal(err)
	}
	c.Agent.Model = "openai/$CODDY_ENROLLMENT_MISSING_MODEL"
	c.Models[0].Model = c.Agent.Model
	cookie := sessionCookieOf(t, signIn(t, srv, loginTestUser, loginTestPassword))
	secret, err := webauth.GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	code, err := webauth.TOTPCode(secret, srv.authNow())
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/coddy/auth/totp/enable", strings.NewReader(`{"secret":"`+secret+`","code":"`+code+`"}`))
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("invalid rendered config returned status %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"codes"`) {
		t.Error("invalid rendered config returned backup codes")
	}
	if _, err := os.Stat(c.Paths.ConfigPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("originally absent config changed: %v", err)
	}
	if srv.activeCfg().HTTPServer.Login.TOTPSecret != "" || srv.activeCfg().HTTPServer.Login.BackupCodes.Generation != "" {
		t.Error("live auth changed")
	}
}
