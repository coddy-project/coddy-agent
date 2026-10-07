package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// TestConfigJSON_PreservesTelegramGateway is the regression for the data-loss bug:
// saving config through the HTTP UI (ConfigToJSONDTO → JSON → ParseAndValidateConfigJSON
// → MarshalConfigYAML) used to drop the entire gateways block, disabling the bot.
func TestConfigJSON_PreservesTelegramGateway(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvCODDYHome, home)
	yml := `
providers:
  - name: openai
    type: openai
    api_key: "k"
models:
  - model: "openai/gpt-4o"
    max_tokens: 4096
    temperature: 0.1
agent:
  model: "openai/gpt-4o"
gateways:
  telegram:
    enable: true
    token: "secret-token"
    rich_messages: true
    proxy: "socks5h://127.0.0.1:1080"
    admins: [111, 222]
    default_access: "admins"
    default_isolation: "shared"
    user_groups:
      - name: "devs"
        user_ids: [333, 444]
    chats:
      - chat_id: -100123
        isolation: "individual"
        access: "all"
    mini_app:
      url: "https://coddy.example.com/"
      menu_button: false
`
	p := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(p, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate the PUT /coddy/config round-trip.
	raw, err := json.Marshal(config.ConfigToJSONDTO(cfg))
	if err != nil {
		t.Fatal(err)
	}
	cfg2, err := config.ParseAndValidateConfigJSON(raw, cfg.Paths)
	if err != nil {
		t.Fatalf("ParseAndValidateConfigJSON: %v", err)
	}

	tg := cfg2.Gateways.Telegram
	if !tg.Enabled {
		t.Fatal("telegram enable was lost in the config round-trip")
	}
	if tg.Token != "secret-token" {
		t.Fatalf("token: want secret-token got %q", tg.Token)
	}
	if !tg.RichMessages {
		t.Fatal("rich_messages was lost")
	}
	if tg.Proxy != "socks5h://127.0.0.1:1080" {
		t.Fatalf("proxy: got %q", tg.Proxy)
	}
	if len(tg.Admins) != 2 || tg.Admins[0] != 111 || tg.Admins[1] != 222 {
		t.Fatalf("admins: got %v", tg.Admins)
	}
	if tg.DefaultAccess != config.AccessAdmins {
		t.Fatalf("default_access: got %q", tg.DefaultAccess)
	}
	if tg.DefaultIsolation != config.IsolationShared {
		t.Fatalf("default_isolation: got %q", tg.DefaultIsolation)
	}
	if len(tg.UserGroups) != 1 || tg.UserGroups[0].Name != "devs" ||
		len(tg.UserGroups[0].UserIDs) != 2 || tg.UserGroups[0].UserIDs[0] != 333 {
		t.Fatalf("user_groups: got %+v", tg.UserGroups)
	}
	if len(tg.Chats) != 1 || tg.Chats[0].ChatID != -100123 ||
		tg.Chats[0].Isolation != config.IsolationIndividual || tg.Chats[0].Access != config.AccessAll {
		t.Fatalf("chats: got %+v", tg.Chats)
	}

	if tg.MiniApp.URL != "https://coddy.example.com/" || tg.MiniApp.MenuButton == nil || *tg.MiniApp.MenuButton {
		t.Fatalf("mini_app: got %+v", tg.MiniApp)
	}

	// And the YAML that gets written to disk must still contain the secret + flag.
	yb, err := config.MarshalConfigYAML(cfg2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(yb), "secret-token") || !strings.Contains(string(yb), "rich_messages") {
		t.Fatalf("serialized YAML dropped telegram settings:\n%s", yb)
	}
}

func TestUISchema_HasTelegramGatewayFields(t *testing.T) {
	doc := config.UISchemaMap()
	props, ok := doc["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("properties")
	}
	gw, ok := props["gateways"].(map[string]interface{})
	if !ok {
		t.Fatal("schema missing gateways")
	}
	gwProps, ok := gw["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("gateways has no properties")
	}
	tg, ok := gwProps["telegram"].(map[string]interface{})
	if !ok {
		t.Fatal("gateways missing telegram")
	}
	tgProps, ok := tg["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("telegram has no properties")
	}
	for _, key := range []string{"enable", "token", "rich_messages", "admins", "default_access", "default_isolation", "user_groups", "chats", "mini_app"} {
		if _, ok := tgProps[key].(map[string]interface{}); !ok {
			t.Fatalf("telegram schema missing property %q", key)
		}
	}
}

