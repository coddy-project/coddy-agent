package session

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// SettingsChange is one change of a session's settings, whichever surface
// asked for it. A nil field is left alone.
type SettingsChange struct {
	Model *string
	// Reasoning is a level the model offers, "off" where its provider can
	// turn thinking off, or "default" (or "") for the model's default level.
	Reasoning      *string
	Mode           *string
	PermissionMode *string
	// Turns > 0 changes the named settings for that many operator turns
	// instead of for the session (--once is 1, --count=N is N).
	Turns int
	// Source names who asked, for the log line and the notice: console, web,
	// acp, telegram, remote, command, permission_dialog, model, skill:<name>.
	Source string
	// Quiet leaves no notice in the transcript's log: a browser re-sending
	// its selection with a message is not a change anybody asked to see.
	Quiet bool
}

// Empty reports whether the change names no setting at all.
func (c SettingsChange) Empty() bool {
	return c.Model == nil && c.Reasoning == nil && c.Mode == nil && c.PermissionMode == nil
}

// settingsVersionSeq numbers settings snapshots across the process, the way
// the message queue numbers its changes: a session rebuilt from disk never
// reuses a version a client has already seen.
var settingsVersionSeq atomic.Uint64

// settingsObservers is the manager's registry of settings observers.
type settingsObservers struct {
	mu  sync.Mutex
	seq int
	fns map[int]func(acp.SessionSettingsUpdate)
}

// AddSessionSettingsObserver registers fn for every change of any session's
// settings and returns the function that removes it again. The HTTP server
// puts the change on GET /coddy/events, so a browser that is not reading the
// turn's stream - a second tab, an idle one - still mirrors it.
//
// fn runs on the goroutine that made the change and MUST NOT block.
func (m *Manager) AddSessionSettingsObserver(fn func(acp.SessionSettingsUpdate)) (remove func()) {
	if fn == nil {
		return func() {}
	}
	o := &m.settingsObs
	o.mu.Lock()
	if o.fns == nil {
		o.fns = make(map[int]func(acp.SessionSettingsUpdate))
	}
	o.seq++
	id := o.seq
	o.fns[id] = fn
	o.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			o.mu.Lock()
			delete(o.fns, id)
			o.mu.Unlock()
		})
	}
}

// SessionSettings returns the settings snapshot of a live session.
func (m *Manager) SessionSettings(sessionID string) (acp.SessionSettings, error) {
	st := m.getSession(strings.TrimSpace(sessionID))
	if st == nil {
		return acp.SessionSettings{}, fmt.Errorf("session not found: %s", sessionID)
	}
	return m.settingsSnapshot(sessionID, st), nil
}

// settingsSnapshot reads the session's settings in one pass. The version is
// taken last, so a snapshot never carries a version newer than its values.
func (m *Manager) settingsSnapshot(sessionID string, st *State) acp.SessionSettings {
	cfg := m.activeCfg()
	out := acp.SessionSettings{
		SessionID:                sessionID,
		Model:                    st.SessionModelID(cfg),
		Mode:                     st.GetMode(),
		PermissionMode:           st.GetPermissionMode(),
		ConfiguredPermissionMode: config.PermModeAsk,
		Overrides:                st.TurnOverrides(),
	}
	if out.Mode == "" {
		out.Mode = string(ModeAgent)
	}
	if cfg != nil {
		out.ConfiguredPermissionMode = cfg.Tools.ResolvedPermMode()
		out.Reasoning = st.SessionReasoning(cfg)
		out.ReasoningChoices = cfg.ReasoningChoicesFor(cfg.FindModelEntry(out.Model))
	}
	if out.PermissionMode == "" {
		out.PermissionMode = out.ConfiguredPermissionMode
	}
	out.Version = settingsVersionSeq.Add(1)
	return out
}

