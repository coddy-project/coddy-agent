package tooling

import (
	"fmt"
	"strings"
)

// TruncateLines caps out to at most maxLines lines. When it truncates it appends
// a marker line naming the config knob (hint) and how much was dropped, so the
// model knows the result is partial and how to fetch the rest. maxLines <= 0 means
// unlimited (the input is returned unchanged).
func TruncateLines(out string, maxLines int, hint string) string {
	if maxLines <= 0 || out == "" {
		return out
	}
	// Count lines without allocating a full split when the output is short.
	total := strings.Count(out, "\n") + 1
	// A trailing newline means the last "line" is empty; do not count it.
	if strings.HasSuffix(out, "\n") {
		total--
	}
	if total <= maxLines {
		return out
	}

	// Keep the first maxLines lines.
	idx := 0
	for kept := 0; kept < maxLines; kept++ {
		nl := strings.IndexByte(out[idx:], '\n')
		if nl < 0 {
			idx = len(out)
			break
		}
		idx += nl + 1
	}
	head := strings.TrimRight(out[:idx], "\n")

	marker := fmt.Sprintf("[output truncated: showing first %d of %d lines", maxLines, total)
	if strings.TrimSpace(hint) != "" {
		marker += " (" + strings.TrimSpace(hint) + ")"
	}
	marker += "]"
	return head + "\n" + marker
}

// outputLimitHint names the config knob for a tool and how to fetch the rest of a
// truncated result. MCP and unlisted tools fall through to the default knob.
func outputLimitHint(tool string) string {
	switch tool {
	case "read":
		return "tools.output_limits.read; use offset/limit to read further"
	case "grep":
		return "tools.output_limits.grep; narrow the pattern or path, or raise the limit"
	case "glob":
		return "tools.output_limits.glob; narrow the pattern, or raise the limit"
	case "print_tree":
		return "tools.output_limits.print_tree; target a subdirectory, or raise the limit"
	case "run_command":
		return "tools.output_limits.run_command; filter the command output, or raise the limit"
	case "ssh_run_command":
		return "tools.output_limits.ssh_run_command; filter the command output, or raise the limit"
	case "webfetch":
		return "tools.output_limits.webfetch; raise the limit if you need more"
	case "websearch":
		return "tools.output_limits.websearch; use page for more results, or raise the limit"
	default:
		return "tools.output_limits.default; raise the limit if you need more"
	}
}

// ApplyOutputLimit truncates a tool result to the per-tool line ceiling carried by
// env. It is a no-op when env is nil, carries no limits, or the tool's limit is 0
// (unlimited). Used by the registry for built-ins and by the agent for MCP calls.
func ApplyOutputLimit(out, tool string, env *Env) string {
	if env == nil {
		return out
	}
	limit := env.OutputLineLimit(tool)
	if limit <= 0 {
		return out
	}
	return TruncateLines(out, limit, outputLimitHint(tool))
}
