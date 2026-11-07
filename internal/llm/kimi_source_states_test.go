package llm

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestKimiSourceManagedFailuresDoNotFallback(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	for _, tc := range []struct {
		name, credential, intent string
		want                     error
	}{
		{"revoked", `{"access_token":""}`, "", ErrKimiCredentialUnauthorized},
		{"uncertain", `{"access_token":"managed","generation":1}`, `{"target_generation":2}`, ErrKimiCredentialUncertain},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			store := NewKimiCredentialStore(dir)
			if err := os.WriteFile(store.CredentialPath("global"), []byte(tc.credential), 0600); err != nil {
				t.Fatal(err)
			}
			if tc.intent != "" {
				if err := os.WriteFile(store.intentPath("global"), []byte(tc.intent), 0600); err != nil {
					t.Fatal(err)
				}
			}
			src, err := NewKimiSubscriptionSource(store, "subscription", "global", "none", false)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = src.AccessToken(context.Background())
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}
