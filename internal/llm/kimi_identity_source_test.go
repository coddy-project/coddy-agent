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

func TestKimiIdentityAbsentUsesCLI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "credentials"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "credentials", "kimi-code.json")
	if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"access_token":"cli","expires_at":%d}`, time.Now().Add(time.Hour).Unix())), 0600); err != nil {
		t.Fatal(err)
	}
	src, err := NewKimiSubscriptionSource(nil, "subscription", "china", "none", false)
	if err != nil {
		t.Fatal(err)
	}
	token, kind, err := src.AccessToken(context.Background())
	if err != nil || token != "cli" || kind != KimiSourceCLI {
		t.Fatalf("%q %q %v", token, kind, err)
	}
}

func TestKimiInputSourceIgnoresLegacyPathWithoutIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", t.TempDir())
	legacy := NewKimiCredentialStore(home)
	if err := os.WriteFile(legacy.CredentialPath("global"), []byte(`{"access_token":"legacy"}`), 0600); err != nil {
		t.Fatal(err)
	}
	source, err := kimiInputSource(ProviderInput{Type: "kimi", AuthPath: home, NoCLILogin: true, ProxyURL: "none"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = source.AccessToken(context.Background())
	if !errors.Is(err, ErrKimiCLILoginRequired) {
		t.Fatalf("legacy credential used or unexpected error: %v", err)
	}
}
