package llm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestKimiSourceSelection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	dir := filepath.Join(home, "credentials")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(dir, "kimi-code.json")
	data := []byte(fmt.Sprintf(`{"access_token":"cli","refresh_token":"unchanged","expires_at":%d}`, time.Now().Add(time.Hour).Unix()))
	if err := os.WriteFile(cli, data, 0600); err != nil {
		t.Fatal(err)
	}
	storeDir := t.TempDir()
	if err := os.Chmod(storeDir, 0700); err != nil {
		t.Fatal(err)
	}
	store := NewKimiCredentialStore(storeDir)
	src, err := NewKimiSubscriptionSource(store, "subscription", "china", "none", false)
	if err != nil {
		t.Fatal(err)
	}
	token, kind, err := src.AccessToken(context.Background())
	if err != nil || token != "cli" || kind != KimiSourceCLI {
		t.Fatalf("fallback %q %q %v", token, kind, err)
	}
	if _, err := src.Rejected(context.Background(), kind); !errors.Is(err, ErrKimiCLILoginRequired) {
		t.Fatalf("CLI rejection: %v", err)
	}
	after, err := os.ReadFile(cli)
	if err != nil || string(after) != string(data) {
		t.Fatal("CLI credential changed")
	}
	attempt, err := store.BeginLogin(context.Background(), "china")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteLogin(context.Background(), "china", attempt, KimiCredential{AccessToken: "managed"}); err != nil {
		t.Fatal(err)
	}
	token, kind, err = src.AccessToken(context.Background())
	if err != nil || token != "managed" || kind != KimiSourceManaged {
		t.Fatalf("precedence %q %q %v", token, kind, err)
	}
	if err := os.WriteFile(store.CredentialPath("china"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err = src.AccessToken(context.Background())
	if !errors.Is(err, ErrKimiCredentialCorrupt) {
		t.Fatalf("corrupt: %v", err)
	}
	disabled, err := NewKimiSubscriptionSource(nil, "subscription", "china", "none", true)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = disabled.AccessToken(context.Background())
	if !errors.Is(err, ErrKimiCLILoginRequired) {
		t.Fatalf("disabled: %v", err)
	}
	other, err := NewKimiSubscriptionSource(nil, "subscription", "global", "none", false)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = other.AccessToken(context.Background())
	if !errors.Is(err, ErrKimiCLILoginRequired) {
		t.Fatalf("region: %v", err)
	}
}

func TestKimiSourceRejectsPlatformAndInvalidProxy(t *testing.T) {
	store := NewKimiCredentialStore(t.TempDir())
	if _, err := NewKimiSubscriptionSource(store, "platform", "global", "none", false); err == nil {
		t.Fatal("platform accepted")
	}
	if _, err := NewKimiSubscriptionSource(store, "subscription", "global", "://invalid", false); err == nil {
		t.Fatal("invalid proxy accepted")
	}
}
