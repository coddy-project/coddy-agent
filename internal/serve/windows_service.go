package serve

import (
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// sameServiceExecutable compares the executable in a service command line,
// never an arbitrary argument or a path that merely contains exe.
func sameServiceExecutable(imagePath, exe string) bool {
	exe = strings.TrimSpace(exe)
	imagePath = strings.TrimSpace(imagePath)
	if exe == "" || imagePath == "" {
		return false
	}
	if imagePath[0] == '"' {
		path, ok := quotedWindowsExecutable(imagePath)
		return ok && sameWindowsPath(path, exe)
	}
	if len(imagePath) < len(exe) || !sameWindowsPath(imagePath[:len(exe)], exe) {
		return false
	}
	if len(imagePath) == len(exe) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(imagePath[len(exe):])
	return unicode.IsSpace(r)
}

func sameWindowsPath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(strings.TrimSpace(a)), filepath.Clean(strings.TrimSpace(b)))
}

// ServesHome reports whether this service registration belongs to both the
// current executable and the requested agent home. Home is part of service
// identity: stopping an executable-matching service for another home would
// terminate the wrong instance.
func (s WindowsServiceState) ServesHome(home string) bool {
	return s.Installed && s.Home != "" && sameWindowsPath(s.Home, home)
}

func windowsServiceHome(imagePath string) string {
	args, ok := windowsCommandLineArgs(imagePath)
	if !ok || len(args) < 4 || args[1] != "__windows-service" {
		return ""
	}
	for i := 2; i+1 < len(args); i++ {
		if args[i] == "--home" {
			return args[i+1]
		}
	}
	return ""
}

// windowsCommandLineArgs decodes the quoting produced by syscall.EscapeArg.
// Service identity must not be inferred from substring matches in a command
// line, especially when the path selects which instance a verb will stop.
func windowsCommandLineArgs(command string) ([]string, bool) {
	var args []string
	for pos := 0; ; {
		for pos < len(command) && (command[pos] == ' ' || command[pos] == '\t') {
			pos++
		}
		if pos == len(command) {
			return args, true
		}
		var out strings.Builder
		quoted := false
		for pos < len(command) {
			if (command[pos] == ' ' || command[pos] == '\t') && !quoted {
				break
			}
			if command[pos] == '\\' {
				start := pos
				for pos < len(command) && command[pos] == '\\' {
					pos++
				}
				backslashes := pos - start
				if pos < len(command) && command[pos] == '"' {
					out.WriteString(strings.Repeat(`\`, backslashes/2))
					if backslashes%2 != 0 {
						out.WriteByte('"')
					} else {
						quoted = !quoted
					}
					pos++
					continue
				}
				out.WriteString(command[start:pos])
				continue
			}
			if command[pos] == '"' {
				quoted = !quoted
				pos++
				continue
			}
			out.WriteByte(command[pos])
			pos++
		}
		if quoted {
			return nil, false
		}
		args = append(args, out.String())
	}
}

// quotedWindowsExecutable decodes the first argument using the backslash and
// quote rules of a Windows command line. A service executable path cannot
// contain a quote, so the first unescaped quote closes it.
func quotedWindowsExecutable(command string) (string, bool) {
	var out strings.Builder
	for pos := 1; pos < len(command); {
		if command[pos] != '\\' {
			if command[pos] == '"' {
				return out.String(), true
			}
			out.WriteByte(command[pos])
			pos++
			continue
		}
		start := pos
		for pos < len(command) && command[pos] == '\\' {
			pos++
		}
		backslashes := pos - start
		if pos == len(command) {
			out.WriteString(command[start:pos])
			return out.String(), false
		}
		if command[pos] != '"' {
			out.WriteString(command[start : pos+1])
			pos++
			continue
		}
		out.WriteString(strings.Repeat(`\`, backslashes/2))
		if backslashes%2 != 0 {
			out.WriteByte('"')
			pos++
			continue
		}
		return out.String(), true
	}
	return out.String(), false
}