// TestUISchemaCoversConfigJSONFields locks DTO ↔ schema parity so a future config
// field cannot silently disappear from the Settings UI again.
func TestUISchemaCoversConfigJSONFields(t *testing.T) {
	if err := config.UISchemaCoversConfigJSONFields(); err != nil {
		t.Fatal(err)
	}
}

// TestConfigJSON_PreservesPachcaGateway holds the Pachca block to the same
// settings-editor round trip the Telegram one survives.
func TestConfigJSON_PreservesPachcaGateway(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvCODDYHome, home)
	yml := `
providers:
  - name: openai
    type: openai
    api_key: "k"
models:
  - model: "openai/gpt-4o"
agent:
  model: "openai/gpt-4o"
gateways:
  pachca:
    enable: true
    token: "pachca-secret"
    proxy: "http://u:pa$$word@127.0.0.1:3128"
    poll_interval_seconds: 5
    admins: [7]
    default_access: "group:devs"
    default_isolation: "shared"
    user_groups:
      - name: "devs"
        user_ids: [8, 9]
    chats:
      - chat_id: 50001
        isolation: "admin"
        access: "admins"
`
	p := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(p, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(config.ConfigToJSONDTO(cfg))
	if err != nil {
		t.Fatal(err)
	}
	cfg2, err := config.ParseAndValidateConfigJSON(raw, cfg.Paths)
	if err != nil {
		t.Fatalf("ParseAndValidateConfigJSON: %v", err)
	}
	pc := cfg2.Gateways.Pachca
	if !pc.Enabled || pc.Token != "pachca-secret" || pc.PollIntervalSeconds != 5 {
		t.Fatalf("pachca scalars lost: %+v", pc)
	}
	if pc.Proxy != "http://u:pa$word@127.0.0.1:3128" {
		t.Fatalf("proxy: got %q", pc.Proxy)
	}
	if len(pc.Admins) != 1 || pc.Admins[0] != 7 || pc.DefaultAccess != "group:devs" || pc.DefaultIsolation != config.IsolationShared {
		t.Fatalf("access settings lost: %+v", pc)
	}
	if len(pc.UserGroups) != 1 || len(pc.UserGroups[0].UserIDs) != 2 || len(pc.Chats) != 1 ||
		pc.Chats[0].ChatID != 50001 || pc.Chats[0].Isolation != config.IsolationAdmin {
		t.Fatalf("groups/chats lost: %+v", pc)
	}
	yb, err := config.MarshalConfigYAML(cfg2)
	if err != nil {
		t.Fatal(err)
	}
	// The "$" of the proxy password is doubled on disk, so the next load reads
	// it as a literal rather than an environment reference.
	if !strings.Contains(string(yb), "pachca-secret") || !strings.Contains(string(yb), "pa$$word") {
		t.Fatalf("serialized YAML dropped pachca settings:\n%s", yb)
	}
}

func TestPachcaConfig_DefaultsAndValidation(t *testing.T) {
	c := &config.PachcaGatewayConfig{Enabled: true}
	c.Normalize()
	c.ApplyDefaults()
	if c.DefaultAccess != config.AccessAll || c.DefaultIsolation != config.IsolationIndividual {
		t.Fatalf("defaults: %+v", c)
	}
	if got := c.PollInterval().Seconds(); got != config.PachcaPollIntervalDefault {
		t.Fatalf("poll interval default: %v", got)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("enabled pachca with no token must validate (token may come from env): %v", err)
	}
	for _, n := range []int{-1, 61} {
		c.PollIntervalSeconds = n
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), "gateways.pachca.poll_interval_seconds") {
			t.Fatalf("poll_interval_seconds %d: want a keyed error, got %v", n, err)
		}
	}
	c.PollIntervalSeconds = 0
	c.Proxy = "ftp://nope"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "gateways.pachca.proxy") {
		t.Fatalf("bad proxy: want a keyed error, got %v", err)
	}
	off := &config.PachcaGatewayConfig{Proxy: "ftp://nope", PollIntervalSeconds: 999}
	if err := off.Validate(); err != nil {
		t.Fatalf("a disabled pachca block is not validated: %v", err)
	}
}

func TestPachcaConfig_EffectiveToken(t *testing.T) {
	t.Setenv(config.PachcaBotTokenEnvVar, "from-env")
	if got := (&config.PachcaGatewayConfig{Token: " from-config "}).EffectiveToken(); got != "from-config" {
		t.Fatalf("config token wins: got %q", got)
	}
	if got := (&config.PachcaGatewayConfig{}).EffectiveToken(); got != "from-env" {
		t.Fatalf("env fallback: got %q", got)
	}
}
