package config

import (
	"strings"
	"testing"
)

// Telegram token follows the same "optional in config, resolved from env at runtime"
// pattern as provider api_key: the user may keep it in .env / the environment instead
// of writing it into config.yaml.

func TestTelegramConfig_TokenOptionalWhenEnabled(t *testing.T) {
	c := &TelegramGatewayConfig{Enabled: true} // no token
	c.Normalize()
	c.ApplyDefaults()
	if err := c.Validate(); err != nil {
		t.Fatalf("enabled telegram with no token must validate (token may come from env), got: %v", err)
	}
}

func TestTelegramConfig_ProxyStillValidatedWithoutToken(t *testing.T) {
	c := &TelegramGatewayConfig{Enabled: true, Proxy: "ftp://nope"}
	c.Normalize()
	c.ApplyDefaults()
	if err := c.Validate(); err == nil {
		t.Fatal("invalid proxy scheme must still fail validation")
	}
}

func TestTelegramConfig_EffectiveToken(t *testing.T) {
	// Configured token wins.
	c := &TelegramGatewayConfig{Token: "from-config"}
	t.Setenv(TelegramBotTokenEnvVar, "from-env")
	if got := c.EffectiveToken(); got != "from-config" {
		t.Fatalf("configured token should win, got %q", got)
	}

	// Empty token falls back to the environment variable.
	c2 := &TelegramGatewayConfig{}
	t.Setenv(TelegramBotTokenEnvVar, "env-token")
	if got := c2.EffectiveToken(); got != "env-token" {
		t.Fatalf("empty token should resolve from %s, got %q", TelegramBotTokenEnvVar, got)
	}

	// Empty token and no env var resolves to empty.
	c3 := &TelegramGatewayConfig{}
	t.Setenv(TelegramBotTokenEnvVar, "")
	if got := c3.EffectiveToken(); got != "" {
		t.Fatalf("no token anywhere should be empty, got %q", got)
	}
}

// The Mini App address is where Telegram opens the web UI: https, or plain
// http on this machine for the offline stand, and no fragment, which carries
// Telegram's launch parameters. It is checked while the gateway is enabled,
// like the rest of the block, and the error names the key.
func TestTelegramConfig_MiniAppURL(t *testing.T) {
	for _, tc := range []struct {
		url string
		ok  bool
	}{
		{"", true},
		{"https://coddy.example.com/", true},
		{"https://coddy.example.com/coddy/?team=a", true},
		{"  https://coddy.example.com  ", true},
		{"http://127.0.0.1:18792/", true},
		{"http://localhost:8080", true},
		{"http://[::1]:8080/", true},
		{"http://coddy.example.com/", false},
		{"https://coddy.example.com/#/s/sess_1", false},
		{"https://user:pw@coddy.example.com/", false},
		{"coddy.example.com", false},
		{"/coddy", false},
		{"ftp://coddy.example.com/", false},
	} {
		c := &TelegramGatewayConfig{Enabled: true, MiniApp: TelegramMiniAppConfig{URL: tc.url}}
		c.Normalize()
		c.ApplyDefaults()
		err := c.Validate()
		if tc.ok && err != nil {
			t.Errorf("mini_app.url %q: %v", tc.url, err)
		}
		if !tc.ok && (err == nil || !strings.Contains(err.Error(), "gateways.telegram.mini_app.url")) {
			t.Errorf("mini_app.url %q: want an error naming gateways.telegram.mini_app.url, got %v", tc.url, err)
		}
	}
	off := &TelegramGatewayConfig{MiniApp: TelegramMiniAppConfig{URL: "http://coddy.example.com/"}}
	if err := off.Validate(); err != nil {
		t.Fatalf("a disabled gateway is not checked: %v", err)
	}
	trimmed := &TelegramGatewayConfig{MiniApp: TelegramMiniAppConfig{URL: "  https://coddy.example.com/ "}}
	trimmed.Normalize()
	if trimmed.MiniApp.URL != "https://coddy.example.com/" {
		t.Fatalf("Normalize leaves %q", trimmed.MiniApp.URL)
	}
}

func TestTelegramConfig_MiniAppMenuButton(t *testing.T) {
	no, yes := false, true
	for _, tc := range []struct {
		app  TelegramMiniAppConfig
		want bool
	}{
		{TelegramMiniAppConfig{}, false},
		{TelegramMiniAppConfig{MenuButton: &yes}, false},
		{TelegramMiniAppConfig{URL: "https://coddy.example.com/"}, true},
		{TelegramMiniAppConfig{URL: "https://coddy.example.com/", MenuButton: &yes}, true},
		{TelegramMiniAppConfig{URL: "https://coddy.example.com/", MenuButton: &no}, false},
	} {
		if got := tc.app.MenuButtonEnabled(); got != tc.want {
			t.Errorf("%+v: MenuButtonEnabled = %v, want %v", tc.app, got, tc.want)
		}
	}
}
