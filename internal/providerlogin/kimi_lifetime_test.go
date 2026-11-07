package providerlogin

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestKimiLoginStoresTokenLifetime(t *testing.T) {
	if !llm.KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	id, err := config.NewKimiIdentity()
	if err != nil {
		t.Fatal(err)
	}
	store := llm.NewKimiCredentialStore(config.KimiIdentityDir(home, id))
	attempt, err := store.BeginLogin(context.Background(), "global")
	if err != nil {
		t.Fatal(err)
	}
	login := &KimiLogin{store: store, region: "global", attempt: attempt, waitToken: func(context.Context) (llm.KimiToken, error) {
		return llm.KimiToken{AccessToken: "synthetic", RefreshToken: "synthetic-refresh", ExpiresAt: time.Now().Add(2 * time.Hour).Unix(), ExpiresIn: 7200}, nil
	}}
	if err := login.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	credential, state, err := store.Load("global")
	if err != nil || state != llm.KimiCredentialValid || credential.ExpiresIn != 7200 {
		t.Fatalf("credential lifetime = %d, state = %v, error = %v", credential.ExpiresIn, state, err)
	}
}
