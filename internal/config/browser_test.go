package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBrowserKeyRoundTrip(t *testing.T) {
	live := &Config{Browser: BrowserConfig{Enable: true, Token: strings.Repeat("x", 32), Host: "127.0.0.1", Port: 12348}}
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
	if next.Browser != live.Browser {
		t.Fatal("settings save changed the browser listener")
	}
	if _, err := ParseAndValidateConfigJSON([]byte(`{"browser":{"enable":true,"token":"short"}}`), Paths{}); err == nil {
		t.Fatal("short key accepted")
	}
	if err := (&BrowserConfig{CertFile: "cert"}).Validate(); err == nil {
		t.Fatal("unpaired TLS certificate accepted")
	}
}
