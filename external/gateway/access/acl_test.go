//go:build gateway || gateway.telegram || gateway.pachca

package access_test

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/access"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func cfg() *config.TelegramGatewayConfig {
	return &config.TelegramGatewayConfig{
		Admins:           []int64{100},
		DefaultAccess:    config.AccessAll,
		DefaultIsolation: config.IsolationIndividual,
		UserGroups: []config.TelegramUserGroup{
			{Name: "devs", UserIDs: []int64{200, 300}},
		},
	}
}

func TestCanAccess_All(t *testing.T) {
	c := cfg()
	if !access.CanAccess(999, config.AccessAll, c) {
		t.Fatal("everyone should pass AccessAll")
	}
}

func TestCanAccess_AdminsOnly(t *testing.T) {
	c := cfg()
	if !access.CanAccess(100, config.AccessAdmins, c) {
		t.Fatal("admin should pass AccessAdmins")
	}
	if access.CanAccess(200, config.AccessAdmins, c) {
		t.Fatal("non-admin should not pass AccessAdmins")
	}
}

func TestCanAccess_Group(t *testing.T) {
	c := cfg()
	if !access.CanAccess(200, "group:devs", c) {
		t.Fatal("group member should pass")
	}
	if !access.CanAccess(100, "group:devs", c) {
		t.Fatal("admin should always pass group check")
	}
	if access.CanAccess(999, "group:devs", c) {
		t.Fatal("outsider should not pass group check")
	}
}

func TestEffectiveIsolation_Override(t *testing.T) {
	c := cfg()
	c.Chats = []config.TelegramChatConfig{
		{ChatID: -1001, Isolation: config.IsolationShared, Access: config.AccessAll},
	}
	if got := access.EffectiveIsolation(-1001, c); got != config.IsolationShared {
		t.Fatalf("want shared got %s", got)
	}
	if got := access.EffectiveIsolation(-9999, c); got != config.IsolationIndividual {
		t.Fatalf("want individual got %s", got)
	}
}

// TestPolicy_PachcaReadsLikeTelegram holds the Pachca block to the same rules.
func TestPolicy_PachcaReadsLikeTelegram(t *testing.T) {
	p := &config.PachcaGatewayConfig{
		Admins:           []int64{1},
		DefaultAccess:    "group:ops",
		DefaultIsolation: config.IsolationShared,
		UserGroups:       []config.GatewayUserGroup{{Name: "ops", UserIDs: []int64{2}}},
		Chats:            []config.GatewayChatConfig{{ChatID: 50, Access: config.AccessAdmins, Isolation: config.IsolationAdmin}},
	}
	if lvl := access.EffectiveAccess(49, p); !access.CanAccess(2, lvl, p) || access.CanAccess(3, lvl, p) || !access.CanAccess(1, lvl, p) {
		t.Fatalf("default group access misread: %s", lvl)
	}
	if lvl := access.EffectiveAccess(50, p); access.CanAccess(2, lvl, p) || !access.CanAccess(1, lvl, p) {
		t.Fatalf("per-chat override misread: %s", lvl)
	}
	if got := access.EffectiveIsolation(49, p); got != config.IsolationShared {
		t.Fatalf("default isolation: %s", got)
	}
	if got := access.EffectiveIsolation(50, p); got != config.IsolationAdmin {
		t.Fatalf("override isolation: %s", got)
	}
}

func TestKeyIsAdmin(t *testing.T) {
	p := &config.PachcaGatewayConfig{Admins: []int64{7}}
	cases := map[string]bool{
		"tg:user:7": true, "tg:user:8": false,
		"tg:chat:-1:user:7": true, "tg:chat:-1:user:8": false,
		"tg:chat:-1": false, "tg:chat:-1:admin": true,
		"$last_model": false, "tg:user:x": false,
	}
	for key, want := range cases {
		if got := access.KeyIsAdmin(key, p); got != want {
			t.Errorf("KeyIsAdmin(%q) = %v, want %v", key, got, want)
		}
	}
	r := access.NonAdminTurn()
	if !r.AskAlways || !r.ConfineToWorkspace || r.Allows("config_commit") || r.Allows("switch_model") || r.Allows("run_command") || r.Allows("github__create_issue") || r.Allows("load_skill") || !r.Allows("read") {
		t.Fatalf("restriction: %+v", r)
	}
}
