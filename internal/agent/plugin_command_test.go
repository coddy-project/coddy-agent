package agent

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestParsePluginCommand(t *testing.T) {
	tests := []struct {
		in     string
		want   []string
		wantOK bool
	}{
		{"/plugin", nil, true},
		{"  /plugin  ", nil, true},
		{"/plugin marketplace list", []string{"marketplace", "list"}, true},
		{"/plugin   install   owner/repo", []string{"install", "owner/repo"}, true},
		{"/plugin\tmarketplace\tadd x", []string{"marketplace", "add", "x"}, true},
		{"/plugincast", nil, false},       // must not match a longer word
		{"say /plugin later", nil, false}, // only when it leads the message
		{"hello world", nil, false},
	}
	for _, tc := range tests {
		got, ok := parsePluginCommand(tc.in)
		if ok != tc.wantOK {
			t.Errorf("parsePluginCommand(%q) ok=%v, want %v", tc.in, ok, tc.wantOK)
			continue
		}
		if ok && !reflect.DeepEqual(got, tc.want) {
			t.Errorf("parsePluginCommand(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// A surface that took the tools away from the turn - a messenger user who is
// not the bot's admin - keeps /plugin to its usage screen and the list of
// installed skills: installing, updating or removing a skill changes what
// every session of the home runs.
func TestPluginCommandHonoursATurnRestriction(t *testing.T) {
	home := t.TempDir()
	st := &session.State{ID: "sess_restricted", CWD: t.TempDir(), Mode: session.ModeAgent}
	st.SetTurnRestriction(&session.TurnRestriction{AllowedTools: []string{"read"}, Note: "only the bot's admins can do this"})
	a := NewAgent(&config.Config{Paths: config.Paths{Home: home}}, st, nil, nil)
	reply := func(prompt string) string {
		t.Helper()
		if _, err := a.Run(context.Background(), []acp.ContentBlock{{Type: acp.ContentTypeText, Text: prompt}}); err != nil {
			t.Fatal(err)
		}
		msgs := st.GetMessages()
		return msgs[len(msgs)-1].Content
	}
	for _, prompt := range []string{"/plugin update", "/plugin install owner/repo", "/plugin remove rpa-feat", "/plugin marketplace add owner/repo"} {
		if got := reply(prompt); got != "/plugin: only the bot's admins can do this." {
			t.Errorf("%s answered %q, want the restriction's note", prompt, got)
		}
	}
	if _, err := os.Stat(config.GlobalMarketplacesPath(home)); !os.IsNotExist(err) {
		t.Fatalf("a restricted turn wrote the operator's marketplaces.json: %v", err)
	}
	if got := reply("/plugin"); !strings.Contains(got, "plugin commands:") {
		t.Errorf("/plugin answered %q, want the usage screen", got)
	}
}
