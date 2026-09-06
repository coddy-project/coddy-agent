package config

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"
)

func TestEffectiveAPIKeyContextErrReportsAHelperCutShort(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("no sleep binary")
	}
	t.Setenv("HELPER_API_KEY", "")
	p := &ProviderConfig{Name: "helper", Type: "neuraldeep", APIKeyCommand: "sleep 30"}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	key, err := p.EffectiveAPIKeyContextErr(ctx)
	if key != "" || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cut short: key=%q err=%v", key, err)
	}
	// A helper that exits without output is a missing credential, not an error.
	quiet := &ProviderConfig{Name: "helper", Type: "neuraldeep", APIKeyCommand: "true"}
	if key, err := quiet.EffectiveAPIKeyContextErr(context.Background()); key != "" || err != nil {
		t.Fatalf("quiet helper: key=%q err=%v", key, err)
	}
	// The environment still wins over a silent helper.
	t.Setenv("HELPER_API_KEY", "sk-from-env")
	if key, err := quiet.EffectiveAPIKeyContextErr(context.Background()); key != "sk-from-env" || err != nil {
		t.Fatalf("env fallback: key=%q err=%v", key, err)
	}
}
