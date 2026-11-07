package llm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestKimiCLIAuth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	dir := filepath.Join(home, "credentials")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	// Produced by upstream JSON.stringify and SHA-256 for the global profile.
	global := filepath.Join(dir, "kimi-code-env-0e4f99c69cc27850.json")
	china := filepath.Join(dir, "kimi-code.json")
	write := func(path, text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	future := time.Now().Add(time.Hour).Unix()
	fixture := func(token string, expiry int64) string {
		return fmt.Sprintf(`{"access_token":%q,"refresh_token":"secret-refresh","expires_at":%d}`, token, expiry)
	}
	write(global, fixture("global-token", future))
	write(china, fixture("china-token", future))
	for _, tc := range []struct{ region, want string }{{"global", "global-token"}, {"", "global-token"}, {"china", "china-token"}} {
		got, err := ReadKimiCLIAccessToken("subscription", tc.region, false)
		if err != nil || got != tc.want {
			t.Errorf("%s: token=%q err=%v", tc.region, got, err)
		}
	}
	for _, tc := range []struct {
		mode, region string
		disabled     bool
	}{{"platform", "global", false}, {"subscription", "global", true}, {"subscription", "other", false}} {
		if token, err := ReadKimiCLIAccessToken(tc.mode, tc.region, tc.disabled); token != "" || err == nil {
			t.Errorf("unexpected token or no error: %q %v", token, err)
		}
	}
	write(global, fixture("expired-secret", time.Now().Add(-time.Hour).Unix()))
	if token, err := ReadKimiCLIAccessToken("", "global", false); token != "" || !errors.Is(err, ErrKimiCLILoginRequired) || strings.Contains(err.Error(), "expired-secret") {
		t.Fatalf("expired: %q %v", token, err)
	}
	write(global, `{invalid secret-refresh`)
	if token, err := ReadKimiCLIAccessToken("", "global", false); token != "" || !errors.Is(err, ErrKimiCLILoginRequired) || strings.Contains(err.Error(), "secret-refresh") {
		t.Fatalf("corrupt: %q %v", token, err)
	}
}

func TestKimiCLIAuthConcurrentReplacement(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	dir := filepath.Join(home, "credentials")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "kimi-code.json")
	fixture := []byte(fmt.Sprintf(`{"access_token":"replacement","expires_at":%d}`, time.Now().Add(time.Hour).Unix()))
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 100; i++ {
			tmp := filepath.Join(dir, "next")
			if err := os.WriteFile(tmp, fixture, 0600); err != nil {
				done <- err
				return
			}
			if err := os.Rename(tmp, path); err != nil {
				done <- err
				return
			}
			if err := os.Remove(path); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for i := 0; i < 1000; i++ {
		token, err := ReadKimiCLIAccessToken("subscription", "china", false)
		if err == nil && token != "replacement" {
			t.Errorf("unexpected token %q", token)
		}
		if err != nil && !errors.Is(err, ErrKimiCLILoginRequired) {
			t.Errorf("unexpected error %v", err)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestKimiCLIAuthHomeAndReadOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", "")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, ".kimi-code", "credentials", "kimi-code.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte(fmt.Sprintf(`{"access_token":"fixture","refresh_token":"private","expires_at":%d}`, time.Now().Add(time.Hour).Unix()))
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	token, err := ReadKimiCLIAccessToken("", "china", false)
	if err != nil || token != "fixture" {
		t.Fatalf("token=%q err=%v", token, err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("credential file changed")
	}
}
