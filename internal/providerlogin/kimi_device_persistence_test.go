package providerlogin

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestKimiLoginStoresDeviceIdentity(t *testing.T) {
	if !llm.KimiRefreshSupported() {
		t.Skip("secure Kimi credential store is unavailable")
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
	const deviceID = "9f7f5b16-a233-48f3-8c56-8c5526809c12"
	login := &KimiLogin{store: store, region: "global", attempt: attempt, waitToken: func(context.Context) (llm.KimiToken, error) {
		return llm.KimiToken{AccessToken: "synthetic", RefreshToken: "synthetic-refresh", ExpiresAt: time.Now().Add(time.Hour).Unix(), ExpiresIn: 3600, DeviceID: deviceID}, nil
	}}
	if err := login.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	credential, state, err := store.Load("global")
	if err != nil || state != llm.KimiCredentialValid || credential.DeviceID != deviceID {
		t.Fatalf("device ID was not saved: state=%v error=%v", state, err)
	}
}
