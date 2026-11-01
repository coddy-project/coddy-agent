package bgtask

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

func TestAgentUsageIncompletePersistsAndLegacyIsCompatible(t *testing.T) {
	pool := NewWithRunner(Config{}, &stubRunner{})
	dir := t.TempDir()
	pool.SetSessionDir("s", dir)
	t.Cleanup(func() { pool.StopSession("s") })
	h := &stubHandle{release: make(chan struct{})}
	snap, err := pool.Launch(Spec{SessionID: "s", Kind: KindAgent, Agent: &AgentInfo{Name: "reviewer", Model: "demo/model"}}, func(_ string, out io.Writer) (Handle, error) { h.out = out; return h, nil })
	if err != nil {
		t.Fatal(err)
	}
	pool.SetAgentUsageStatus("s", snap.ID, 12, 4, false)
	before, err := pool.Get("s", snap.ID)
	if err != nil {
		t.Fatal(err)
	}
	pool.SetAgentUsageStatus("s", snap.ID, 12, 4, true)
	pool.SetAgentUsageStatus("s", snap.ID, 15, 25, false)
	after, err := pool.Get("s", snap.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.Agent.UsageIncomplete || !after.Agent.UsageIncomplete || after.Agent.OutputTokens != 25 {
		t.Fatalf("before=%+v after=%+v", before.Agent, after.Agent)
	}
	h.finish(0)
	if _, err := pool.Wait(context.Background(), "s", snap.ID, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, row := range LoadPersisted(dir) {
		if row.ID == snap.ID {
			found = true
			if row.Agent == nil || !row.Agent.UsageIncomplete || row.Agent.OutputTokens != 25 {
				t.Fatalf("persisted=%+v", row.Agent)
			}
		}
	}
	if !found {
		t.Fatal("missing persisted row")
	}
	var legacy AgentInfo
	if err := json.Unmarshal([]byte(`{"name":"reviewer","input_tokens":12,"output_tokens":4}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.UsageIncomplete || legacy.InputTokens != 12 {
		t.Fatalf("legacy=%+v", legacy)
	}
}
