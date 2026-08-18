package prompts

import "strings"

// Identity is the self-identification sentence every Coddy-authored system
// prompt opens with.
//
// Why it exists: an LLM gateway cannot tell one OpenAI-compatible client from
// another by the wire protocol, so gateways classify traffic by matching the
// opening of the system prompt against a table of known products
// ("You are Claude Code…", "You are Cline…"). Coddy used to open with a
// generic sentence and was therefore invisible in that kind of analytics.
//
// Treat this string as a published contract: gateways key on it, and changing
// the wording silently drops Coddy out of their reports until they catch up.
const Identity = "You are Coddy, an AI coding agent."

// IdentityMarker is the lowercase substring a gateway keys on. It is kept
// separate from Identity so the surrounding sentence can be reworded without
// breaking attribution.
const IdentityMarker = "you are coddy"

// identityWindow mirrors how much of a system prompt a gateway inspects
// (NeuralDeep reads the first 220 characters, lowercases them, then matches
// substrings). A marker outside that prefix is as good as absent, so the
// dedup check below uses the same window rather than scanning the whole text.
const identityWindow = 220

// WithIdentity guarantees a system prompt names Coddy inside the window a
// gateway looks at.
//
// A prompt that already identifies itself there — the built-in templates do —
// is returned untouched, so the common path reads as one natural sentence
// instead of a stacked identity line plus a generic opener. Everything else
// (a user's own prompts.dir template, the render fallback, the auxiliary
// summarizer/title/memory prompts) gets the line prepended.
func WithIdentity(prompt string) string {
	trimmed := strings.TrimSpace(prompt)
	if trimmed == "" {
		return Identity
	}
	if strings.Contains(strings.ToLower(headOf(trimmed, identityWindow)), IdentityMarker) {
		return trimmed
	}
	return Identity + "\n" + trimmed
}

// headOf returns at most n runes from the start of s. Runes rather than bytes
// so a multi-byte prompt is not cut mid-character.
func headOf(s string, n int) string {
	if len(s) <= n { // fast path: byte length bounds rune count
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
