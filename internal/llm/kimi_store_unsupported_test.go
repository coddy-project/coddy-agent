//go:build !linux && !android && !darwin && !windows

package llm

import (
	"context"
	"errors"
	"testing"
)

func TestKimiManagedStoreUnsupportedFailsClosed(t *testing.T) {
	if KimiRefreshSupported() {
		t.Fatal("unsupported platform reports managed storage support")
	}
	store := NewKimiCredentialStore(t.TempDir())
	if _, _, err := store.Load("global"); !errors.Is(err, ErrKimiStoreUnsupported) {
		t.Fatalf("Load: %v", err)
	}
	if _, err := store.BeginLogin(context.Background(), "global"); !errors.Is(err, ErrKimiStoreUnsupported) {
		t.Fatalf("BeginLogin: %v", err)
	}
	if err := store.CompleteLogin(context.Background(), "global", "attempt", KimiCredential{AccessToken: "synthetic"}); !errors.Is(err, ErrKimiStoreUnsupported) {
		t.Fatalf("CompleteLogin: %v", err)
	}
	if err := store.Remove("global"); !errors.Is(err, ErrKimiStoreUnsupported) {
		t.Fatalf("Remove: %v", err)
	}
}
