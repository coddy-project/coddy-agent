package session

import (
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/rules"
)

// DiscoverRules loads the rules that apply to a session: the operator's own
// ${CODDY_HOME}/rules (from cfg.Paths.Home) and the rule folders of the
// workspace at cwd.
func DiscoverRules(cfg *config.Config, cwd string) []*rules.Rule {
	if cfg == nil || !cfg.Rules.AutoDiscoverEnabled() {
		return nil
	}
	cat, err := rules.DefaultFactory(cfg.Paths.Home).Discover(cwd, rules.ParseSystems(cfg.Rules.Systems))
	if err != nil {
		return nil
	}
	return cat
}

// RulesPrompt is the standing part of a session's system prompt: the
// AGENTS.md and DESIGN.md layers of the agent home and the session folder
// (Docs), the always-on rules (Rules), the files of instructions.files (User)
// and the key (rules.DocKey) of every file the first and the last carry
// (Keys). A session renders it once per rules generation and reuses it on
// every later turn, whatever template the turn runs on - the agent places the
// parts per template, from the same strings - so a file behind it that is
// edited during the session, an AGENTS.md the agent itself updates, does not
// move the system message and throw away the provider's cached copy of the
// conversation behind it. The next generation reads the files again: a
// compaction, a config reload, a workspace switch, a restart.
type RulesPrompt struct {
	// Generation is the rules generation the parts were rendered for.
	Generation uint64
	// Inputs names what the parts were rendered from besides the files
	// themselves - the agent home, the workspace, the instructions.files
	// list - so a configuration that changed them is not answered from a
	// rendering of the old one.
	Inputs string
	Docs   string
	Rules  string
	User   string
	Keys   map[string]bool
}

// CachedRulesPrompt returns the standing prompt rendered for the current rules
// generation from the same inputs - nil when there is none yet - and the
// current generation, which a caller stores its own rendering under.
func (s *State) CachedRulesPrompt(inputs string) (*RulesPrompt, uint64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p := s.rulesPrompt
	if p == nil || p.Generation != s.rulesGeneration || p.Inputs != inputs {
		return nil, s.rulesGeneration
	}
	return p, s.rulesGeneration
}

// StoreRulesPrompt keeps p for the rest of its generation. A rendering of a
// generation that has already ended is dropped: the catalog it was built from
// has been replaced.
func (s *State) StoreRulesPrompt(p *RulesPrompt) {
	if p == nil {
		return
	}
	s.mu.Lock()
	if p.Generation == s.rulesGeneration {
		s.rulesPrompt = p
	}
	s.mu.Unlock()
}