// PublishSessionSettings tells everyone watching the session what its
// settings are now: the running turn's sender (or the manager's own between
// turns), and every observer. notice says what changed; empty for a resend.
func (m *Manager) PublishSessionSettings(sessionID string, st *State, notice, source string) acp.SessionSettings {
	snap := m.settingsSnapshot(sessionID, st)
	update := acp.SessionSettingsUpdate{
		SessionUpdate: acp.UpdateTypeSessionSettings,
		Settings:      snap,
		Notice:        notice,
		Source:        source,
	}
	st.publishedSettings.Store(snap.Version)
	sender := st.TurnSender()
	if sender == nil {
		sender = m.server
	}
	if sender != nil {
		_ = sender.SendSessionUpdate(sessionID, update)
	}
	o := &m.settingsObs
	o.mu.Lock()
	fns := make([]func(acp.SessionSettingsUpdate), 0, len(o.fns))
	for _, fn := range o.fns {
		fns = append(fns, fn)
	}
	o.mu.Unlock()
	for _, fn := range fns {
		fn(update)
	}
	return snap
}

// ApplySessionSettings is the one way a session's settings change: the ACP
// config options, PATCH /coddy/sessions/{id}, the metadata of a prompt, a
// settings command, the permission dialog, the model's own switch. It
// validates the whole change before writing any of it, writes it, tells the
// ACP client (config_option_update, current_mode_update), logs it, leaves a
// notice in the transcript's log and publishes the new snapshot.
func (m *Manager) ApplySessionSettings(ctx context.Context, sessionID string, ch SettingsChange) (acp.SessionSettings, error) {
	snap, _, err := m.applySessionSettings(ctx, sessionID, ch)
	return snap, err
}

// applySessionSettings is ApplySessionSettings returning the notice too.
func (m *Manager) applySessionSettings(_ context.Context, sessionID string, ch SettingsChange) (acp.SessionSettings, string, error) {
	st := m.getSession(strings.TrimSpace(sessionID))
	if st == nil {
		return acp.SessionSettings{}, "", fmt.Errorf("session not found: %s", sessionID)
	}
	// A child's settings were fixed at spawn time and a job session runs
	// nothing of its own.
	if err := readOnlyRefusal(st, sessionID); err != nil {
		return acp.SessionSettings{}, "", err
	}
	if ch.Empty() {
		return m.settingsSnapshot(sessionID, st), "", nil
	}
	values, err := m.validateSettingsChange(st, ch)
	if err != nil {
		return acp.SessionSettings{}, "", err
	}
	notice := m.writeSettings(sessionID, st, ch, values)
	source := strings.TrimSpace(ch.Source)
	if source == "" {
		source = "unknown"
	}
	if ch.Quiet {
		m.log.Debug("session settings changed", "session", sessionID, "source", source, "change", notice)
	} else {
		m.log.Info("session settings changed", "session", sessionID, "source", source, "change", notice)
		st.AppendUILogNotice(CountUserTurns(st.GetMessages()), notice)
	}
	return m.PublishSessionSettings(sessionID, st, notice, source), notice, nil
}

// validateSettingsChange checks every named value against the configuration
// and returns them normalised, keyed by setting. The reasoning level is
// checked against the model the change leaves the session (or the turns) on.
func (m *Manager) validateSettingsChange(st *State, ch SettingsChange) (map[string]string, error) {
	cfg := m.activeCfg()
	out := make(map[string]string, 4)
	if ch.Turns < 0 || ch.Turns > MaxOverrideTurns {
		return nil, fmt.Errorf("--count must be between 1 and %d", MaxOverrideTurns)
	}
	model := ""
	if ch.Model != nil {
		v := strings.TrimSpace(*ch.Model)
		if cfg == nil || len(cfg.Models) == 0 {
			return nil, fmt.Errorf("no models configured")
		}
		if cfg.FindModelEntry(v) == nil || v == "" {
			return nil, fmt.Errorf("unknown model %q (configured: %s)", v, strings.Join(configuredModelIDs(cfg), ", "))
		}
		out[SettingModel] = v
		model = v
	}
	if ch.Reasoning != nil {
		v := strings.ToLower(strings.TrimSpace(*ch.Reasoning))
		if v == "" {
			v = config.ReasoningDefault
		}
		if model == "" {
			if ch.Turns > 0 {
				model = st.EffectiveModelID(cfg)
			} else {
				model = st.SessionModelID(cfg)
			}
		}
		var choices []string
		var ent *config.ModelEntry
		if cfg != nil {
			ent = cfg.FindModelEntry(model)
			choices = cfg.ReasoningChoicesFor(ent)
		}
		if len(choices) == 0 {
			return nil, fmt.Errorf("model %q offers no reasoning levels", model)
		}
		if v == reasoningOn {
			v = thinkingOnLevel(cfg, ent, choices)
		}
		if v == config.ReasoningOff && !containsLevel(choices, v) {
			return nil, fmt.Errorf("thinking cannot be turned off for model %q: its provider has no switch for it (levels: %s)", model, strings.Join(choices, ", "))
		}
		if v != config.ReasoningDefault && !containsLevel(choices, v) {
			return nil, fmt.Errorf("reasoning %q is not offered by model %q (offered: %s, default)", v, model, strings.Join(choices, ", "))
		}
		out[SettingReasoning] = v
	}
	if ch.Mode != nil {
		v := strings.ToLower(strings.TrimSpace(*ch.Mode))
		if !IsValidMode(v) {
			return nil, fmt.Errorf("unknown mode %q (agent, plan, ask)", v)
		}
		out[SettingMode] = v
	}
	if ch.PermissionMode != nil {
		v := strings.ToLower(strings.TrimSpace(*ch.PermissionMode))
		switch v {
		case config.PermModeAsk, config.PermModeAcceptEdits, config.PermModeBypass:
		default:
			return nil, fmt.Errorf("unknown permission mode %q (ask, accept_edits, bypass)", v)
		}
		out[SettingPermissionMode] = v
	}
	return out, nil
}

