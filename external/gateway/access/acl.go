//go:build gateway || gateway.telegram || gateway.pachca

// Package access implements access control for the messenger gateway.
package access

import (
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// Policy is what a gateway's configuration answers about access: the admins,
// the named user groups, the defaults and the per-chat overrides. Every
// adapter's config block reads the same way, so one set of rules serves all.
type Policy interface {
	IsAdmin(userID int64) bool
	UserGroupIDs(name string) []int64
	AccessDefault() config.AccessLevel
	IsolationDefault() config.IsolationMode
	ChatOverride(chatID int64) *config.GatewayChatConfig
}

// CanAccess reports whether userID is allowed to interact given the effective access level.
func CanAccess(userID int64, level config.AccessLevel, p Policy) bool {
	switch level {
	case config.AccessAdmins:
		return p.IsAdmin(userID)
	case config.AccessAll:
		return true
	default:
		// "group:<name>" prefix
		if name, ok := groupName(string(level)); ok {
			ids := p.UserGroupIDs(name)
			for _, id := range ids {
				if id == userID {
					return true
				}
			}
			// Admins always pass group checks too.
			return p.IsAdmin(userID)
		}
		return false
	}
}

// EffectiveAccess returns the per-chat access override or the global default.
func EffectiveAccess(chatID int64, p Policy) config.AccessLevel {
	if cc := p.ChatOverride(chatID); cc != nil && cc.Access != "" {
		return cc.Access
	}
	return p.AccessDefault()
}

// EffectiveIsolation returns the per-chat isolation mode or the global default.
func EffectiveIsolation(chatID int64, p Policy) config.IsolationMode {
	if cc := p.ChatOverride(chatID); cc != nil && cc.Isolation != "" {
		return cc.Isolation
	}
	return p.IsolationDefault()
}

func groupName(s string) (string, bool) {
	const prefix = "group:"
	if strings.HasPrefix(s, prefix) {
		return s[len(prefix):], true
	}
	return "", false
}
