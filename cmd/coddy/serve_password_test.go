package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/external/httpserver"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/webauth"
)

const setPasswordBaseYAML = `# a comment the operator wrote
providers:
  - name: openai
    type: openai
    api_key: k
models:
  - model: openai/gpt-4o
    max_tokens: 1024
    temperature: 0.2
agent:
  model: openai/gpt-4o
`

// withStdin replaces os.Stdin with a pipe carrying text, which is the path
// `coddy serve set-password` takes when it is not on a terminal.
func withStdin(t *testing.T, text string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(text); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	prev := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = prev
		_ = r.Close()
	})
}

func setPasswordHome(t *testing.T, body string) (home, cfgPath string) {
	t.Helper()
	home = t.TempDir()
	cfgPath = filepath.Join(home, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, cfgPath
}

func TestServeSetPasswordWritesAnAccountThatVerifies(t *testing.T) {
	home, cfgPath := setPasswordHome(t, setPasswordBaseYAML)
	withStdin(t, "correct-horse-battery-staple\n")

	if err := runServeSetPassword([]string{"--home", home, "--config", cfgPath, "--user", "pasha"}); err != nil {
		t.Fatalf("set-password: %v", err)
	}

	// The whole point: what was written loads back and accepts the password.
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("reload the config it wrote: %v", err)
	}
	login := cfg.HTTPServer.Login
	if !login.IsExplicitlyEnabled() {
		t.Fatal("the form was not switched on")
	}
	if login.User != "pasha" {
		t.Fatalf("account = %q, want pasha", login.User)
	}
	ok, err := webauth.VerifyPassword(login.PasswordHash, "correct-horse-battery-staple")
	if err != nil || !ok {
		t.Fatalf("the written hash does not verify the password: ok=%v err=%v", ok, err)
	}

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, "correct-horse-battery-staple") {
		t.Fatalf("the plaintext password was written to the file:\n%s", text)
	}
	// Every "$" of the hash is doubled on disk, which is how this file spells a
	// literal dollar sign; without it the next load reads the hash as a row of
	// empty environment references.
	if !strings.Contains(text, "$$argon2id$$") {
		t.Fatalf("the hash was not escaped for the file:\n%s", text)
	}
	if !strings.Contains(text, "# a comment the operator wrote") {
		t.Fatalf("the operator's comment did not survive the edit:\n%s", text)
	}
	if !strings.Contains(text, "max_turns") && !strings.Contains(text, "openai/gpt-4o") {
		t.Fatalf("the rest of the document did not survive the edit:\n%s", text)
	}
}

func TestServeSetPasswordKeepsTheConfiguredAccountName(t *testing.T) {
	home, cfgPath := setPasswordHome(t, setPasswordBaseYAML+
		"httpserver:\n  login:\n    user: already-there\n")
	withStdin(t, "another-long-password\n")

	if err := runServeSetPassword([]string{"--home", home, "--config", cfgPath}); err != nil {
		t.Fatalf("set-password: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPServer.Login.User != "already-there" {
		t.Fatalf("account = %q, want the one already in the file", cfg.HTTPServer.Login.User)
	}
}

func TestServeSetPasswordRotationReplacesTheHash(t *testing.T) {
	home, cfgPath := setPasswordHome(t, setPasswordBaseYAML)

	withStdin(t, "first-password-here\n")
	if err := runServeSetPassword([]string{"--home", home, "--config", cfgPath, "--user", "pasha"}); err != nil {
		t.Fatalf("first set-password: %v", err)
	}
	first, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	withStdin(t, "second-password-here\n")
	if err := runServeSetPassword([]string{"--home", home, "--config", cfgPath}); err != nil {
		t.Fatalf("second set-password: %v", err)
	}
	second, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if first.HTTPServer.Login.PasswordHash == second.HTTPServer.Login.PasswordHash {
		t.Fatal("the second password left the first hash in place")
	}
	if ok, _ := webauth.VerifyPassword(second.HTTPServer.Login.PasswordHash, "second-password-here"); !ok {
		t.Fatal("the rotated hash does not verify the new password")
	}
	if ok, _ := webauth.VerifyPassword(second.HTTPServer.Login.PasswordHash, "first-password-here"); ok {
		t.Fatal("the old password still verifies after a rotation")
	}
}

func TestServeSetPasswordRefusesWhatItWillNotWrite(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stdin string
		want  string
	}{
		{"empty", "\n", "empty"},
		{"too short", "short\n", "shorter"},
		{"whitespace only", "        \n", "empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, cfgPath := setPasswordHome(t, setPasswordBaseYAML)
			withStdin(t, tc.stdin)
			err := runServeSetPassword([]string{"--home", home, "--config", cfgPath, "--user", "pasha"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one naming %q", err, tc.want)
			}
			raw, readErr := os.ReadFile(cfgPath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(raw) != setPasswordBaseYAML {
				t.Fatalf("a refused password still changed the file:\n%s", raw)
			}
		})
	}
}

func TestDefaultAccountNameFollowsTheEnvironment(t *testing.T) {
	t.Setenv("USER", "pasha")
	t.Setenv("USERNAME", "")
	if got := defaultAccountName(); got != "pasha" {
		t.Fatalf("defaultAccountName = %q, want pasha", got)
	}
	t.Setenv("USER", "")
	t.Setenv("USERNAME", "windows-pasha")
	if got := defaultAccountName(); got != "windows-pasha" {
		t.Fatalf("defaultAccountName = %q, want windows-pasha", got)
	}
	t.Setenv("USERNAME", "")
	if got := defaultAccountName(); got != "admin" {
		t.Fatalf("defaultAccountName = %q, want admin", got)
	}
}

func TestOutOfBandLoginReadsTheEnvironment(t *testing.T) {
	t.Setenv(httpserver.LoginUserEnvVar, "  envuser  ")
	t.Setenv(httpserver.LoginPasswordEnvVar, "env-pass")
	got := outOfBandLogin()
	if got.User != "envuser" {
		t.Fatalf("user = %q, want the trimmed value", got.User)
	}
	// The password is taken verbatim: leading or trailing spaces may be part of it.
	if got.Password != "env-pass" {
		t.Fatalf("password = %q", got.Password)
	}
	if !got.IsSet() {
		t.Fatal("a complete account does not report itself as set")
	}

	t.Setenv(httpserver.LoginPasswordEnvVar, "")
	if outOfBandLogin().IsSet() {
		t.Fatal("half an account reports itself as set")
	}
}

func TestServeSetPasswordRefusesANameTheFileCannotCarry(t *testing.T) {
	// `login.user` may be an environment reference, so it is written literally;
	// a "$" in a name would therefore be read as one on the next load and the
	// account would change under the operator.
	home, cfgPath := setPasswordHome(t, setPasswordBaseYAML)
	withStdin(t, "a-long-enough-password\n")
	err := runServeSetPassword([]string{"--home", home, "--config", cfgPath, "--user", "pa$ha"})
	if err == nil || !strings.Contains(err.Error(), "environment reference") {
		t.Fatalf("error = %v, want one explaining the dollar sign", err)
	}
	raw, readErr := os.ReadFile(cfgPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(raw) != setPasswordBaseYAML {
		t.Fatalf("a refused name still changed the file:\n%s", raw)
	}
}