// writeSettings applies validated values and returns the notice describing
// the change.
func (m *Manager) writeSettings(sessionID string, st *State, ch SettingsChange, values map[string]string) string {
	cfg := m.activeCfg()
	names := sortedSettings(values)
	if ch.Turns > 0 {
		for _, name := range names {
			st.ArmTurnOverride(name, values[name], ch.Turns)
		}
		return settingsNotice(values, names, turnsScope(ch.Turns))
	}
	modeChanged := false
	for _, name := range names {
		v := values[name]
		switch name {
		case SettingModel:
			st.SetSelectedModelID(v)
			// A level the new model does not offer falls back to its default,
			// in the snapshot for everyone to see, instead of lingering unused.
			if _, named := values[SettingReasoning]; !named && cfg != nil {
				sel := strings.TrimSpace(st.GetSelectedReasoning())
				if sel != "" && !containsLevel(cfg.ReasoningChoicesFor(cfg.FindModelEntry(v)), sel) {
					st.SetSelectedReasoning("")
				}
			}
		case SettingReasoning:
			if v == config.ReasoningDefault {
				v = ""
			}
			st.SetSelectedReasoning(v)
		case SettingMode:
			modeChanged = st.GetMode() != v
			st.SetMode(v)
		case SettingPermissionMode:
			st.SetPermissionMode(v)
		}
		st.ClearTurnOverride(name)
	}
	if modeChanged && m.server != nil {
		if err := m.server.SendSessionUpdate(sessionID, acp.ModeUpdate{
			SessionUpdate: acp.UpdateTypeCurrentModeUpdate,
			CurrentModeID: values[SettingMode],
		}); err != nil {
			m.log.Warn("failed to send mode update", "error", err)
		}
	}
	m.sendConfigOptionUpdate(sessionID, st)
	return settingsNotice(values, names, "for this session")
}

// turnsScope names a number of turns the way a notice says it.
func turnsScope(turns int) string {
	if turns == 1 {
		return "for the next turn"
	}
	return fmt.Sprintf("for the next %d turns", turns)
}

// settingsNotice renders a change as one line: "Model: x for this session;
// Reasoning: off for this session".
func settingsNotice(values map[string]string, names []string, scope string) string {
	parts := make([]string, 0, len(names))
	for _, name := range names {
		v := values[name]
		label := name
		switch name {
		case SettingModel:
			label = "Model"
		case SettingReasoning:
			label = "Reasoning"
		case SettingMode:
			label = "Mode"
		case SettingPermissionMode:
			label = "Permission mode"
		}
		parts = append(parts, fmt.Sprintf("%s: %s %s", label, v, scope))
	}
	return strings.Join(parts, "; ")
}

