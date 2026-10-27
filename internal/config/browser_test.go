package config

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestBrowserKeyRoundTrip(t *testing.T) {
	live := &Config{Browser: BrowserConfig{Enable: true, Token: strings.Repeat("x", 32), ServeRemote: true, Remotes: []string{"home"}}}
	raw, err := json.Marshal(ConfigToJSONDTO(live))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), live.Browser.Token) {
		t.Fatal("browser key leaked")
	}
	next, err := ParseConfigJSONPreservingSecrets(raw, Paths{}, live)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(next.Browser, live.Browser) {
		t.Fatal("a settings save changed the browser block")
	}
	if _, err := ParseAndValidateConfigJSON([]byte(`{"browser":{"enable":true,"token":"short"}}`), Paths{}); err == nil {
		t.Fatal("short key accepted")
	}
}

// What each combination of the block's keys asks for.
func TestBrowserBlockValidation(t *testing.T) {
	key := strings.Repeat("k", 32)
	for _, c := range []struct {
		name string
		b    BrowserConfig
		want string
	}{
		{"off asks nothing", BrowserConfig{}, ""},
		{"a key", BrowserConfig{Enable: true, Token: key}, ""},
		{"remotes alone", BrowserConfig{Enable: true, Remotes: []string{"home"}}, ""},
		{"serving with a key", BrowserConfig{Enable: true, Token: key, ServeRemote: true}, ""},
		{"neither a key nor remotes", BrowserConfig{Enable: true}, "browser.token"},
		{"a short key", BrowserConfig{Enable: true, Token: "short"}, "32 characters"},
		{"serving without a key", BrowserConfig{Enable: true, ServeRemote: true, Remotes: []string{"home"}}, "serve_remote needs browser.token"},
		{"an empty remote", BrowserConfig{Enable: true, Remotes: []string{" "}}, "remotes[0]"},
	} {
		err := c.b.Validate()
		if c.want == "" && err != nil || c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

// The extension connects to the HTTP server, so accepting it with the HTTP
// server off is refused when the configuration loads; a process that only
// uses the browsers of other servers needs no HTTP server of its own.
func TestTheBrowserNeedsTheHTTPServer(t *testing.T) {
	off := false
	cfg := &Config{Browser: BrowserConfig{Enable: true, Token: strings.Repeat("x", 32)}, HTTPServer: HTTPServerConfig{Enabled: &off}}
	if err := validateSubconfigs(cfg); err == nil || !strings.Contains(err.Error(), "httpserver.enable") {
		t.Fatalf("browser without the HTTP server: %v", err)
	}
	// With the HTTP server on, nothing about the browser is refused (a bare
	// struct trips over other sections' unapplied defaults, not this one).
	cfg.HTTPServer.Enabled = nil
	if err := validateSubconfigs(cfg); err != nil && strings.Contains(err.Error(), "browser") {
		t.Fatal(err)
	}
	cfg = &Config{Browser: BrowserConfig{Enable: true, Remotes: []string{"home"}}, HTTPServer: HTTPServerConfig{Enabled: &off}}
	if err := validateSubconfigs(cfg); err != nil && strings.Contains(err.Error(), "browser") {
		t.Fatalf("browser remotes without the HTTP server: %v", err)
	}
}
