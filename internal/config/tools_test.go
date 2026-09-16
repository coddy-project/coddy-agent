package config

import (
	"net/url"
	"strings"
	"testing"
)

func TestToolOutputLimitsDefaults(t *testing.T) {
	var l ToolOutputLimits
	cases := map[string]int{
		"read":            OutputLimitDefaultRead,
		"grep":            OutputLimitDefaultGrep,
		"glob":            OutputLimitDefaultGlob,
		"print_tree":      OutputLimitDefaultPrintTree,
		"run_command":     OutputLimitDefaultRunCommand,
		"ssh_run_command": OutputLimitDefaultSSHRunCommand,
		"webfetch":        OutputLimitDefaultWebFetch,
		"websearch":       OutputLimitDefaultWebSearch,
		"anything_else":   OutputLimitDefaultDefault,
		"":                OutputLimitDefaultDefault,
	}
	for tool, want := range cases {
		if got := l.MaxLines(tool); got != want {
			t.Errorf("MaxLines(%q) = %d, want %d", tool, got, want)
		}
	}
}

func TestToolOutputLimitsExplicitOverrides(t *testing.T) {
	zero := 0
	fifty := 50
	l := ToolOutputLimits{Read: &fifty, Grep: &zero}
	if got := l.MaxLines("read"); got != 50 {
		t.Fatalf("read = %d, want 50", got)
	}
	if got := l.MaxLines("grep"); got != 0 {
		t.Fatalf("grep = %d, want 0 (explicit unlimited)", got)
	}
	// Unset field still falls back to its default.
	if got := l.MaxLines("glob"); got != OutputLimitDefaultGlob {
		t.Fatalf("glob = %d, want default %d", got, OutputLimitDefaultGlob)
	}
}

func TestToolOutputLimitsAsMapHasDefaultKey(t *testing.T) {
	l := ToolOutputLimits{}
	m := l.AsMap()
	if _, ok := m[""]; !ok {
		t.Fatal("AsMap missing default key")
	}
	if m["read"] != OutputLimitDefaultRead {
		t.Fatalf("AsMap read = %d, want %d", m["read"], OutputLimitDefaultRead)
	}
	if m[""] != OutputLimitDefaultDefault {
		t.Fatalf("AsMap default = %d, want %d", m[""], OutputLimitDefaultDefault)
	}
}

func TestToolsValidateRejectsNegativeOutputLimit(t *testing.T) {
	neg := -1
	c := Tools{OutputLimits: ToolOutputLimits{Read: &neg}}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for negative read limit")
	}
}

func TestToolsValidateAcceptsZeroOutputLimit(t *testing.T) {
	zero := 0
	c := Tools{OutputLimits: ToolOutputLimits{Grep: &zero}}
	if err := c.Validate(); err != nil {
		t.Fatalf("zero limit should be valid: %v", err)
	}
}

func TestHTTPAllowlistMatchesHostsOriginsAndPrefixes(t *testing.T) {
	cases := []struct {
		entry string
		allow []string
		deny  []string
	}{
		{"*", []string{"https://anything.example/x", "http://127.0.0.1:9/"}, nil},
		{"api.github.com", []string{"https://api.github.com/repos", "http://API.GitHub.com:8080/"}, []string{"https://github.com/", "https://evil-api.github.com/"}},
		{"*.example.com", []string{"https://a.example.com/", "http://b.c.example.com:81/x"}, []string{"https://example.com/", "https://notexample.com/"}},
		{"localhost:8080", []string{"http://localhost:8080/health", "https://localhost:8080/"}, []string{"http://localhost/", "http://localhost:8081/"}},
		{"[::1]:9000", []string{"http://[::1]:9000/"}, []string{"http://[::1]:9001/"}},
		{"http://127.0.0.1:3000", []string{"http://127.0.0.1:3000/", "http://127.0.0.1:3000/a/b"}, []string{"https://127.0.0.1:3000/", "http://127.0.0.1:3001/"}},
		{"https://api.example.com", []string{"https://api.example.com:443/v1"}, []string{"https://api.example.com:8443/v1", "http://api.example.com/v1"}},
		{"https://api.example.com/v1", []string{"https://api.example.com/v1", "https://api.example.com/v1/items"}, []string{"https://api.example.com/v10", "https://api.example.com/v2/items"}},
		{"https://api.example.com/v1/", []string{"https://api.example.com/v1/items"}, []string{"https://api.example.com/v1", "https://api.example.com/v2/"}},
		{"https://*.corp.example", []string{"https://git.corp.example/x"}, []string{"http://git.corp.example/x", "https://corp.example/"}},
	}
	for _, c := range cases {
		for _, raw := range c.allow {
			u, _ := url.Parse(raw)
			if !HTTPAllowlistAllows([]string{c.entry}, u) {
				t.Errorf("entry %q does not allow %s", c.entry, raw)
			}
		}
		for _, raw := range c.deny {
			u, _ := url.Parse(raw)
			if HTTPAllowlistAllows([]string{c.entry}, u) {
				t.Errorf("entry %q allows %s", c.entry, raw)
			}
		}
	}
}

func TestHTTPAllowlistRefusesEntriesItCannotMatch(t *testing.T) {
	for _, entry := range []string{
		"", "   ", "ftp://files.example.com", "https://user:pw@api.example.com", "https://api.example.com/?q=1",
		"api.example.com/v1", "api.*.example.com", "*.", "*example.com", "host:0", "host:99999", "host:http",
	} {
		c := Tools{HTTPRequest: ToolHTTPRequest{Allowlist: []string{"api.github.com", entry}}}
		err := c.Validate()
		if err == nil {
			t.Errorf("entry %q was accepted", entry)
			continue
		}
		if !strings.Contains(err.Error(), "tools.http_request.allowlist[1]") {
			t.Errorf("entry %q: error %q does not name the entry", entry, err)
		}
	}
}

func TestHTTPAllowlistValidateTrimsEntries(t *testing.T) {
	c := Tools{HTTPRequest: ToolHTTPRequest{Allowlist: []string{"  api.github.com  "}}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.HTTPRequest.Allowlist[0] != "api.github.com" {
		t.Fatalf("entry = %q", c.HTTPRequest.Allowlist[0])
	}
}
