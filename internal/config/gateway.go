package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// TelegramBotTokenEnvVar is the environment variable consulted for the bot token
// when gateways.telegram.token is left empty (so the token can live in .env instead
// of config.yaml). Mirrors the provider api_key → NAME_API_KEY convention.
const TelegramBotTokenEnvVar = "TELEGRAM_BOT_TOKEN"

// GatewayConfig is the root config block for all messenger gateways (built with -tags gateway or gateway.telegram).
type GatewayConfig struct {
	Telegram TelegramGatewayConfig `yaml:"telegram"`
}

// IsolationMode controls how sessions are scoped in a group chat.
type IsolationMode string

const (
	// IsolationIndividual gives each user their own session within the group.
	IsolationIndividual IsolationMode = "individual"
	// IsolationShared uses a single session for everyone in the group.
	IsolationShared IsolationMode = "shared"
	// IsolationAdmin only responds to admin users; all admins share one session.
	IsolationAdmin IsolationMode = "admin"
)

// AccessLevel controls who may interact with the bot in a chat.
type AccessLevel string

const (
	AccessAll    AccessLevel = "all"
	AccessAdmins AccessLevel = "admins"
	// AccessGroup:<name> — checked by prefix match at runtime.
)

// TelegramGatewayConfig configures the Telegram bot adapter.
type TelegramGatewayConfig struct {
	Enabled bool   `yaml:"enable"`
	Token   string `yaml:"token"`

	// Proxy is an optional outbound proxy for Telegram API requests.
	// Supported schemes: http, https, socks5, socks5h.
	// Example: "socks5h://127.0.0.1:1080" or "http://proxy.example.com:3128"
	Proxy string `yaml:"proxy"`

	// RichMessages enables Bot API 10.1 Rich Messages: the agent's native Markdown
	// (headings, tables, task lists, code, footnotes, LaTeX) is sent verbatim, tool
	// activity streams as a "Thinking…" placeholder, and executed tools are listed in
	// a collapsible block. Requires a Bot API server that supports 10.1; the gateway
	// falls back to legacy formatting if a rich send fails. Default false.
	RichMessages bool `yaml:"rich_messages"`

	// Admins is the list of Telegram user IDs with elevated permissions.
	Admins []int64 `yaml:"admins"`

	// DefaultAccess is the fallback access level for chats without a specific override.
	// Values: "all", "admins", "group:<name>".
	DefaultAccess AccessLevel `yaml:"default_access"`

	// DefaultIsolation is the fallback isolation mode for group chats.
	DefaultIsolation IsolationMode `yaml:"default_isolation"`

	// UserGroups defines named sets of user IDs for group-level access control.
	UserGroups []TelegramUserGroup `yaml:"user_groups"`

	// Chats holds per-chat overrides for isolation and access.
	Chats []TelegramChatConfig `yaml:"chats"`
}

// TelegramUserGroup is a named set of Telegram user IDs.
type TelegramUserGroup struct {
	Name    string  `yaml:"name"`
	UserIDs []int64 `yaml:"user_ids"`
}

// TelegramChatConfig is a per-chat override.
type TelegramChatConfig struct {
	ChatID    int64         `yaml:"chat_id"`
	Isolation IsolationMode `yaml:"isolation"`
	Access    AccessLevel   `yaml:"access"`
}

// Normalize trims whitespace in string fields.
func (t *TelegramGatewayConfig) Normalize() {
	t.Token = strings.TrimSpace(t.Token)
	t.Proxy = strings.TrimSpace(t.Proxy)
	t.DefaultAccess = AccessLevel(strings.TrimSpace(string(t.DefaultAccess)))
	t.DefaultIsolation = IsolationMode(strings.TrimSpace(string(t.DefaultIsolation)))
}

// ApplyDefaults fills zero values with safe defaults.
func (t *TelegramGatewayConfig) ApplyDefaults() {
	if t.DefaultAccess == "" {
		t.DefaultAccess = AccessAll
	}
	if t.DefaultIsolation == "" {
		t.DefaultIsolation = IsolationIndividual
	}
}

// EffectiveToken returns the configured token, or the TELEGRAM_BOT_TOKEN environment
// variable when token is left empty. Returns empty when neither is set.
func (t *TelegramGatewayConfig) EffectiveToken() string {
	if tok := strings.TrimSpace(t.Token); tok != "" {
		return tok
	}
	return strings.TrimSpace(os.Getenv(TelegramBotTokenEnvVar))
}

// Validate checks the Telegram config when enabled. The token is intentionally not
// required here: it may be supplied at runtime via the TELEGRAM_BOT_TOKEN environment
// variable (see EffectiveToken). The gateway logs a clear warning and skips the bot if
// no token can be resolved at startup.
func (t *TelegramGatewayConfig) Validate() error {
	if !t.Enabled {
		return nil
	}
	if t.Proxy != "" {
		u, err := url.Parse(t.Proxy)
		if err != nil {
			return fmt.Errorf("gateways.telegram.proxy: invalid URL: %w", err)
		}
		switch strings.ToLower(u.Scheme) {
		case "http", "https", "socks5", "socks5h":
		default:
			return fmt.Errorf("gateways.telegram.proxy: unsupported scheme %q (use http, https, socks5, or socks5h)", u.Scheme)
		}
	}
	return nil
}

// ChatConfig returns the per-chat override for chatID, or nil when no override exists.
func (t *TelegramGatewayConfig) ChatConfig(chatID int64) *TelegramChatConfig {
	for i := range t.Chats {
		if t.Chats[i].ChatID == chatID {
			return &t.Chats[i]
		}
	}
	return nil
}

// IsAdmin reports whether userID is in the admins list.
func (t *TelegramGatewayConfig) IsAdmin(userID int64) bool {
	for _, id := range t.Admins {
		if id == userID {
			return true
		}
	}
	return false
}

// UserGroupIDs returns the user IDs for the named group, or nil when not found.
func (t *TelegramGatewayConfig) UserGroupIDs(name string) []int64 {
	name = strings.TrimSpace(name)
	for _, g := range t.UserGroups {
		if g.Name == name {
			return g.UserIDs
		}
	}
	return nil
}
