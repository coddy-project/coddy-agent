package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const activateToolsName = "activate_tools"

func activationToolDefinition() llm.ToolDefinition {
	return llm.ToolDefinition{Name: activateToolsName, Description: "Load complete native definitions for permitted tools by exact catalog name. Call those tools only in a later response, after receiving their definitions. Activation does not grant execution permission.", InputSchema: map[string]interface{}{
		"type": "object", "properties": map[string]interface{}{"names": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "minItems": 1, "uniqueItems": true}}, "required": []string{"names"}, "additionalProperties": false,
	}}
}

// toolActivation belongs to one loop invocation, never to persisted history.
// Compaction keeps it; new Run and detached permission continuation do not.
type toolActivation map[string]bool

func (active toolActivation) offer(permitted []llm.ToolDefinition) []llm.ToolDefinition {
	if len(permitted) == 0 {
		clear(active)
		return nil
	}
	allowed := make(map[string]bool, len(permitted))
	offered := []llm.ToolDefinition{activationToolDefinition()}
	for _, d := range permitted {
		allowed[d.Name] = true
		if active[d.Name] {
			offered = append(offered, d)
		}
	}
	for name := range active {
		if !allowed[name] {
			delete(active, name)
		}
	}
	return offered
}

func (active toolActivation) activate(raw string, permitted []llm.ToolDefinition) (string, error) {
	var args struct {
		Names []string `json:"names"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return "", fmt.Errorf("invalid activation arguments: %w", err)
	}
	if err := decoder.Decode(new(interface{})); err != io.EOF {
		return "", fmt.Errorf("activation requires one JSON object")
	}
	if len(args.Names) == 0 {
		return "", fmt.Errorf("activation requires at least one exact tool name")
	}
	seen := make(map[string]bool, len(args.Names))
	for _, name := range args.Names {
		if name == activateToolsName || !offersTool(permitted, name) {
			return "", fmt.Errorf("unknown or excluded tool: %q", name)
		}
		if seen[name] {
			return "", fmt.Errorf("duplicate activation name: %q", name)
		}
		seen[name] = true
	}
	// Validate the entire batch before adding any name. The next request reads
	// original definitions from the permitted set, without rewriting metadata.
	for _, name := range args.Names {
		active[name] = true
	}
	acknowledged, _ := json.Marshal(struct {
		Activated []string `json:"activated"`
	}{args.Names})
	return string(acknowledged), nil
}

// The catalog is deliberately lossy. The complete description and schema must
// be offered natively before execution; summaries never replace that contract.
func toolDiscoveryCatalog(defs []llm.ToolDefinition) string {
	if len(defs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Lossy discovery summaries (first sentence, at most 24 words). Call activate_tools with exact names to load complete definitions; call newly activated tools only in the next response. Activation does not grant execution permission.\n")
	for _, d := range defs {
		description := strings.TrimSpace(d.Description)
		for i, r := range description {
			if r != '.' && r != '?' && r != '!' {
				continue
			}
			next, _ := utf8.DecodeRuneInString(description[i+1:])
			if i+1 == len(description) || unicode.IsSpace(next) {
				description = description[:i+1]
				break
			}
		}
		words := strings.Fields(description)
		if len(words) > 24 {
			words = append(words[:24], "...")
		}
		fmt.Fprintf(&b, "- `%s`: %s\n", d.Name, strings.Join(words, " "))
	}
	return strings.TrimRight(b.String(), "\n")
}

// Discovery remains present even when a custom/child template omits Tools.
func withToolDiscovery(full, catalog string) string {
	if !strings.Contains(full, catalog) {
		return joinNonEmptyPromptBlocks(full, "## Available tools\n\n"+catalog)
	}
	return full
}

// activateToolDefinitions is discovery control, not tool execution: it runs no
// execution hooks, permission gate or tool observer. It still completes the
// streamed/persisted row just as every other announced response call must.
func (a *Agent) activateToolDefinitions(tc llm.ToolCall, active toolActivation, permitted []llm.ToolDefinition) (result string, err error) {
	dir := strings.TrimSpace(a.state.GetPersistedSessionDir())
	if dir != "" {
		_ = session.MarkToolCallStarted(dir, tc.ID, tc.Name, session.ToolKind(tc.Name), "in_progress")
		if writeErr := session.WriteToolCallArgs(dir, tc.ID, tc.InputJSON); writeErr != nil {
			err = fmt.Errorf("persist activation arguments: %w", writeErr)
		}
	}
	if err == nil {
		result, err = active.activate(tc.InputJSON, permitted)
	}
	status := "completed"
	if err != nil {
		status = "failed"
	}
	a.finishToolCall(dir, a.state.GetID(), tc, result, err, status)
	return result, err
}
