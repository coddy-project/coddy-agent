package session

// CheckMentions answers the web composer, which highlights the "@" mentions of
// a draft: only a mention sending would attach is marked, over the reading
// that would win. "npm install @google/genai" names a package, not a file, and
// "compare @src/a.go b.go" names src/a.go, not "src/a.go b.go" - the grammar
// alone cannot tell, the resolver can. It runs dry, so a check reads nothing.

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/mention"
)

// MaxMentionCheckBytes caps the draft a check reads.
const MaxMentionCheckBytes = 256 << 10

// MentionCheck is one draft to check, in the session it would be sent to (or
// in CWD before the session exists).
type MentionCheck struct {
	SessionID string
	CWD       string
	Text      string
}

// CheckedMention is one "@" token of a checked draft, in document order.
type CheckedMention struct {
	// Token is the whole token as the grammar reads it, "@" included: the key
	// a client matches its own parse of the draft against.
	Token string `json:"token"`
	// Typed is the part of Token that resolves - "@src/a.go" of
	// "@src/a.go b.go" - and is empty when the token names nothing.
	Typed string `json:"typed,omitempty"`
	// Kind is what Typed names: file, directory, session, rule, agent, plan,
	// url or doc.
	Kind string `json:"kind,omitempty"`
}

// CheckMentions reports every "@" token of the draft and, for the ones a sent
// prompt would attach, the part that resolves and what it names. Mentions a
// fenced block, an inline code span or a quoted line holds are prose and are
// not reported.
func (m *Manager) CheckMentions(ctx context.Context, req MentionCheck) []CheckedMention {
	text := req.Text
	if len(text) > MaxMentionCheckBytes {
		text = text[:MaxMentionCheckBytes]
	}
	toks := mention.Parse(text)
	if len(toks) == 0 {
		return nil
	}
	cwd := strings.TrimSpace(req.CWD)
	var st *State
	if m != nil && strings.TrimSpace(req.SessionID) != "" {
		st = m.getSession(strings.TrimSpace(req.SessionID))
	}
	if st != nil {
		cwd = st.GetCWD()
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	r := &mentionResolver{ctx: ctx, cwd: cwd, home: mention.HomeDir(), dry: true, seen: map[string]bool{}}
	switch {
	case st != nil:
		r.sessionID = st.GetID()
		r.sessionDir = strings.TrimSpace(st.GetPersistedSessionDir())
		r.rules = st.GetRulesCatalog()
		r.scope = m.mentionScope(st, false, false)
	case m != nil:
		// The composer before its first message: the session it starts is the
		// operator's, in this folder, with no plans yet.
		r.rules = DiscoverRules(m.Cfg(), cwd)
		r.scope = MentionScope{
			Sessions: m.store != nil,
			URLs:     true,
			Agents: func() ([]MentionAgent, string) {
				return m.mentionAgents(cwd, false)
			},
		}
	}
	if m != nil {
		r.store = m.store
		r.live = m.getSession
	}
	out := make([]CheckedMention, 0, len(toks))
	for _, tok := range toks {
		c := CheckedMention{Token: text[tok.Start:tok.End]}
		if res, reading, ok := r.resolveToken(text, tok); ok && res != nil {
			end := tok.End
			if reading >= 0 && reading < len(tok.Readings) {
				end = tok.Readings[reading].End
			}
			c.Typed = text[tok.Start:end]
			c.Kind = mention.KindFile
			if res.Mention != nil && res.Mention.Kind != "" {
				c.Kind = res.Mention.Kind
			}
		}
		out = append(out, c)
	}
	return out
}
