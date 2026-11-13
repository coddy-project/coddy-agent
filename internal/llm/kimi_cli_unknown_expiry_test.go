package llm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKimiCLIAuthUnknownExpiry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	dir := filepath.Join(home, "credentials")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "kimi-code.json")
	for _, payload := range []string{
		`{"access_token":"synthetic","expires_at":0}`,
		`{"access_token":"synthetic"}`,
	} {
		if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := ReadKimiCLIAccessToken("subscription", "china", false)
		if err != nil || got != "synthetic" {
			t.Fatalf("unknown expiry: token=%q error=%v", got, err)
		}
	}
}
