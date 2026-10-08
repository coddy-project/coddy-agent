package dryrun

import (
	"fmt"
	"slices"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// finding is one disagreement between a key a coddy row writes and what the
// remote lists for its alias: the key as written in the file, what is wrong
// and how to fix it.
type finding struct {
	key     string
	message string
	fix     string
}

// coddyKeyFindings compares the capability keys a models[] row of a coddy
// provider writes with the record the remote's listing carries for its alias,
// and reports one finding per key that differs. A key left out inherits the
// listing and an equal one agrees with it, so neither is reported.
//
// A written key wins over the listing, and a refresh of the listing never
// writes one, so a key that disagrees keeps disagreeing until it is removed:
// a level or an "off" the remote does not offer is refused by it with
// invalid_option on every request that uses it, and the other keys describe a
// model the remote does not serve. Every finding therefore ends in the same
// fix, removing the key.
func coddyKeyFindings(m config.ModelEntry, row llm.ModelEntry) []finding {
	var out []finding
	add := func(key, message string) {
		out = append(out, finding{
			key:     key,
			message: message,
			fix:     "remove " + key + " from this model to inherit the remote's listing",
		})
	}

	// A remote that lists no window gives nothing to compare with.
	if m.MaxContextTokens > 0 && row.ContextWindow > 0 && m.MaxContextTokens != row.ContextWindow {
		add("max_context_tokens", fmt.Sprintf(
			"max_context_tokens is %d here and the remote lists %d: the local key wins, so the context ring and the automatic compaction measure against %d, not against the window of the remote's model",
			m.MaxContextTokens, row.ContextWindow, m.MaxContextTokens))
	}

	if m.Multimodal != nil && *m.Multimodal != row.Multimodal {
		consequence := "images are sent to a model the remote does not list as reading them"
		if !*m.Multimodal {
			consequence = "this row never sends images to a model that reads them"
		}
		add("multimodal", fmt.Sprintf("multimodal is %t here and the remote lists %t: the local key wins, so %s",
			*m.Multimodal, row.Multimodal, consequence))
	}

	if m.AllowReasoningOff != nil && *m.AllowReasoningOff != row.AllowReasoningOff {
		consequence := `"off" is never offered although the remote allows it`
		if *m.AllowReasoningOff {
			consequence = `"off" is offered and the remote refuses a request at that level with invalid_option`
		}
		add("allow_reasoning_off", fmt.Sprintf("allow_reasoning_off is %t here and the remote lists %t: the local key wins, so %s",
			*m.AllowReasoningOff, row.AllowReasoningOff, consequence))
	}

	if msg, ok := reasoningDefaultFinding(m, row); ok {
		add("reasoning_default", msg)
	}

	if m.ReasoningLevels != nil {
		if msg, ok := reasoningLevelsFinding(*m.ReasoningLevels, row); ok {
			add("reasoning_levels", msg)
		}
	}
	return out
}

// reasoningDefaultFinding reports a written reasoning_default that is not the
// remote's default. What it costs depends on the level: one the remote offers
// only moves where new chats start, one it does not offer is refused, and one
// that is not among the row's own levels is ignored by the loader.
func reasoningDefaultFinding(m config.ModelEntry, row llm.ModelEntry) (string, bool) {
	local := strings.TrimSpace(m.ReasoningDefault)
	remote := strings.TrimSpace(row.ReasoningDefault)
	if local == "" || local == remote {
		return "", false
	}
	remoteText := "no default"
	if remote != "" {
		remoteText = fmt.Sprintf("%q", remote)
	}
	// ModelEntry.DefaultReasoningLevel ignores a default that is not one of the
	// levels the row resolves to: the ones it writes, else the remote's.
	rowLevels := row.ReasoningLevels
	if m.ReasoningLevels != nil {
		rowLevels = *m.ReasoningLevels
	}
	var consequence string
	switch {
	case !slices.Contains(rowLevels, local):
		consequence = "it is not one of this row's reasoning levels, so it has no effect"
	case !remoteOffers(row, local):
		consequence = fmt.Sprintf("the local key wins, and the remote does not offer %q, so a request at that level is refused with invalid_option", local)
	default:
		consequence = fmt.Sprintf("the local key wins, so new chats start at %q instead of the remote's own default", local)
	}
	return fmt.Sprintf("reasoning_default is %q here and the remote lists %s: %s", local, remoteText, consequence), true
}

// remoteOffers reports whether the remote takes a request at this level: one of
// its listed levels, or "off" where it allows it and has levels to switch.
func remoteOffers(row llm.ModelEntry, level string) bool {
	levels := levelsOf(row.ReasoningLevels)
	if level == config.ReasoningOff {
		return row.AllowReasoningOff && len(levels) > 0
	}
	return slices.Contains(levels, level)
}

// reasoningLevelsFinding reports a written reasoning_levels that is not the
// remote's list, as the difference both ways: the levels the remote does not
// offer are refused by it, the ones it offers and the row omits are unused.
func reasoningLevelsFinding(written []string, row llm.ModelEntry) (string, bool) {
	local := levelsOf(written)
	remote := levelsOf(row.ReasoningLevels)
	var refused, unused []string
	for _, lv := range local {
		if !slices.Contains(remote, lv) {
			refused = append(refused, lv)
		}
	}
	for _, lv := range remote {
		if !slices.Contains(local, lv) {
			unused = append(unused, lv)
		}
	}
	if len(refused) == 0 && len(unused) == 0 {
		return "", false
	}
	var parts []string
	if len(refused) > 0 {
		parts = append(parts, "levels the remote does not offer are refused by it with invalid_option: "+strings.Join(refused, ", "))
	}
	if len(unused) > 0 {
		parts = append(parts, "levels the remote offers stay unused here: "+strings.Join(unused, ", "))
	}
	remoteText := listText(remote)
	if len(remote) == 0 {
		remoteText += " (no levels)"
	}
	return fmt.Sprintf("reasoning_levels is %s here and the remote lists %s: the local key wins; %s",
		listText(trimmed(written)), remoteText, strings.Join(parts, "; ")), true
}

// levelsOf is a list of reasoning levels as the rest of Coddy reads it: names
// trimmed, "off" left out (it is a switch, not a level, and
// Config.ReasoningChoicesFor drops it from a list), each name once, in order.
func levelsOf(levels []string) []string {
	var out []string
	for _, lv := range trimmed(levels) {
		if lv == config.ReasoningOff || slices.Contains(out, lv) {
			continue
		}
		out = append(out, lv)
	}
	return out
}

// trimmed is a list of names, each trimmed, the blank ones removed.
func trimmed(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// listText writes a list the way the file does: [low, high], [] when empty.
func listText(names []string) string {
	return "[" + strings.Join(names, ", ") + "]"
}
