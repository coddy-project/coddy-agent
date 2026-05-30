package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

const dotEnvName = ".env"

// loadDotEnv reads $CODDY_HOME/.env and sets environment variables that are not already set.
// Existing process environment always takes precedence — .env is a fallback, not an override.
// Missing file is silently ignored.
func loadDotEnv(home string) {
	if strings.TrimSpace(home) == "" {
		return
	}
	path := filepath.Join(home, dotEnvName)
	f, err := os.Open(path)
	if err != nil {
		return // file absent or unreadable — not an error
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		key, value, ok := parseDotEnvLine(scanner.Text())
		if !ok {
			continue
		}
		// Only set when the variable is not already present in the environment.
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, value)
		}
	}
}

// parseDotEnvLine parses one line from a .env file.
// Supported formats:
//
//	KEY=VALUE
//	export KEY=VALUE
//	KEY="quoted value"
//	KEY='single quoted'
//	# comment line
//	(blank line)
//
// Inline comments after the value are stripped for unquoted values.
// Returns (key, value, true) when a valid assignment is found.
func parseDotEnvLine(line string) (key, value string, ok bool) {
	// Strip leading/trailing whitespace.
	line = strings.TrimSpace(line)

	// Skip blank lines and comments.
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}

	// Strip optional "export " prefix.
	if strings.HasPrefix(line, "export ") {
		line = strings.TrimSpace(line[len("export "):])
	}

	// Must contain '='.
	idx := strings.IndexByte(line, '=')
	if idx <= 0 {
		return "", "", false
	}

	key = strings.TrimSpace(line[:idx])
	// Key must be a valid identifier: letters, digits, underscore.
	if !isValidEnvKey(key) {
		return "", "", false
	}

	raw := line[idx+1:]

	// Handle quoted values.
	if len(raw) >= 2 {
		q := raw[0]
		if q == '"' || q == '\'' {
			end := strings.LastIndexByte(raw, q)
			if end > 0 {
				value = raw[1:end]
				if q == '"' {
					value = unescapeDoubleQuoted(value)
				}
				return key, value, true
			}
		}
	}

	// Unquoted value: strip inline comment and surrounding whitespace.
	if ci := strings.IndexByte(raw, '#'); ci >= 0 {
		raw = raw[:ci]
	}
	value = strings.TrimSpace(raw)
	return key, value, true
}

func isValidEnvKey(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}

// unescapeDoubleQuoted handles common escape sequences inside double-quoted values.
func unescapeDoubleQuoted(s string) string {
	s = strings.ReplaceAll(s, `\n`, "\n")
	s = strings.ReplaceAll(s, `\r`, "\r")
	s = strings.ReplaceAll(s, `\t`, "\t")
	s = strings.ReplaceAll(s, `\\`, `\`)
	s = strings.ReplaceAll(s, `\"`, `"`)
	return s
}
