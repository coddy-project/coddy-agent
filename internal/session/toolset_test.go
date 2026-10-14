package session_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// toolsetTestConfig is settingsTestConfig with two presets, the Qwen model
// naming one of them as its own.
func toolsetTestConfig() *config.Config {
	cfg := settingsTestConfig()
	cfg.Toolsets = []config.Toolset{
		{Name: "coding", Tools: []string{"read", "grep", "edit"}},
		{Name: "no-net", DisallowedTools: []string{"webfetch", "websearch"}},
	}
	for i := range cfg.Models {
		if cfg.Models[i].Model == "nd/qwen3.8-27b" {
			cfg.Models[i].Toolset = "coding"
		}
	}
	return cfg
}

// toolsetRecorder notes the toolset choice every turn ran with.
type toolsetRecorder struct{ turns []string }

func (r *toolsetRecorder) run(_ context.Context, st *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
	r.turns = append(r.turns, st.EffectiveToolset())
	return string(acp.StopReasonEndTurn), nil
}

func TestParseToolsetCommand(t *testing.T) {
	line, err := session.ParseSettingsCommands("/model nd/qwen3.8-27b /toolset no-net --once review this")
	if err != nil {
		t.Fatal(err)
	}
	if line.Session.Model == nil || *line.Session.Model != "nd/qwen3.8-27b" || line.Session.Toolset != nil {
		t.Fatalf("session change = %+v", line.Session)
	}
	if len(line.Turns) != 1 || line.Turns[0].Toolset == nil || *line.Turns[0].Toolset != "no-net" || line.Turns[0].Turns != 1 {
		t.Fatalf("turn changes = %+v", line.Turns)
	}
	if line.Rest != "review this" {
		t.Fatalf("rest = %q", line.Rest)
	}
	if _, err := session.ParseSettingsCommands("/toolset"); err == nil {
		t.Fatal("/toolset without a value was accepted")
	}
	v := "coding"
	if got := session.FormatSettingsCommands([]session.SettingsChange{{Toolset: &v, Turns: 2}}); got != "/toolset coding --count=2" {
		t.Fatalf("formatted = %q", got)
	}
}

