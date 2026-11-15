package config

import (
	"net/url"
	"os"
	"path/filepath"
	"reflect"
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

func TestPreviewServerDefaultsToLoopbackAndEnabled(t *testing.T) {
	var section *ToolPreviewServer
	got := section.ToolSettings()
	if !got.Enabled || got.Host != PreviewServerDefaultHost || got.PublicHost != "" {
		t.Fatalf("nil section: %+v", got)
	}
	off := false
	got = (&ToolPreviewServer{Enabled: &off, Host: " ::1 ", PublicHost: " dev.example "}).ToolSettings()
	if got.Enabled || got.Host != "::1" || got.PublicHost != "dev.example" {
		t.Fatalf("configured section: %+v", got)
	}
}

func TestToolsValidateWantsABareHostForThePreviewServer(t *testing.T) {
	for _, host := range []string{"", "127.0.0.1", "0.0.0.0", "::1", "localhost", "dev.example"} {
		tools := Tools{PreviewServer: ToolPreviewServer{Host: host, PublicHost: host}}
		if err := tools.Validate(); err != nil {
			t.Errorf("host %q: %v", host, err)
		}
	}
	for _, host := range []string{"127.0.0.1:8080", "[::1]:8080", "[::1]", "http://localhost", "localhost/app", "my host"} {
		tools := Tools{PreviewServer: ToolPreviewServer{Host: host}}
		if err := tools.Validate(); err == nil {
			t.Errorf("host %q was accepted", host)
		}
		tools = Tools{PreviewServer: ToolPreviewServer{PublicHost: host}}
		if err := tools.Validate(); err == nil {
			t.Errorf("public_host %q was accepted", host)
		}
	}
}

func TestHTTPDefaultHeadersValidate(t *testing.T) {
	ok := Tools{HTTPRequest: ToolHTTPRequest{DefaultHeaders: map[string]string{
		"User-Agent": "Mozilla/5.0 (X11; Linux x86_64) Chrome/131.0.0.0",
		"accept":     "application/manifest+json",
		"X-Empty":    "",
		"Cookie":     "session=1",
	}}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("a valid map was refused: %v", err)
	}
	if len(ok.HTTPRequest.DefaultHeaders) != 4 || ok.HTTPRequest.DefaultHeaders["accept"] != "application/manifest+json" {
		t.Fatalf("validation changed the map: %v", ok.HTTPRequest.DefaultHeaders)
	}
	for _, c := range []struct {
		headers map[string]string
		names   string
	}{
		{map[string]string{"Host": "api.internal"}, "tools.http_request.default_headers.Host"},
		{map[string]string{"content-type": "application/json"}, "tools.http_request.default_headers.content-type"},
		{map[string]string{"Content-Length": "3"}, "tools.http_request.default_headers.Content-Length"},
		{map[string]string{"Transfer-Encoding": "chunked"}, "tools.http_request.default_headers.Transfer-Encoding"},
		{map[string]string{"proxy-authorization": "Basic dTpw"}, "tools.http_request.default_headers.proxy-authorization"},
		{map[string]string{"Upgrade": "h2c"}, "tools.http_request.default_headers.Upgrade"},
		{map[string]string{"connection": "close"}, "tools.http_request.default_headers.connection"},
		{map[string]string{"TE": "trailers"}, "tools.http_request.default_headers.TE"},
		{map[string]string{"Bad Name": "x"}, `"Bad Name"`},
		{map[string]string{"User-Agent:": "x"}, `"User-Agent:"`},
		{map[string]string{"": "x"}, `""`},
		{map[string]string{"123": "x"}, `"123"`},
		{map[string]string{"X.Trace": "x"}, `"X.Trace"`},
		{map[string]string{"-": "x"}, `"-"`},
		{map[string]string{"_X": "x"}, `"_X"`},
		{map[string]string{"X-Split": "a\r\nInjected: b"}, "tools.http_request.default_headers.X-Split"},
		{map[string]string{"User-Agent": "a", "user-agent": "b"}, "same header"},
	} {
		tools := Tools{HTTPRequest: ToolHTTPRequest{DefaultHeaders: c.headers}}
		err := tools.Validate()
		if err == nil {
			t.Errorf("%v was accepted", c.headers)
			continue
		}
		if !strings.Contains(err.Error(), c.names) {
			t.Errorf("%v: error %q does not name %s", c.headers, err, c.names)
		}
	}
}

func TestHTTPDefaultHeadersSurviveTheSettingsRoundTrip(t *testing.T) {
	cfg := &Config{}
	cfg.Tools.HTTPRequest.DefaultHeaders = map[string]string{"User-Agent": "", "Accept": "application/json"}
	back := JSONDTOToConfig(ConfigToJSONDTO(cfg), Paths{})
	if !reflect.DeepEqual(back.Tools.HTTPRequest.DefaultHeaders, cfg.Tools.HTTPRequest.DefaultHeaders) {
		t.Fatalf("the settings document carries %v, want %v", back.Tools.HTTPRequest.DefaultHeaders, cfg.Tools.HTTPRequest.DefaultHeaders)
	}
	back.Tools.HTTPRequest.DefaultHeaders["Accept"] = "changed"
	if cfg.Tools.HTTPRequest.DefaultHeaders["Accept"] != "application/json" {
		t.Fatal("the settings document shares the config's map")
	}

	// An empty value is a statement of its own - leave the header out - and
	// must come back from the file as one.
	paths := testPathConfig(t, "tools:\n  http_request:\n    default_headers:\n      User-Agent: \"\"\n      X-Client: coddy-lab\n")
	loaded, err := LoadWithPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	data, err := MarshalConfigYAMLForFile(loaded, paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.ConfigPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	again, err := LoadWithPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"User-Agent": "", "X-Client": "coddy-lab"}
	if !reflect.DeepEqual(again.Tools.HTTPRequest.DefaultHeaders, want) {
		t.Fatalf("after a save the file holds %v, want %v\n%s", again.Tools.HTTPRequest.DefaultHeaders, want, data)
	}
}

func TestPreviewPublishValidateRefusesWhatCouldNotServe(t *testing.T) {
	cases := map[string]ToolPreviewPublish{
		"a host with a port":          {Host: "127.0.0.1:8081"},
		"a host with a scheme":        {Host: "http://127.0.0.1"},
		"a bracketed host":            {Host: "[::1]"},
		"a port out of range":         {Port: 70000},
		"a relative root":             {Root: "previews"},
		"a public URL with no scheme": {PublicURL: "coddy.example/preview/"},
		"a public URL with a query":   {PublicURL: "https://coddy.example/preview/?token=x"},
		"a negative lifetime":         {TTLSeconds: -1},
		"a negative size":             {MaxBytes: -1},
	}
	for name, section := range cases {
		t.Run(name, func(t *testing.T) {
			if err := section.validate(); err == nil {
				t.Fatalf("%+v should not validate", section)
			}
		})
	}
	ok := ToolPreviewPublish{
		Enable: true, Host: "127.0.0.1", Port: 8081,
		Root:      filepath.Join(string(filepath.Separator), "var", "lib", "coddy", "previews"),
		PublicURL: "https://coddy.example/preview/", TTLSeconds: 3600, MaxBytes: 1 << 20,
	}
	if err := ok.validate(); err != nil {
		t.Fatalf("a complete section should validate: %v", err)
	}
}

func TestPreviewPublishToolSettingsFillInTheDefaults(t *testing.T) {
	home := filepath.Join(string(filepath.Separator), "home", "u", ".coddy")
	section := ToolPreviewPublish{Enable: true}
	got := section.ToolSettings(home)
	if want := filepath.Join(home, "previews"); got.Root != want {
		t.Fatalf("root = %q, want %q", got.Root, want)
	}
	if got.PublicURL != "http://127.0.0.1:8081/" {
		t.Fatalf("public URL = %q", got.PublicURL)
	}
	if got.TTLSeconds != PreviewPublishDefaultTTLSeconds || got.MaxBytes != PreviewPublishDefaultMaxBytes {
		t.Fatalf("defaults: %+v", got)
	}
	if addr := section.ListenAddr(); addr != "127.0.0.1:8081" {
		t.Fatalf("listen address = %q", addr)
	}
	// A wildcard bind is not an address anybody opens, so the link falls back
	// to a name that reaches this machine from a browser.
	wildcard := ToolPreviewPublish{Enable: true, Host: "0.0.0.0", Port: 9000}
	if got := wildcard.ToolSettings(home); got.PublicURL != "http://localhost:9000/" {
		t.Fatalf("wildcard public URL = %q", got.PublicURL)
	}
}
