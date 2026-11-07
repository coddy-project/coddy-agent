package llm

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrKimiCLILoginRequired indicates that the CLI token is absent, expired or
// unusable. A rejected token must be reported as this error by the caller.
var ErrKimiCLILoginRequired = errors.New("kimi: sign in with Kimi CLI or Coddy")

// ReadKimiCLIAccessToken reads the current CLI token without refreshing or
// changing it. Call only after establishing that the managed credential is
// absent; a managed credential, including an invalid one, always wins.
// noCLILogin is the caller's row-isolation decision and is enforced here.
func ReadKimiCLIAccessToken(mode, region string, noCLILogin bool) (string, error) {
	if noCLILogin || (mode != "" && mode != "subscription") {
		return "", ErrKimiCLILoginRequired
	}
	var name string
	switch region {
	case "", "global":
		data := []byte(`{"oauthHost":"https://auth.kimi.ai","baseUrl":"https://api.kimi.ai/coding/v1"}`)
		digest := sha256.Sum256(data)
		name = fmt.Sprintf("kimi-code-env-%x.json", digest[:8])
	case "china":
		name = "kimi-code.json"
	default:
		return "", ErrKimiCLILoginRequired
	}
	home := os.Getenv("KIMI_CODE_HOME")
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil || home == "" {
			return "", ErrKimiCLILoginRequired
		}
		home = filepath.Join(home, ".kimi-code")
	}
	path := filepath.Join(home, "credentials", name)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", ErrKimiCLILoginRequired
	}
	f, err := os.Open(path)
	if err != nil {
		return "", ErrKimiCLILoginRequired
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err != nil || len(data) > 1<<20 {
		return "", ErrKimiCLILoginRequired
	}
	var token struct {
		AccessToken string  `json:"access_token"`
		ExpiresAt   float64 `json:"expires_at"`
	}
	if json.Unmarshal(data, &token) != nil || strings.TrimSpace(token.AccessToken) == "" || token.ExpiresAt < 0 || (token.ExpiresAt > 0 && token.ExpiresAt <= float64(time.Now().Unix())) {
		return "", ErrKimiCLILoginRequired
	}
	return token.AccessToken, nil
}
