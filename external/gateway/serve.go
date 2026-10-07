// Package gateway provides a pluggable messenger gateway for Coddy Agent.
//
// The adapters are behind the gateway build tags. This file is not: it carries
// the options a caller fills in and the flag that says whether this binary has
// any adapter at all, so `coddy serve` can read a configuration that asks for a
// bot and answer honestly in a build that has none.
package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// PromptSurfaces is where an adapter offers to show the permission prompt of a
// subagent whose parent turn has ended. `coddy serve` passes its runtime, which
// offers each such prompt to every surface of the process at once.
type PromptSurfaces interface {
	AddDetachedPermissionApprover(agent.DetachedPermissionBroker) (withdraw func())
}

// Options are what one gateway hub is built from.
type Options struct {
	// Cfg is the configuration the adapters start with.
	Cfg *config.Config
	// Mgr owns the sessions the chats talk to.
	Mgr *session.Manager
	// Log is the process logger.
	Log *slog.Logger
	// DefaultCWD is the workspace a chat session gets.
	DefaultCWD string
	// Mirror publishes a chat turn where other surfaces in this process can
	// watch it. Nil means nothing is watching.
	Mirror session.TurnMirror
	// Prompts is where a bot offers to ask about a detached subagent of one of
	// its chats. Nil means such a prompt is never shown in a chat.
	Prompts PromptSurfaces
	// Wakes is where a bot offers to run the turn a finished background task
	// starts in the session one of its chats is bound to, so the answer lands
	// in that chat. Nil means a woken turn never reaches a chat.
	Wakes agent.WakeSurfaces
	// WebUI says what the web UI of this process asks of a visitor under the
	// configuration given. A bot hands out the web UI's address - its menu
	// button, /app - only behind a sign-in or a token, or when the operator
	// said otherwise: the menu button is shown to everybody who opens the bot.
	// Nil reads as WebUIElsewhere.
	WebUI func(*config.Config) WebUIAccess
}

// WebUIAccess is what the web UI a bot would advertise asks of a visitor.
type WebUIAccess int

const (
	// WebUIElsewhere: this process does not serve the web UI, so there is
	// nothing here to check; the bot advertises it and says so in the log.
	WebUIElsewhere WebUIAccess = iota
	// WebUIGated: served by this process behind a sign-in or a token.
	WebUIGated
	// WebUIOpenByChoice: served with no credentials, and the operator said so
	// with httpserver.allow_insecure: true.
	WebUIOpenByChoice
	// WebUIOpen: served with no credentials; the bot does not advertise it.
	WebUIOpen
)

// Fingerprint is everything a rebuilt bot would read differently: the whole
// Telegram block and the token it resolves to, and, for a bot with a Mini
// App, the keys of the HTTP server that decide whether it may advertise the
// web UI (WebUIAccess). It is hashed, so that a key added to the block later
// rebuilds the bot on a reload without being listed here and the token never
// sits in the string the supervisor compares. coddy serve rebuilds the
// gateway in place when it moves: a sign-in set up from the settings screen
// is how a withheld menu button appears. A bot without a Mini App ignores the
// HTTP keys, so a new password does not cut its running turns short.
func Fingerprint(c *config.Config) string {
	if c == nil {
		return ""
	}
	tg := c.Gateways.Telegram
	type webUIKeys struct {
		HTTPEnabled   bool
		AuthToken     string
		Login         config.HTTPLoginConfig
		AllowInsecure bool
	}
	var web webUIKeys
	if strings.TrimSpace(tg.MiniApp.URL) != "" {
		web = webUIKeys{c.HTTPServer.IsEnabled(), c.HTTPServer.AuthToken, c.HTTPServer.Login, c.HTTPServer.AllowInsecure}
	}
	raw, err := json.Marshal(struct {
		Telegram config.TelegramGatewayConfig
		WebUI    webUIKeys
	}{tg, web})
	if err != nil {
		// Every field of the block marshals; this keeps the bot running on
		// what it has rather than rebuilding it on every reload.
		return "unmarshalable"
	}
	sum := sha256.New()
	sum.Write(raw)
	sum.Write([]byte{0})
	sum.Write([]byte(tg.EffectiveToken()))
	return hex.EncodeToString(sum.Sum(nil))
}

// PachcaFingerprint is everything a rebuilt Pachca bot would read
// differently: the whole Pachca block and the token it resolves to, hashed
// for the reasons Fingerprint gives. It is apart from the Telegram one, so a
// change to either bot rebuilds that bot alone.
func PachcaFingerprint(c *config.Config) string {
	if c == nil {
		return ""
	}
	pc := c.Gateways.Pachca
	raw, err := json.Marshal(pc)
	if err != nil {
		return "unmarshalable"
	}
	sum := sha256.New()
	sum.Write(raw)
	sum.Write([]byte{0})
	sum.Write([]byte(pc.EffectiveToken()))
	return hex.EncodeToString(sum.Sum(nil))
}
