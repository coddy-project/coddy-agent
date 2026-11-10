package webauth

import (
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestBackupGenerationAndStorage(t *testing.T) {
	state, codes, err := GenerateBackupCodes(10)
	if err != nil {
		t.Fatal(err)
	}
	if state.Generation == "" || len(codes) != 10 || len(state.Hashes) != 10 {
		t.Fatalf("bad set: %+v", state)
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range codes {
		if !regexp.MustCompile(`^[A-F0-9]{8}-[A-F0-9]{8}$`).MatchString(code) {
			t.Fatalf("bad code format %q", code)
		}
		if strings.Contains(string(data), code) {
			t.Fatal("plaintext in state")
		}
	}
	next, _, err := GenerateBackupCodes(10)
	if err != nil {
		t.Fatal(err)
	}
	if next.Generation == state.Generation {
		t.Fatal("generation reused")
	}
	if AccountFingerprint("u", "h", "", state.Generation) == AccountFingerprint("u", "h", "", next.Generation) {
		t.Fatal("rotation kept fingerprint")
	}
	if AccountFingerprint("u", "h", "", "") != AccountFingerprint("u", "h", "") {
		t.Fatal("empty generation changed fingerprint")
	}
}

func TestBackupConsumeConcurrent(t *testing.T) {
	state, codes, err := GenerateBackupCodes(3)
	if err != nil {
		t.Fatal(err)
	}
	store := NewBackupStore(state)
	before := AccountFingerprint("u", "h", "", state.Generation)
	if store.Consume("wrong") {
		t.Fatal("wrong code accepted")
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if store.Consume(codes[1]) {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("accepted %d times", successes.Load())
	}
	after := store.Snapshot()
	if len(after.Hashes) != 2 || after.Generation != state.Generation {
		t.Fatal("consumption changed generation or wrong count")
	}
	if before != AccountFingerprint("u", "h", "", after.Generation) {
		t.Fatal("consumption invalidated session")
	}
	for _, code := range []string{codes[0], codes[2]} {
		if !store.Consume(strings.ToLower(code)) {
			t.Fatal("valid code refused")
		}
	}
	if store.Consume(codes[1]) {
		t.Fatal("reused code")
	}
}
