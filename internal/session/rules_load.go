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

// RulesPrompt is the standing part of a session's system prompt: the {{.Rules}}
// block (the project docs preamble and the always-on rules) and the
// {{.Instructions}} block. A session renders it once per rules generation and
// reuses it on every later turn, so a file behind it that is edited during the
// session - an AGENTS.md the agent itself updates - does not move the system
// message and throw away the provider's cached copy of the conversation behind
// it. The next generation reads the files again: a compaction, a config reload,
// a workspace switch, a restart.
type RulesPrompt struct {
	// Generation is the rules generation the blocks were rendered for.
	Generation uint64
	// RendersRules records whether the template printed {{.Rules}}: only then
	// were the documents the rules block embedded left out of Instructions.
	RendersRules bool
	Rules        string
	Instructions string
}

// CachedRulesPrompt returns the standing prompt rendered for the current rules
// generation - nil when there is none yet, or when it was rendered for the
// other value of rendersRules - and the current generation, which a caller
// stores its own rendering under.
func (s *State) CachedRulesPrompt(rendersRules bool) (*RulesPrompt, uint64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p := s.rulesPrompt
	if p == nil || p.Generation != s.rulesGeneration || p.RendersRules != rendersRules {
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
