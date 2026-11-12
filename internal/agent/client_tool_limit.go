package agent

// A request the provider refused for the tools it offered, and the end of the
// turn that says what to do about it.
//
// Such a refusal used to surface as the provider's own error wrapped in "LLM
// error:" - 400 Bad Request {"message":"at most 128 tools may be offered"} -
// and the turn was lost with nothing to act on: how many tools a session
// offers is its built-ins plus every tool of every MCP server attached to it,
// and none of that is in the message. The turn still ends here rather than
// being sent again with some tools dropped, because which tools a model is
// offered is the operator's choice and a silent one would answer a different
// question than the one that was asked; what it ends with names the counts and
// the ways to send fewer.

import (
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// clientToolLimitError is the error a turn ends with when streamErr is the
// provider refusing the tools the request offered. offered is how many tool
// definitions Coddy sent, which is the figure the provider's own count is
// checked against and the only one available when the refusal gives none. The
// provider's error stays in the chain: surfaces read its status from it, and
// its words are the last thing the message says.
func (a *Agent) clientToolLimitError(streamErr error, offered int) error {
	detail := llm.ClientToolLimitDetailOf(streamErr)
	a.log.Warn("provider refused the tools the request offered",
		"offeredTools", offered, "providerCounted", detail.Offered, "providerLimit", detail.Limit,
		"tool", detail.Tool, "error", streamErr)
	return fmt.Errorf("%s: %w", clientToolLimitMessage(offered, detail, a.toolsetChoices()), streamErr)
}

// toolsetChoices are the names of the toolsets[] presets this configuration
// has, which decide whether /toolset is something the person can run now or
// something they have to write a preset for first.
func (a *Agent) toolsetChoices() []string {
	if a.cfg == nil {
		return nil
	}
	names := make([]string, 0, len(a.cfg.Toolsets))
	for i := range a.cfg.Toolsets {
		if name := strings.TrimSpace(a.cfg.Toolsets[i].Name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// clientToolLimitMessage says which bound the provider refused the request
// for, what each side counted, and what the person can do. A size that is not
// known is left out rather than shown as zero.
func clientToolLimitMessage(offered int, detail llm.ClientToolLimit, presets []string) string {
	var b strings.Builder
	if detail.SchemaRefusal() {
		fmt.Fprintf(&b, "tool schema too large: the provider refused the request because the tool %q describes its parameters in more bytes than the provider accepts", detail.Tool)
		if detail.Limit > 0 {
			fmt.Fprintf(&b, " (at most %d bytes)", detail.Limit)
		}
		b.WriteString("; stop offering that one tool - turn off the MCP server it comes from, or leave it out of a toolsets[] preset")
		b.WriteString(raiseOnCoddyHint("httpserver.max_client_tool_schema_bytes"))
		return b.String()
	}

	b.WriteString("too many tools offered: the provider refused the request because it carries more tools than the provider accepts")
	var facts []string
	if offered > 0 {
		facts = append(facts, fmt.Sprintf("Coddy offered %d tools", offered))
	}
	if detail.Offered > 0 && detail.Offered != offered {
		facts = append(facts, fmt.Sprintf("the provider counted %d", detail.Offered))
	}
	if detail.Limit > 0 {
		facts = append(facts, fmt.Sprintf("it takes at most %d", detail.Limit))
	}
	if len(facts) > 0 {
		b.WriteString(" (" + strings.Join(facts, "; ") + ")")
	}

	if len(presets) > 0 {
		fmt.Fprintf(&b, "; offer fewer with /toolset (configured here: %s), or turn off an MCP server", strings.Join(presets, ", "))
	} else {
		b.WriteString("; offer fewer by turning off an MCP server, or add a toolsets[] preset that leaves some out and pick it with /toolset")
	}
	b.WriteString(raiseOnCoddyHint("httpserver.max_client_tools"))
	return b.String()
}

// raiseOnCoddyHint names the key to raise where the far side is a Coddy
// server. It is said as a condition because the far side may be anything an
// OpenAI-compatible base URL points at, and the keys exist only here.
func raiseOnCoddyHint(key string) string {
	return fmt.Sprintf("; where the provider is a Coddy server you own, raise %s on it", key)
}
