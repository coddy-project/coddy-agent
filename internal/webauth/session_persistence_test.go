package webauth

import (
	"testing"
	"time"
)

func TestSessionRecordsCookiePersistence(t *testing.T) {
	for _, tc := range []struct {
		name       string
		ttl        time.Duration
		persistent bool
	}{
		{"browser session", 0, false},
		{"persistent", 24 * time.Hour, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewSessionStore()
			token, issued, err := store.Issue("operator", "credential", tc.ttl)
			if err != nil {
				t.Fatal(err)
			}
			if issued.Persistent != tc.persistent {
				t.Fatalf("issued persistent = %v, want %v", issued.Persistent, tc.persistent)
			}
			stored, ok := store.Lookup(token, "credential")
			if !ok || stored.Persistent != tc.persistent {
				t.Fatalf("stored session = %+v, present %v; want persistent %v", stored, ok, tc.persistent)
			}
		})
	}
}
