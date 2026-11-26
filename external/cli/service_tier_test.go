//go:build cli

package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// openaiTierEndpoint upgrades the shared stub provider to the official OpenAI
// endpoint: the service tier contract admits only codex and api.openai.com/v1,
// so a fixture at any other base URL gets no service_tier option at all.
func openaiTierEndpoint(a *App) {
	a.config().Providers[0].APIBase = "https://api.openai.com/v1"
}

func TestServiceTierConsoleSelectionAndNewSession(t *testing.T) {
	a := newReasoningApp(t)
	openaiTierEndpoint(a)
	a.config().Models = append(a.config().Models, config.ModelEntry{Model: "stub/gpt-6-astra"})
	result, err := a.mgr.HandleSessionSetConfigOption(context.Background(), acp.SessionSetConfigOptionParams{SessionID: a.sessionID, ConfigID: "model", Value: "stub/gpt-6-astra"})
	if err != nil {
		t.Fatal(err)
	}
	a.configOpts = result.ConfigOptions
	a.modelID = "stub/gpt-6-astra"
	a.rememberModel(a.modelID)
	if !a.dispatchSlash("/service-tier") {
		t.Fatal("tier command not handled")
	}
	sel, ok := a.modal.(*selectorModal)
	if !ok || sel.title != "Select service tier" {
		t.Fatalf("modal=%T", a.modal)
	}
	if item := sel.list.SelectedItem(); item == nil || item.Value != "default" || item.Label != "Standard" {
		t.Fatalf("unset selection=%+v, want Standard", item)
	}
	for _, item := range a.serviceTierOption().Options {
		if item.Value == "" || item.Name == "Auto" {
			t.Fatalf("legacy empty choice is still offered: %+v", item)
		}
	}
	if !a.dispatchSlash("/service-tier priority") {
		t.Fatal("tier selection not handled")
	}
	deadline := time.Now().Add(reasoningWait)
	for time.Now().Before(deadline) {
		select {
		case msg := <-a.updatesCh:
			a.applyLoopMessage(msg)
		default:
		}
		opt := a.serviceTierOption()
		if opt != nil && opt.CurrentValue == "priority" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	a.JoinWorkers(5 * time.Second)
	if opt := a.serviceTierOption(); opt == nil || opt.CurrentValue != "priority" {
		t.Fatal("tier not selected")
	}
	if !strings.Contains(a.foot.modelSegment(), "Fast") {
		t.Fatalf("footer=%s", a.foot.modelSegment())
	}
	stored := loadConsoleState(a.config().Paths.Home)
	if stored.LastServiceTier != "priority" {
		t.Fatalf("stored=%+v", stored)
	}
	fresh, err := a.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: a.config().Paths.Home})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.applyInitialModel(context.Background(), fresh); err != nil {
		t.Fatal(err)
	}
	if a.mgr.SessionByID(fresh.SessionID).GetSelectedServiceTier() != "priority" {
		t.Fatal("new session lost preference")
	}
	a.rememberServiceTier("ultrafast")
	fresh.ConfigOptions = []acp.ConfigOption{{ID: "service_tier", Options: []acp.ConfigOptionValue{{Value: ""}, {Value: "default"}}}}
	if err = a.applyInitialServiceTier(context.Background(), fresh); err != nil {
		t.Fatal(err)
	}
	if a.mgr.SessionByID(fresh.SessionID).GetSelectedServiceTier() != "priority" {
		t.Fatal("incompatible preference changed session")
	}
}

func TestConfigSelectorsApplyOnlyTheirSetting(t *testing.T) {
	for _, kind := range []string{"service_tier", "toolset"} {
		t.Run(kind, func(t *testing.T) {
			a := newReasoningApp(t)
			openaiTierEndpoint(a)
			a.config().Models = append(a.config().Models, config.ModelEntry{Model: "stub/gpt-6-astra"})
			a.config().Toolsets = []config.Toolset{{Name: "limited", Tools: []string{"read"}}}
			result, err := a.mgr.HandleSessionSetConfigOption(context.Background(), acp.SessionSetConfigOptionParams{SessionID: a.sessionID, ConfigID: "model", Value: "stub/gpt-6-astra"})
			if err != nil {
				t.Fatal(err)
			}
			a.configOpts = result.ConfigOptions
			st := a.mgr.SessionByID(a.sessionID)
			st.SetSelectedServiceTier("priority")
			st.SetSelectedToolset("default")
			a.serviceTierOption().CurrentValue = "priority"
			a.toolsetOption().CurrentValue = "default"
			open := a.openServiceTierSelector
			current, target := "priority", ""
			if kind == "toolset" {
				open, current, target = a.openToolsetSelector, "default", "limited"
			}
			open()
			sel := a.modal.(*selectorModal)
			if item := sel.list.SelectedItem(); item == nil || item.Value != current {
				t.Fatalf("selected=%+v, want %q", item, current)
			}
			if kind == "toolset" && sel.list.SelectedItem().Description != "Every tool the mode offers" {
				t.Fatal("toolset description lost")
			}
			sel.OnDone(nil)
			if a.modal != nil || st.GetSelectedServiceTier() != "priority" || st.GetSelectedToolset() != "default" {
				t.Fatal("cancel changed settings or left the modal open")
			}
			open()
			sel = a.modal.(*selectorModal)
			sel.OnDone(&tui.SelectItem{Value: target})
			a.JoinWorkers(5 * time.Second)
			wantTier, wantToolset := "priority", "default"
			if kind == "service_tier" {
				wantTier = target
			} else {
				wantToolset = target
			}
			if a.modal != nil || st.GetSelectedServiceTier() != wantTier || st.GetSelectedToolset() != wantToolset {
				t.Fatalf("selection: tier=%q toolset=%q modal=%T", st.GetSelectedServiceTier(), st.GetSelectedToolset(), a.modal)
			}
		})
	}
}
