//go:build http

package httpserver

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestConfigLoginSessionTTLZeroRoundTrip(t *testing.T) {
	w := &configRoundTripWorld{}
	if err := w.startSpelledGateway(t); err != nil {
		t.Fatal(err)
	}
	if err := w.save(func(doc map[string]interface{}) error {
		httpConfig := doc["httpserver"].(map[string]interface{})
		login := httpConfig["login"].(map[string]interface{})
		login["session_ttl_hours"] = 0
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadWithPaths(w.paths)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.HTTPServer.Login.SessionTTL(); got != 0 {
		t.Fatalf("reloaded session TTL = %v, want zero", got)
	}
	doc, err := w.read()
	if err != nil {
		t.Fatal(err)
	}
	httpConfig := doc["httpserver"].(map[string]interface{})
	login := httpConfig["login"].(map[string]interface{})
	if got, ok := login["session_ttl_hours"]; !ok || got != float64(0) {
		t.Fatalf("GET session_ttl_hours = %v, present %v; want 0", got, ok)
	}
}
