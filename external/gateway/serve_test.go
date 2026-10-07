package gateway

import (
	"reflect"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// TestFingerprintFollowsEveryTelegramKey sets each field of the Telegram block
// in turn and expects the fingerprint to move, so a key added to the block
// later rebuilds the bot on a reload without anyone listing it anywhere.
func TestFingerprintFollowsEveryTelegramKey(t *testing.T) {
	base := Fingerprint(&config.Config{})
	if base == "" || base != Fingerprint(&config.Config{}) {
		t.Fatalf("the fingerprint of an empty block is not stable: %q", base)
	}
	typ := reflect.TypeOf(config.TelegramGatewayConfig{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		var c config.Config
		v := reflect.ValueOf(&c.Gateways.Telegram).Elem().Field(i)
		setNonZero(t, field.Name, v)
		if got := Fingerprint(&c); got == base {
			t.Errorf("changing gateways.telegram %s does not move the fingerprint", field.Name)
		}
	}
	if Fingerprint(nil) != "" {
		t.Fatal("no config, no fingerprint")
	}
}

// TestFingerprintFollowsTheTokenFromTheEnvironment: an empty token reads
// TELEGRAM_BOT_TOKEN, and a rotated variable is a different bot.
func TestFingerprintFollowsTheTokenFromTheEnvironment(t *testing.T) {
	t.Setenv(config.TelegramBotTokenEnvVar, "1:a")
	a := Fingerprint(&config.Config{})
	t.Setenv(config.TelegramBotTokenEnvVar, "1:b")
	if b := Fingerprint(&config.Config{}); a == b {
		t.Fatal("a token rotated in the environment does not move the fingerprint")
	}
}

// setNonZero gives v a value other than its zero, whatever its kind; nested
// structs get every field set.
func setNonZero(t *testing.T, name string, v reflect.Value) {
	t.Helper()
	switch v.Kind() {
	case reflect.Bool:
		v.SetBool(true)
	case reflect.String:
		v.SetString("x")
	case reflect.Int, reflect.Int64, reflect.Int32:
		v.SetInt(7)
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 1, 1)
		setNonZero(t, name, s.Index(0))
		v.Set(s)
	case reflect.Pointer:
		p := reflect.New(v.Type().Elem())
		setNonZero(t, name, p.Elem())
		v.Set(p)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			setNonZero(t, name+"."+v.Type().Field(i).Name, v.Field(i))
		}
	default:
		t.Fatalf("field %s: kind %s is not handled; extend setNonZero", name, v.Kind())
	}
}

// The keys of the HTTP server that decide whether the bot may advertise the
// web UI rebuild the bot when they move: a sign-in set up from the settings
// screen is how a withheld menu button appears.
func TestFingerprintFollowsTheWebUIAccessKeys(t *testing.T) {
	app := config.GatewayConfig{Telegram: config.TelegramGatewayConfig{
		MiniApp: config.TelegramMiniAppConfig{URL: "https://coddy.example.com/"}}}
	base := Fingerprint(&config.Config{Gateways: app})
	off := false
	for name, c := range map[string]*config.Config{
		"enable":         {Gateways: app, HTTPServer: config.HTTPServerConfig{Enabled: &off}},
		"auth_token":     {Gateways: app, HTTPServer: config.HTTPServerConfig{AuthToken: "t"}},
		"login":          {Gateways: app, HTTPServer: config.HTTPServerConfig{Login: config.HTTPLoginConfig{User: "op", PasswordHash: "h"}}},
		"allow_insecure": {Gateways: app, HTTPServer: config.HTTPServerConfig{AllowInsecure: true}},
	} {
		if Fingerprint(c) == base {
			t.Errorf("httpserver.%s does not move the gateway fingerprint", name)
		}
	}
}

// A bot without a Mini App does not care how the web UI is reached, so a new
// password or token for it does not rebuild the bot, which would cut the
// turns it is running short.
func TestFingerprintIgnoresTheWebUIKeysWithoutAMiniApp(t *testing.T) {
	base := &config.Config{}
	base.Gateways.Telegram.Enabled = true
	base.HTTPServer.Login = config.HTTPLoginConfig{User: "op", PasswordHash: "h1"}
	off := false
	for name, edit := range map[string]func(c *config.Config){
		"login":          func(c *config.Config) { c.HTTPServer.Login.PasswordHash = "h2" },
		"auth_token":     func(c *config.Config) { c.HTTPServer.AuthToken = "rotated" },
		"enable":         func(c *config.Config) { c.HTTPServer.Enabled = &off },
		"allow_insecure": func(c *config.Config) { c.HTTPServer.AllowInsecure = true },
	} {
		c := *base
		edit(&c)
		if Fingerprint(&c) != Fingerprint(base) {
			t.Errorf("httpserver.%s rebuilds a bot that has no Mini App", name)
		}
	}
}
