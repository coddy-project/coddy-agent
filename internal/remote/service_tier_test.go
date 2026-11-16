package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

func TestServiceTierRemoteOptionsAndSnapshots(t *testing.T) {
	const id = "sess_tier"
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"remote/astra","default":true,"owned_by":"remote","service_tier_choices":["","default","priority","ultrafast"]}]}`))
	})
	mux.HandleFunc("GET /coddy/sessions/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"messages": []any{}, "settings": acp.SessionSettings{SessionID: id, Version: 1, Model: "remote/astra", ServiceTier: "default", ServiceTierChoices: []string{"", "default", "priority", "ultrafast"}}})
	})
	mux.HandleFunc("PATCH /coddy/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		var patch map[string]string
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			t.Error(err)
		}
		if len(patch) != 1 || patch["selectedServiceTier"] != "priority" {
			t.Errorf("patch=%v", patch)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"settings": acp.SessionSettings{SessionID: id, Version: 2, Model: "remote/astra", ServiceTier: "priority", ServiceTierChoices: []string{"", "default", "priority", "ultrafast"}}})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	h, err := NewHandler(Options{BaseURL: ts.URL})
	if err != nil {
		t.Fatal(err)
	}
	h.SetServer(&collectSender{})
	h.SetPreferredSessionID(id)
	if _, err := h.HandleSessionNew(context.Background(), acp.SessionNewParams{}); err != nil {
		t.Fatal(err)
	}
	if snap, err := h.SessionSettings(id); err != nil || snap.ServiceTier != "default" {
		t.Fatalf("new snapshot=%+v err=%v", snap, err)
	}
	if _, err := h.HandleSessionLoad(context.Background(), acp.SessionLoadParams{SessionID: id}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.HandleSessionResume(context.Background(), acp.SessionResumeParams{SessionID: id}); err != nil {
		t.Fatal(err)
	}
	if snap, err := h.SessionSettings(id); err != nil || snap.ServiceTier != "default" {
		t.Fatalf("restored snapshot=%+v err=%v", snap, err)
	}
	result, err := h.HandleSessionSetConfigOption(context.Background(), acp.SessionSetConfigOptionParams{SessionID: id, ConfigID: "service_tier", Value: "priority"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, opt := range result.ConfigOptions {
		if opt.ID == "service_tier" {
			found = true
			if opt.CurrentValue != "priority" || opt.Category != "model_config" {
				t.Fatalf("option=%+v", opt)
			}
		}
	}
	if !found {
		t.Fatal("no tier option")
	}
	h.mirrorSettings(id, acp.SessionSettings{SessionID: id, Version: 3, Model: "remote/astra", ServiceTier: "", ServiceTierChoices: []string{"", "default", "priority"}})
	snap, err := h.SessionSettings(id)
	if err != nil || snap.ServiceTier != "" {
		t.Fatalf("snapshot=%+v err=%v", snap, err)
	}
	if _, err = h.HandleSessionSetConfigOption(context.Background(), acp.SessionSetConfigOptionParams{SessionID: id, ConfigID: "service_tier", Value: "bad"}); err == nil {
		t.Fatal("accepted unknown tier")
	}
}
