package session_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestEffectiveSessionCWD(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	wantBase, err := filepath.Abs(base)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("client wins", func(t *testing.T) {
		t.Parallel()
		sub := filepath.Join(base, "proj")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		got, err := session.EffectiveSessionCWD(sub, "/should-not-use")
		if err != nil {
			t.Fatal(err)
		}
		want, err := filepath.Abs(sub)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	})
	t.Run("fallback when client empty", func(t *testing.T) {
		t.Parallel()
		got, err := session.EffectiveSessionCWD("   ", base)
		if err != nil {
			t.Fatal(err)
		}
		if got != wantBase {
			t.Fatalf("got %q want %q", got, wantBase)
		}
	})
	t.Run("both empty errors", func(t *testing.T) {
		t.Parallel()
		_, err := session.EffectiveSessionCWD("", "")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}