// thinkingOnLevel is the level /think without one means: the model's default
// when it has one, else medium, else its lowest level that still thinks.
func thinkingOnLevel(cfg *config.Config, ent *config.ModelEntry, choices []string) string {
	if def := cfg.DefaultReasoningLevelFor(ent); def != "" && def != config.ReasoningOff && def != config.ReasoningNone {
		return def
	}
	if containsLevel(choices, config.ReasoningMedium) {
		return config.ReasoningMedium
	}
	for _, lv := range choices {
		if lv != config.ReasoningOff && lv != config.ReasoningNone {
			return lv
		}
	}
	return choices[0]
}

// configuredModelIDs lists the models[].model ids of the configuration.
func configuredModelIDs(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	out := make([]string, 0, len(cfg.Models))
	for i := range cfg.Models {
		out = append(out, cfg.Models[i].Model)
	}
	return out
}

// SessionModelID is the session's own model, ignoring what the running turn
// holds: what a model selector shows.
func (s *State) SessionModelID(cfg *config.Config) string {
	s.mu.RLock()
	sel := s.SelectedModelID
	s.mu.RUnlock()
	return ResolveModelID(cfg, sel)
}

// SessionReasoning is the session's own reasoning level for its own model,
// ignoring what the running turn holds; "" when the model offers none.
func (s *State) SessionReasoning(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	ent := cfg.FindModelEntry(s.SessionModelID(cfg))
	if ent == nil {
		return ""
	}
	choices := cfg.ReasoningChoicesFor(ent)
	if len(choices) == 0 {
		return ""
	}
	s.mu.RLock()
	sel := strings.TrimSpace(s.SelectedReasoning)
	s.mu.RUnlock()
	if containsLevel(choices, sel) {
		return sel
	}
	return cfg.DefaultReasoningLevelFor(ent)
}

// TakenSettings is what TakeSettingsCommands did with the start of a prompt.
type TakenSettings struct {
	// Prompt is the prompt without its leading settings commands.
	Prompt []acp.ContentBlock
	// TurnChanges are validated turn-scoped changes for the turn the rest of
	// the prompt starts; the caller applies them once that turn is admitted,
	// so no other turn can take them first.
	TurnChanges []SettingsChange
	// Notice says what was applied right away.
	Notice string
	// Handled says nothing is left to run: the prompt was only commands.
	Handled bool
	// Names are the commands taken, in order.
	Names []string
}

// TakeSettingsCommands takes the settings commands off the start of a prompt
// the operator typed (commands.go) and applies what applies at once: a
// session-wide change immediately, whether or not a turn is running, and a
// turn-scoped one immediately only when nothing is left to run - then it is
// armed for the next operator turns. A turn-scoped change followed by text
// comes back in TurnChanges for the turn that text starts.
//
// Only the first text block is read: it is what the operator typed. The
// attachments a mention resolved and the bodies of skills come later, so a
// file or a page that starts with /model switches nothing.
func (m *Manager) TakeSettingsCommands(ctx context.Context, sessionID string, prompt []acp.ContentBlock, source string) (TakenSettings, error) {
	out := TakenSettings{Prompt: prompt}
	idx := -1
	for i, b := range prompt {
		if b.Type == acp.ContentTypeText {
			idx = i
			break
		}
	}
	if idx < 0 {
		return out, nil
	}
	line, err := ParseSettingsCommands(prompt[idx].Text)
	if err != nil {
		return out, err
	}
	if line.Empty() {
		return out, nil
	}
	st := m.getSession(strings.TrimSpace(sessionID))
	if st == nil {
		return out, fmt.Errorf("session not found: %s", sessionID)
	}
	if err := readOnlyRefusal(st, sessionID); err != nil {
		return out, err
	}
	out.Names = line.Names
	// Validate every part before applying any, so a typo in the second
	// command does not leave the first one half done.
	for _, ch := range line.Turns {
		if _, err := m.validateSettingsChange(st, ch); err != nil {
			return out, err
		}
	}
	var notices []string
	if !line.Session.Empty() {
		ch := line.Session
		ch.Source = source
		_, notice, err := m.applySessionSettings(ctx, sessionID, ch)
		if err != nil {
			return out, err
		}
		notices = append(notices, notice)
	}
	rest := strings.TrimSpace(line.Rest)
	others := false
	for i, b := range prompt {
		if i != idx && (b.Type != acp.ContentTypeText || strings.TrimSpace(b.Text) != "") {
			others = true
			break
		}
	}
	if rest == "" && !others {
		for _, ch := range line.Turns {
			ch.Source = source
			_, notice, err := m.applySessionSettings(ctx, sessionID, ch)
			if err != nil {
				return out, err
			}
			notices = append(notices, notice)
		}
		out.Handled = true
		out.Prompt = nil
		out.Notice = strings.Join(notices, "; ")
		return out, nil
	}
	for _, ch := range line.Turns {
		ch.Source = source
		out.TurnChanges = append(out.TurnChanges, ch)
	}
	rewritten := make([]acp.ContentBlock, 0, len(prompt))
	for i, b := range prompt {
		if i == idx {
			if rest == "" {
				continue
			}
			b.Text = line.Rest
		}
		rewritten = append(rewritten, b)
	}
	out.Prompt = rewritten
	out.Notice = strings.Join(notices, "; ")
	return out, nil
}

