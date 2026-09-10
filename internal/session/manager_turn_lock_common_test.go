package session

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
)

func TestAcquireStubTurnLockReturnsBusy(t *testing.T) {
	mgr := &Manager{}
	unlock, err := mgr.acquireStubTurnLock("sess_busy")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.acquireStubTurnLock("sess_busy"); err != ErrSessionTurnBusy {
		t.Fatalf("second lock error = %v, want %v", err, ErrSessionTurnBusy)
	}
	unlock()

	unlockAgain, err := mgr.acquireStubTurnLock("sess_busy")
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	unlockAgain()
}

// The manager tags its own logger, so logger.levels can name "session"
// whichever entrypoint built it. Without the tag the component key is absent
// and a configured override for "session" scopes nothing.
func TestNewManagerTagsItsLoggerWithTheSessionComponent(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	m := NewManager(&config.Config{}, nil, nil, base, t.TempDir(), nil)

	m.log.Info("probe")

	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec); err != nil {
		t.Fatalf("log line is not JSON: %v\n%s", err, buf.String())
	}
	if got := rec[logger.ComponentKey]; got != logger.ComponentSession {
		t.Fatalf("component = %v, want %q", got, logger.ComponentSession)
	}
}
