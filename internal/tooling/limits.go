package tooling

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// hardMaxLimitedOutputBytes is the byte safety ceiling applied whenever a line
// limit is enabled. It prevents a single minified/base64 line from bypassing the
// guard. Disabled line limits remain fully unlimited.
const hardMaxLimitedOutputBytes = 64 * 1024

// TruncateLines caps out to at most maxLines lines and, while that limit is
// enabled, to hardMaxLimitedOutputBytes. When it truncates it appends a marker
// naming the config knob (hint) and how much was dropped, so the model knows the
// result is partial and how to fetch the rest. maxLines <= 0 means unlimited.
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

	lineCut := len(out)
	if total > maxLines {
		// Keep the first maxLines lines.
		lineCut = 0
		for kept := 0; kept < maxLines; kept++ {
			nl := strings.IndexByte(out[lineCut:], '\n')
			if nl < 0 {
				lineCut = len(out)
				break
			}
			lineCut += nl + 1
		}
	}

	byteCut := len(out)
	if len(out) > hardMaxLimitedOutputBytes {
		byteCut = safeBytePrefix(out, hardMaxLimitedOutputBytes)
	}

	cut := lineCut
	truncatedByBytes := byteCut < cut
	if truncatedByBytes {
		cut = byteCut
	}
	if cut >= len(out) {
		return out
	}

	head := strings.TrimRight(out[:cut], "\r\n")
	var marker string
	if truncatedByBytes {
		marker = fmt.Sprintf("[output truncated: showing first %d of %d bytes", len(head), len(out))
	} else {
		marker = fmt.Sprintf("[output truncated: showing first %d of %d lines", maxLines, total)
	}
	if strings.TrimSpace(hint) != "" {
		marker += " (" + strings.TrimSpace(hint) + ")"
	}
	marker += "]"
	return head + "\n" + marker
}

// safeBytePrefix returns a cut point no larger than max that does not split a
// valid UTF-8 rune. When a newline is reasonably close to the ceiling, it cuts
// there instead so ordinary multi-line output does not end in a partial record.
func safeBytePrefix(out string, max int) int {
	if max <= 0 || len(out) <= max {
		return len(out)
	}
	cut := max
	if nl := strings.LastIndexByte(out[:cut], '\n'); nl >= cut/2 {
		cut = nl + 1
	}
	for cut > 0 && cut < len(out) && !utf8.RuneStart(out[cut]) {
		cut--
	}
	return cut
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

type outputLimitedError struct {
	cause error
	text  string
}

func (e *outputLimitedError) Error() string { return e.text }
func (e *outputLimitedError) Unwrap() error { return e.cause }

// ApplyOutputLimitError applies the same output ceiling to a tool error while
// retaining its original cause for errors.Is/errors.As.
func ApplyOutputLimitError(err error, tool string, env *Env) error {
	if err == nil {
		return nil
	}
	text := ApplyOutputLimit(err.Error(), tool, env)
	if text == err.Error() {
		return err
	}
	return &outputLimitedError{cause: err, text: text}
}
