package tooling

import (
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// commandSegments splits a command into the simple commands the shell runs,
// so that CommandAllowed matches each of them against the allowlist. ok is
// false when the command does something no allowlist entry can vouch for, or
// uses syntax this reader does not follow exactly: such a command asks.
func commandSegments(shell platform.ShellKind, cmd string) (segments []string, ok bool) {
	switch shell {
	case platform.ShellBash, platform.ShellSh:
		return posixSegments(cmd)
	default:
		return plainSegment(cmd)
	}
}

// windowsMetacharacters are the characters PowerShell or cmd.exe read as
// operators, substitutions, redirections or escapes. The two quote and escape
// differently from a POSIX shell and from each other: cmd.exe has no single
// quotes and escapes with ^, PowerShell escapes with a backtick and leaves a
// backslash alone, and cmd.exe expands %VAR% before it reads its operators.
// So there a prefix covers only a command with none of them anywhere, quoted
// or not.
const windowsMetacharacters = "|&;<>()$`^%\n\r"

// plainSegment accepts a command that is one plain invocation on any shell.
func plainSegment(cmd string) ([]string, bool) {
	if cmd == "" || strings.ContainsAny(cmd, windowsMetacharacters) {
		return nil, false
	}
	return []string{cmd}, true
}

// posixSegments splits a command for sh and bash on the operators that start
// another command (;, &, &&, |, ||, |& and a newline) outside quotes, the way
// the shell reads them: single quotes take everything literally, double quotes
// and a backslash protect an operator. It refuses what runs or writes
// something no single segment shows: command substitution ($(...), backticks,
// also inside double quotes), ${...} and $[...] expansions, whose arithmetic
// runs a command named in an array subscript of a variable's value (also one
// the same line assigns, as ${W:=a[\$(cmd)]} ${PWD:W} does), process
// substitution and every redirection (< and >), subshell and arithmetic
// parentheses, ANSI-C $'...' strings, whose backslash escapes a quote, and
// comments, whose text the shell skips while a quote in it would hide the next
// line from this reader. A plain $NAME only substitutes a value.
func posixSegments(cmd string) ([]string, bool) {
	const (
		normal = iota
		single
		double
	)
	var segments []string
	var cur strings.Builder
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			segments = append(segments, s)
		}
		cur.Reset()
	}
	wordStart := func() bool {
		s := cur.String()
		return s == "" || s[len(s)-1] == ' ' || s[len(s)-1] == '\t'
	}
	next := func(i int) byte {
		if i+1 < len(cmd) {
			return cmd[i+1]
		}
		return 0
	}

	// expands reports whether $ followed by n starts an expansion that can run
	// a command: $(...), $((...)), ${...} or $[...].
	expands := func(n byte) bool {
		return n == '(' || n == '{' || n == '['
	}

	state := normal
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch state {
		case single:
			cur.WriteByte(c)
			if c == '\'' {
				state = normal
			}
		case double:
			switch {
			case c == '`' || (c == '$' && expands(next(i))):
				return nil, false
			case c == '\\' && i+1 < len(cmd):
				cur.WriteByte(c)
				i++
				cur.WriteByte(cmd[i])
			case c == '"':
				cur.WriteByte(c)
				state = normal
			default:
				cur.WriteByte(c)
			}
		default:
			switch c {
			case '`', '<', '>', '(', ')':
				return nil, false
			case '$':
				if n := next(i); expands(n) || n == '\'' {
					return nil, false
				}
				cur.WriteByte(c)
			case '#':
				if wordStart() {
					return nil, false
				}
				cur.WriteByte(c)
			case '\\':
				if i+1 >= len(cmd) {
					cur.WriteByte(c)
					continue
				}
				i++
				if cmd[i] == '\n' {
					// A line continuation: the shell drops both characters.
					continue
				}
				cur.WriteByte(c)
				cur.WriteByte(cmd[i])
			case '\'':
				cur.WriteByte(c)
				state = single
			case '"':
				cur.WriteByte(c)
				state = double
			case ';', '&', '|', '\n':
				flush()
			default:
				cur.WriteByte(c)
			}
		}
	}
	if state != normal {
		return nil, false
	}
	flush()
	return segments, len(segments) > 0
}
