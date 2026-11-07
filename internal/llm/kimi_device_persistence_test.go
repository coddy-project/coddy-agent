package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestKimiRefreshPreservesDeviceIdentity(t *testing.T) {
	if !kimiSecureStoreAvailable || !KimiRefreshSupported() {
		t.Skip("secure Kimi credential store is unavailable")
	}
	const deviceID = "9f7f5b16-a233-48f3-8c56-8c5526809c12"
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	store := NewKimiCredentialStore(dir)
	old := []byte(`{"access_token":"old","refresh_token":"old-refresh","expires_at":1,"expires_in":7200,"generation":1,"device_id":"` + deviceID + `"}`)
	if err := os.WriteFile(store.CredentialPath("global"), old, 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Msh-Device-Id"); got != deviceID {
			t.Errorf("refresh device ID = %q, want stored identity", got)
		}
		_, _ = w.Write([]byte(`{"access_token":"new","refresh_token":"new-refresh","expires_in":7200}`))
	}))
	defer server.Close()
	refresher, err := NewKimiCredentialRefresher(store, "global", server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if token, err := refresher.AccessToken(ctx); err != nil || token != "new" {
		t.Fatalf("refresh failed: %v", err)
	}
	data, err := os.ReadFile(store.CredentialPath("global"))
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result["device_id"] != deviceID {
		t.Fatalf("device identity not retained after refresh")
	}
}
