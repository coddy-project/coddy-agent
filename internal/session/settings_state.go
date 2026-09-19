package session

import (
	"sort"
	"sync/atomic"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// The settings a session keeps and a command changes.
const (
	SettingModel          = "model"
	SettingReasoning      = "reasoning"
	SettingMode           = "mode"
	SettingPermissionMode = "permission_mode"
)

// settingOrder is the order settings are listed in, whatever order a change
// named them in.
var settingOrder = []string{SettingModel, SettingReasoning, SettingMode, SettingPermissionMode}

// MaxOverrideTurns bounds --count: an override is a detour, and one that
// outlives a working day is a session setting the operator forgot about.
const MaxOverrideTurns = 50

// TurnOverride is a setting changed for a number of operator turns rather than
// for the session: armed by --once or --count=N, consumed one turn at a time.
type TurnOverride = acp.TurnOverride

// turnValues are the settings a turn holds for its whole length; an empty
// field follows the session.
type turnValues struct {
	model, reasoning, mode, permissionMode string
}

func (v *turnValues) get(setting string) string {
	switch setting {
	case SettingModel:
		return v.model
	case SettingReasoning:
		return v.reasoning
	case SettingMode:
		return v.mode
	case SettingPermissionMode:
		return v.permissionMode
	}
	return ""
}

func (v *turnValues) set(setting, value string) {
	switch setting {
	case SettingModel:
		v.model = value
	case SettingReasoning:
		v.reasoning = value
	case SettingMode:
		v.mode = value
	case SettingPermissionMode:
		v.permissionMode = value
	}
}

// armedOverride is one setting waiting for the operator turns it was armed for.
type armedOverride struct {
	value     string
	turnsLeft int
}

// turnSettings is the part of a session's settings that belongs to turns
// rather than to the session: what was armed for the next turns, what the
// running turn holds, and what the last operator turn held. Process memory
// only - a restart forgets a detour, the way it forgets the turn itself.
type turnSettings struct {
	armed  map[string]*armedOverride
	active turnValues
	// last is what the most recent operator turn held: a permission resume
	// continues that turn and takes the same values back.
	last turnValues
}

// settingsRevision counts changes of anything a model request reads - the
// session's model and reasoning, the running turn's values - so the loop can
// tell between two requests that it has to build a new transport.
var settingsRevisionSeq atomic.Uint64

func (s *State) bumpSettingsRevision() {
	s.settingsRev.Store(settingsRevisionSeq.Add(1))
}

// PublishedSettingsVersion is the version of the last settings snapshot
// published for the session: a client holding an older one has missed a
// change (applyProfileSettings in the HTTP server ignores what it sends).
func (s *State) PublishedSettingsVersion() uint64 {
	return s.publishedSettings.Load()
}

// SettingsRevision is the number of the last change of a setting a model
// request reads. Two equal readings mean nothing moved in between.
func (s *State) SettingsRevision() uint64 {
	return s.settingsRev.Load()
}

// ArmTurnOverride arms value for the next turns operator turns, replacing
// what was armed for the setting before.
func (s *State) ArmTurnOverride(setting, value string, turns int) {
	if turns <= 0 {
		return
	}
	if turns > MaxOverrideTurns {
		turns = MaxOverrideTurns
	}
	s.settingsMu.Lock()
	if s.turn.armed == nil {
		s.turn.armed = make(map[string]*armedOverride)
	}
	s.turn.armed[setting] = &armedOverride{value: value, turnsLeft: turns}
	s.settingsMu.Unlock()
}

// ClearTurnOverride drops what was armed for setting and, for the running
// turn, the value it held: a session-wide change is the operator's last word
// on that setting, the turn in progress included.
func (s *State) ClearTurnOverride(setting string) {
	s.settingsMu.Lock()
	delete(s.turn.armed, setting)
	s.turn.active.set(setting, "")
	s.turn.last.set(setting, "")
	s.settingsMu.Unlock()
	s.bumpSettingsRevision()
}

// BeginOperatorTurnSettings starts the settings of a turn an operator
// started: every armed override gives up one of its turns and the values
// become the turn's for its whole length.
func (s *State) BeginOperatorTurnSettings() {
	s.settingsMu.Lock()
	var held turnValues
	for setting, o := range s.turn.armed {
		if o.turnsLeft <= 0 {
			delete(s.turn.armed, setting)
			continue
		}
		held.set(setting, o.value)
		o.turnsLeft--
		if o.turnsLeft == 0 {
			delete(s.turn.armed, setting)
		}
	}
	s.turn.active = held
	s.turn.last = held
	s.settingsMu.Unlock()
	s.bumpSettingsRevision()
}

// ResumeTurnSettings gives a permission resume the values of the turn it
// continues. Nothing armed is consumed: it is the same turn.
func (s *State) ResumeTurnSettings() {
	s.settingsMu.Lock()
	s.turn.active = s.turn.last
	s.settingsMu.Unlock()
	s.bumpSettingsRevision()
}

// EndTurnSettings drops the values of the turn that ended. The last operator
// turn's values stay for a resume.
func (s *State) EndTurnSettings() {
	s.settingsMu.Lock()
	s.turn.active = turnValues{}
	s.settingsMu.Unlock()
	s.bumpSettingsRevision()
}

// SetTurnSetting changes one value for the rest of the running turn: the
// model's own switch_model call, the frontmatter of a skill it loaded.
func (s *State) SetTurnSetting(setting, value string) {
	s.settingsMu.Lock()
	s.turn.active.set(setting, value)
	s.turn.last.set(setting, value)
	s.settingsMu.Unlock()
	s.bumpSettingsRevision()
}

// TurnSetting is the value the running turn holds for setting, or "" when it
// follows the session.
func (s *State) TurnSetting(setting string) string {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	return s.turn.active.get(setting)
}

// TurnOverrides lists what the running turn holds and what is armed for the
// next ones, in setting order. A setting the running turn holds and that is
// still armed appears once, active, with the turns it has left.
func (s *State) TurnOverrides() []TurnOverride {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	var out []TurnOverride
	for _, setting := range settingOrder {
		active := s.turn.active.get(setting)
		armed := s.turn.armed[setting]
		switch {
		case armed != nil && active != "" && armed.value == active:
			out = append(out, TurnOverride{Setting: setting, Value: active, TurnsLeft: armed.turnsLeft, Active: true})
		default:
			if active != "" {
				out = append(out, TurnOverride{Setting: setting, Value: active, Active: true})
			}
			if armed != nil {
				out = append(out, TurnOverride{Setting: setting, Value: armed.value, TurnsLeft: armed.turnsLeft})
			}
		}
	}
	return out
}

// EffectiveMode is the operating mode the running turn works in: its own
// when it holds one, the session's otherwise.
func (s *State) EffectiveMode() string {
	if m := s.TurnSetting(SettingMode); m != "" {
		return m
	}
	return s.GetMode()
}

// EffectivePermissionMode is the permission mode the running turn works
// under: its own, then the session's override; "" means the configuration's.
func (s *State) EffectivePermissionMode() string {
	if m := s.TurnSetting(SettingPermissionMode); m != "" {
		return m
	}
	return s.GetPermissionMode()
}

// ResolvedPermissionMode is EffectivePermissionMode with the configuration's
// mode filled in.
func (s *State) ResolvedPermissionMode(cfg *config.Config) string {
	if m := s.EffectivePermissionMode(); m != "" {
		return m
	}
	if cfg == nil {
		return config.PermModeAsk
	}
	return cfg.Tools.ResolvedPermMode()
}

// containsLevel reports whether levels holds level.
func containsLevel(levels []string, level string) bool {
	for _, lv := range levels {
		if lv == level {
			return true
		}
	}
	return false
}

// sortedSettings returns the settings named in m in setting order.
func sortedSettings(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	rank := func(s string) int {
		for i, n := range settingOrder {
			if n == s {
				return i
			}
		}
		return len(settingOrder)
	}
	sort.Slice(out, func(i, j int) bool { return rank(out[i]) < rank(out[j]) })
	return out
}