// beginOperatorTurnSettings consumes the armed overrides for the turn that
// was just admitted and, when the turn holds any, publishes the snapshot so
// every surface shows them as active. It reports whether it published.
func (m *Manager) beginOperatorTurnSettings(sessionID string, st *State) bool {
	st.BeginOperatorTurnSettings()
	if len(st.TurnOverrides()) == 0 {
		return false
	}
	m.PublishSessionSettings(sessionID, st, "", "turn")
	return true
}

// endTurnSettings ends the turn's settings and publishes what is left.
func (m *Manager) endTurnSettings(sessionID string, st *State) {
	st.EndTurnSettings()
	m.PublishSessionSettings(sessionID, st, "", "turn")
}

// AnnounceSettingsNotice answers a prompt that was only settings commands:
// the notice goes to the surface as the agent's reply, which every client
// renders (an editor has nothing else to show it with). It is not a message
// of the conversation: nothing is added to the history.
func AnnounceSettingsNotice(sender acp.UpdateSender, sessionID, notice string) {
	if sender == nil || strings.TrimSpace(notice) == "" {
		return
	}
	_ = sender.SendSessionUpdate(sessionID, acp.MessageChunkUpdate{
		SessionUpdate: acp.UpdateTypeAgentMessageChunk,
		Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: notice},
	})
}

// ApplyTurnSettings changes settings for the rest of the running turn only:
// a switch_model call the user limited to the turn, the frontmatter of a
// skill. It validates
// like ApplySessionSettings, against the model the turn runs on, takes effect
// from the turn's next model request, and is gone when the turn ends.
func (m *Manager) ApplyTurnSettings(_ context.Context, sessionID string, ch SettingsChange) (acp.SessionSettings, error) {
	st := m.getSession(strings.TrimSpace(sessionID))
	if st == nil {
		return acp.SessionSettings{}, fmt.Errorf("session not found: %s", sessionID)
	}
	if ch.Empty() {
		return m.settingsSnapshot(sessionID, st), nil
	}
	check := ch
	check.Turns = 1
	values, err := m.validateSettingsChange(st, check)
	if err != nil {
		return acp.SessionSettings{}, err
	}
	cfg := m.activeCfg()
	names := sortedSettings(values)
	for _, name := range names {
		st.SetTurnSetting(name, values[name])
	}
	// A level the new model does not offer is dropped, so the turn falls
	// back on the session's level or the model's default.
	if model, ok := values[SettingModel]; ok && cfg != nil {
		if _, named := values[SettingReasoning]; !named {
			if lv := st.TurnSetting(SettingReasoning); lv != "" && lv != config.ReasoningDefault &&
				!containsLevel(cfg.ReasoningChoicesFor(cfg.FindModelEntry(model)), lv) {
				st.SetTurnSetting(SettingReasoning, "")
			}
		}
	}
	notice := settingsNotice(values, names, "for the rest of this turn")
	source := strings.TrimSpace(ch.Source)
	if source == "" {
		source = "unknown"
	}
	m.log.Info("turn settings changed", "session", sessionID, "source", source, "change", notice)
	st.AppendUILogNotice(CountUserTurns(st.GetMessages()), notice)
	return m.PublishSessionSettings(sessionID, st, notice, source), nil
}
