package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"rsc.io/qr"

	"github.com/EvilFreelancer/coddy-agent/external/httpserver"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/webauth"
)

const totpCLISecret = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"

// fixTOTP pins the secret the command generates and the clock it checks the
// confirming code against, and returns the code the app would show.
func fixTOTP(t *testing.T) string {
	t.Helper()
	now := time.Unix(1_800_000_000, 0)
	prevSecret, prevNow := newTOTPSecret, totpNow
	newTOTPSecret = func() (string, error) { return totpCLISecret, nil }
	totpNow = func() time.Time { return now }
	t.Cleanup(func() { newTOTPSecret, totpNow = prevSecret, prevNow })
	code, err := webauth.TOTPCode(totpCLISecret, now)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// clearLoginEnv keeps an account in the test runner's environment out of the
// account name the command picks.
func clearLoginEnv(t *testing.T) {
	t.Helper()
	t.Setenv(httpserver.LoginUserEnvVar, "")
	t.Setenv(httpserver.LoginPasswordEnvVar, "")
}

func homeWithAccount(t *testing.T) (home, cfgPath string) {
	t.Helper()
	clearLoginEnv(t)
	home, cfgPath = setPasswordHome(t, setPasswordBaseYAML)
	withStdin(t, "correct-horse-battery-staple\n")
	if err := runServeSetPassword([]string{"--home", home, "--config", cfgPath, "--user", "operator"}); err != nil {
		t.Fatalf("set-password: %v", err)
	}
	return home, cfgPath
}

func loadedLogin(t *testing.T, home, cfgPath string) config.HTTPLoginConfig {
	t.Helper()
	paths, err := config.Resolve(config.CLIPaths{Home: home, Config: cfgPath})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadWithPaths(paths)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return cfg.HTTPServer.Login
}

func TestServeSetTOTPWritesTheKeyOnceTheAppConfirmsIt(t *testing.T) {
	home, cfgPath := homeWithAccount(t)
	code := fixTOTP(t)
	withStdin(t, code+"\n")
	if err := runServeSetTOTP([]string{"--home", home, "--config", cfgPath}); err != nil {
		t.Fatalf("set-totp: %v", err)
	}
	login := loadedLogin(t, home, cfgPath)
	if login.TOTPSecret != totpCLISecret {
		t.Fatalf("totp_secret = %q, want the generated key", login.TOTPSecret)
	}
	if login.User != "operator" || login.PasswordHash == "" {
		t.Fatalf("the account was disturbed: %+v", login)
	}
	raw, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(raw), "# a comment the operator wrote") {
		t.Fatal("the operator's comment was lost")
	}
}

func TestServeSetTOTPAcceptsASecondTry(t *testing.T) {
	home, cfgPath := homeWithAccount(t)
	code := fixTOTP(t)
	withStdin(t, "000000\n"+code+"\n")
	if err := runServeSetTOTP([]string{"--home", home, "--config", cfgPath}); err != nil {
		t.Fatalf("set-totp: %v", err)
	}
	if login := loadedLogin(t, home, cfgPath); !login.HasTOTP() {
		t.Fatal("the key was not written after a correct second try")
	}
}

func TestServeSetTOTPWritesNothingWithoutAConfirmingCode(t *testing.T) {
	home, cfgPath := homeWithAccount(t)
	code := fixTOTP(t)
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	for name, input := range map[string]string{
		"wrong codes": strings.Repeat(wrong+"\n", totpConfirmAttempts),
		"no input":    "",
	} {
		t.Run(name, func(t *testing.T) {
			before, _ := os.ReadFile(cfgPath)
			withStdin(t, input)
			err := runServeSetTOTP([]string{"--home", home, "--config", cfgPath})
			if err == nil || !strings.Contains(err.Error(), "nothing was written") {
				t.Fatalf("err = %v, want a refusal that wrote nothing", err)
			}
			after, _ := os.ReadFile(cfgPath)
			if string(before) != string(after) {
				t.Fatal("config.yaml changed although the app never confirmed the key")
			}
		})
	}
}

func TestServeSetTOTPNeedsAnAccount(t *testing.T) {
	clearLoginEnv(t)
	home, cfgPath := setPasswordHome(t, setPasswordBaseYAML)
	fixTOTP(t)
	withStdin(t, "")
	err := runServeSetTOTP([]string{"--home", home, "--config", cfgPath})
	if err == nil || !strings.Contains(err.Error(), "set-password") {
		t.Fatalf("err = %v, want one pointing at set-password", err)
	}
}

func TestServeSetTOTPDisableRemovesTheKey(t *testing.T) {
	home, cfgPath := homeWithAccount(t)
	withStdin(t, fixTOTP(t)+"\n")
	if err := runServeSetTOTP([]string{"--home", home, "--config", cfgPath}); err != nil {
		t.Fatalf("set-totp: %v", err)
	}
	if err := runServeSetTOTP([]string{"--home", home, "--config", cfgPath, "--disable"}); err != nil {
		t.Fatalf("set-totp --disable: %v", err)
	}
	login := loadedLogin(t, home, cfgPath)
	if login.HasTOTP() {
		t.Fatal("--disable left the key in place")
	}
	if login.User != "operator" || login.PasswordHash == "" {
		t.Fatalf("--disable disturbed the account: %+v", login)
	}
	// A second --disable has nothing to do and says so without failing.
	if err := runServeSetTOTP([]string{"--home", home, "--config", cfgPath, "--disable"}); err != nil {
		t.Fatalf("second --disable: %v", err)
	}
}

func TestRenderQRDrawsTheCodeWithItsBorder(t *testing.T) {
	code, err := qr.Encode(webauth.TOTPKeyURI("operator", totpCLISecret), qr.M)
	if err != nil {
		t.Fatal(err)
	}
	side := code.Size + 2*qrQuietZone
	plain := strings.Split(strings.TrimSuffix(renderQR(code, false), "\n"), "\n")
	if len(plain) != (side+1)/2 {
		t.Fatalf("%d lines, want %d for a %d-module side", len(plain), (side+1)/2, side)
	}
	for i, line := range plain {
		if n := len([]rune(line)); n != side {
			t.Fatalf("line %d is %d characters, want %d", i, n, side)
		}
	}
	if strings.TrimSpace(plain[0]) != "" {
		t.Fatal("the first line is not the light border")
	}
	coloured := renderQR(code, true)
	if !strings.Contains(coloured, "\x1b[30;") || !strings.HasSuffix(coloured, "\x1b[0m\n") {
		t.Fatal("the terminal rendering does not set its colours or reset them")
	}
}

func TestGroupSecret(t *testing.T) {
	if got := groupSecret("JBSWY3DPEHPK3PXPJB"); got != "JBSW Y3DP EHPK 3PXP JB" {
		t.Fatalf("groupSecret = %q", got)
	}
}