// The session keeps the toolset it picked; the selector shows it, or the
// model's own while it picked none; default picks none; a name the
// configuration does not have is refused.
func TestApplySessionToolset(t *testing.T) {
	m := session.NewManager(toolsetTestConfig(), noopSender{}, noopRunner, slog.Default(), "", nil)
	res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	snap, err := m.SessionSettings(res.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Toolset != "default" || strings.Join(snap.ToolsetChoices, ",") != "coding,no-net,default" {
		t.Fatalf("a session on a model without a toolset = %q of %v", snap.Toolset, snap.ToolsetChoices)
	}

	model := "nd/qwen3.8-27b"
	if snap, err = m.ApplySessionSettings(ctx, res.SessionID, session.SettingsChange{Model: &model}); err != nil {
		t.Fatal(err)
	}
	st := m.SessionByID(res.SessionID)
	if snap.Toolset != "coding" || st.GetSelectedToolset() != "" || st.EffectiveToolset() != "" {
		t.Fatalf("on the model naming coding: snapshot %q, picked %q", snap.Toolset, st.GetSelectedToolset())
	}

	for _, pick := range []string{"no-net", "default"} {
		v := pick
		if snap, err = m.ApplySessionSettings(ctx, res.SessionID, session.SettingsChange{Toolset: &v}); err != nil {
			t.Fatal(err)
		}
		if snap.Toolset != pick || st.GetSelectedToolset() != pick || st.EffectiveToolset() != pick {
			t.Fatalf("picked %s: snapshot %q, stored %q", pick, snap.Toolset, st.GetSelectedToolset())
		}
	}
	// A pick of the session survives a switch of its model.
	other := "p1/gpt-5"
	if snap, err = m.ApplySessionSettings(ctx, res.SessionID, session.SettingsChange{Model: &other}); err != nil || snap.Toolset != "default" {
		t.Fatalf("after a model switch = %q, %v", snap.Toolset, err)
	}

	bad := "codding"
	if _, err := m.ApplySessionSettings(ctx, res.SessionID, session.SettingsChange{Toolset: &bad}); err == nil ||
		!strings.Contains(err.Error(), `unknown toolset "codding" (configured: coding, no-net; default offers every tool)`) {
		t.Fatalf("an unknown toolset: %v", err)
	}
}

// Without a preset there is nothing to pick: the snapshot carries no toolset,
// the menus no /toolset, and only default is accepted.
func TestNoToolsetsNoChoice(t *testing.T) {
	m := session.NewManager(settingsTestConfig(), noopSender{}, noopRunner, slog.Default(), "", nil)
	res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := m.SessionSettings(res.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Toolset != "" || len(snap.ToolsetChoices) != 0 {
		t.Fatalf("snapshot = %q of %v, want none", snap.Toolset, snap.ToolsetChoices)
	}
	for _, row := range session.BuiltinCommandRows(settingsTestConfig(), m.SessionByID(res.SessionID), nil) {
		if row.Name == "toolset" {
			t.Fatalf("the menu offers /toolset without a preset: %+v", row)
		}
	}
	def := "default"
	if _, err := m.ApplySessionSettings(context.Background(), res.SessionID, session.SettingsChange{Toolset: &def}); err != nil {
		t.Fatalf("default without a preset: %v", err)
	}

	cfg := toolsetTestConfig()
	var row *session.CommandRow
	for _, r := range session.BuiltinCommandRows(cfg, nil, nil) {
		if r.Name == "toolset" {
			rr := r
			row = &rr
		}
	}
	if row == nil || row.Setting != "toolset" || strings.Join(row.Choices, ",") != "coding,no-net,default" {
		t.Fatalf("the /toolset row = %+v", row)
	}
}

// --once and --count=N hold a toolset for their turns and leave the session's
// own pick alone.
func TestToolsetTurnOverride(t *testing.T) {
	rec := &toolsetRecorder{}
	m := session.NewManager(toolsetTestConfig(), noopSender{}, rec.run, slog.Default(), "", nil)
	res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, text := range []string{"/toolset coding", "/toolset no-net --once first", "second"} {
		if _, err := m.HandleSessionPromptWithSender(ctx, acp.SessionPromptParams{SessionID: res.SessionID, Prompt: promptText(text)}, noopSender{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(rec.turns, " "); got != "no-net coding" {
		t.Fatalf("turns ran with %q, want no-net then coding", got)
	}
	if st := m.SessionByID(res.SessionID); st.GetSelectedToolset() != "coding" || len(st.TurnOverrides()) != 0 {
		t.Fatalf("session = %q, overrides %+v", st.GetSelectedToolset(), st.TurnOverrides())
	}
}

// The pick is the session's, kept in its session.json across a restart.
func TestSessionToolsetSurvivesARestart(t *testing.T) {
	root := t.TempDir()
	cfg := toolsetTestConfig()
	store := &session.FileStore{Root: t.TempDir()}
	m := session.NewManager(cfg, noopSender{}, noopRunner, slog.Default(), root, store)
	ctx := context.Background()
	res, err := m.HandleSessionNew(ctx, acp.SessionNewParams{CWD: root})
	if err != nil {
		t.Fatal(err)
	}
	pick := "no-net"
	if _, err := m.ApplySessionSettings(ctx, res.SessionID, session.SettingsChange{Toolset: &pick}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(m.SessionByID(res.SessionID)); err != nil {
		t.Fatal(err)
	}
	again := session.NewManager(cfg, noopSender{}, noopRunner, slog.Default(), root, store)
	if _, err := again.HandleSessionLoad(ctx, acp.SessionLoadParams{SessionID: res.SessionID, CWD: root}); err != nil {
		t.Fatal(err)
	}
	if st := again.SessionByID(res.SessionID); st == nil || st.GetSelectedToolset() != "no-net" {
		t.Fatalf("restored session = %+v", st)
	}
}

// An editor sees the toolset as a config option while there are presets, and
// sets it the way it sets the model.
func TestToolsetConfigOption(t *testing.T) {
	m := session.NewManager(toolsetTestConfig(), noopSender{}, noopRunner, slog.Default(), "", nil)
	ctx := context.Background()
	res, err := m.HandleSessionNew(ctx, acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	out, err := m.HandleSessionSetConfigOption(ctx, acp.SessionSetConfigOptionParams{SessionID: res.SessionID, ConfigID: "toolset", Value: "coding"})
	if err != nil {
		t.Fatal(err)
	}
	var opt *acp.ConfigOption
	for i := range out.ConfigOptions {
		if out.ConfigOptions[i].ID == "toolset" {
			opt = &out.ConfigOptions[i]
		}
	}
	if opt == nil || opt.CurrentValue != "coding" || len(opt.Options) != 3 || opt.Options[2].Value != "default" {
		t.Fatalf("toolset option = %+v", opt)
	}
	if _, ok := session.ToolsetOption(settingsTestConfig(), m.SessionByID(res.SessionID)); ok {
		t.Fatal("a configuration without presets offers a toolset option")
	}
}

// A reload that adds or removes a preset publishes every live session's
// settings again, so a surface that shows one offers the toolset at once; a
// reload that leaves the presets alone publishes nothing.
func TestConfigReloadRepublishesTheToolsetChoice(t *testing.T) {
	sender := &captureSender{}
	m := session.NewManager(settingsTestConfig(), sender, noopRunner, slog.Default(), "", nil)
	res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	var seen []acp.SessionSettings
	remove := m.AddSessionSettingsObserver(func(u acp.SessionSettingsUpdate) { seen = append(seen, u.Settings) })
	defer remove()

	m.ReplaceConfig(settingsTestConfig())
	if len(seen) != 0 {
		t.Fatalf("a reload without a toolset change published %+v", seen)
	}
	sender.mu.Lock()
	sender.ups = nil
	sender.mu.Unlock()
	m.ReplaceConfig(toolsetTestConfig())
	if len(seen) != 1 || seen[0].SessionID != res.SessionID || strings.Join(seen[0].ToolsetChoices, ",") != "coding,no-net,default" {
		t.Fatalf("after adding presets = %+v", seen)
	}
	// An editor's selectors follow too: the config options carry the toolset.
	offered := false
	sender.mu.Lock()
	for _, u := range sender.ups {
		if up, ok := u.(acp.ConfigOptionUpdate); ok {
			for _, o := range up.ConfigOptions {
				if o.ID == "toolset" && len(o.Options) == 3 {
					offered = true
				}
			}
		}
	}
	sender.mu.Unlock()
	if !offered {
		t.Fatal("the editor was not sent a toolset config option")
	}
	m.ReplaceConfig(settingsTestConfig())
	if len(seen) != 2 || len(seen[1].ToolsetChoices) != 0 {
		t.Fatalf("after removing them = %+v", seen)
	}
}
