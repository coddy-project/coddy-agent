package config

import "testing"

// TestExpandEnvEscaped covers the "$$" literal escape alongside normal ${VAR}/$VAR expansion,
// the mechanism that lets secrets containing "$" (e.g. a proxy password) survive config load.
func TestExpandEnvEscaped(t *testing.T) {
	t.Setenv("CODDY_EXPAND_TEST", "value")
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "double dollar is literal", in: "a$$b", want: "a$b"},
		{name: "braced ref still expands", in: "x${CODDY_EXPAND_TEST}y", want: "xvaluey"},
		{name: "escaped braced ref is literal", in: "x$${CODDY_EXPAND_TEST}y", want: "x${CODDY_EXPAND_TEST}y"},
		{name: "bare ref still expands", in: "$CODDY_EXPAND_TEST", want: "value"},
		{name: "unset ref is empty", in: "a${CODDY_UNSET_XYZ}b", want: "ab"},
		{name: "bcrypt-style password", in: "$$2y$$10$$abcdEF", want: "$2y$10$abcdEF"},
		{name: "escaped proxy url", in: "http://u:$$2y$$10$$abcd@127.0.0.1:3128", want: "http://u:$2y$10$abcd@127.0.0.1:3128"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := expandEnvEscaped(tt.in); got != tt.want {
				t.Errorf("expandEnvEscaped(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestEscapeYAMLDollarRoundTrip proves the on-disk escape and the load-time unescape are inverses:
// a literal secret survives escapeYAMLDollar -> expandEnvEscaped verbatim.
func TestEscapeYAMLDollarRoundTrip(t *testing.T) {
	for _, s := range []string{
		"http://user:$2y$10$abcd@proxy:3128",
		"socks5h://user:p$$w0rd@127.0.0.1:1080",
		"no-dollars-here",
		"$1$2$3",
	} {
		if got := expandEnvEscaped(escapeYAMLDollar(s)); got != s {
			t.Errorf("round-trip for %q = %q", s, got)
		}
	}
}
