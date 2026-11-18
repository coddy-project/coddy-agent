package session_test

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestImageGenerationAdmissionUsesTurnSettings(t *testing.T) {
	for _, inline := range []bool{false, true} {
		for _, allowed := range []bool{false, true} {
			t.Run(strings.Join([]string{map[bool]string{true: "inline", false: "armed"}[inline], map[bool]string{true: "allowed", false: "refused"}[allowed]}, "/"), func(t *testing.T) {
				cfg := settingsTestConfig()
				cfg.FindModelEntry("p1/gpt-4o").ImageGeneration = true
				ran := 0
				actual := "nd/qwen3.8-27b"
				base := "p1/gpt-4o"
				if allowed {
					actual, base = base, actual
				}
				runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, sender acp.UpdateSender) (string, error) {
					ran++
					if st.EffectiveModelID(cfg) != actual || !st.GetTurnImageGeneration() {
						t.Errorf("wrong turn model or image flag: %s", st.EffectiveModelID(cfg))
					}
					return string(acp.StopReasonEndTurn), nil
				}
				mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), "", nil)
				created, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
				if err != nil {
					t.Fatal(err)
				}
				st := mgr.SessionByID(created.SessionID)
				st.SetSelectedModelID(base)
				st.ArmTurnOverride(session.SettingReasoning, "default", 2)
				prompt := "draw"
				if inline {
					prompt = "/model " + actual + " --once draw"
				} else {
					st.ArmTurnOverride(session.SettingModel, actual, 1)
				}
				before := st.TurnOverrides()
				_, err = mgr.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{SessionID: created.SessionID, Prompt: promptText(prompt)}, noopSender{}, &session.PromptRunOpts{ImageGeneration: true})
				if allowed {
					if err != nil || ran != 1 {
						t.Fatalf("admission: ran=%d err=%v", ran, err)
					}
					overrides := st.TurnOverrides()
					if len(overrides) != 1 || overrides[0].Setting != session.SettingReasoning || overrides[0].TurnsLeft != 1 {
						t.Fatalf("wrong consumption: %+v", overrides)
					}
				} else {
					if err == nil || ran != 0 {
						t.Fatalf("admission: ran=%d err=%v", ran, err)
					}
					if !reflect.DeepEqual(st.TurnOverrides(), before) {
						t.Fatalf("rejection changed overrides: before=%+v after=%+v", before, st.TurnOverrides())
					}
				}
				if st.GetSelectedModelID() != base || st.TurnSetting(session.SettingModel) != "" || st.GetTurnImageGeneration() {
					t.Fatal("turn settings leaked")
				}
			})
		}
	}
}

func TestImageGenerationRefusedSessionCommandLeavesSettingsUnchanged(t *testing.T) {
	cfg := settingsTestConfig()
	cfg.FindModelEntry("p1/gpt-4o").ImageGeneration = true
	ran := false
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		ran = true
		return string(acp.StopReasonEndTurn), nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), "", nil)
	created, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	st := mgr.SessionByID(created.SessionID)
	st.SetSelectedModelID("p1/gpt-4o")
	st.ArmTurnOverride(session.SettingModel, "p1/gpt-4o", 2)
	before := st.TurnOverrides()
	_, err = mgr.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{SessionID: created.SessionID, Prompt: promptText("/model nd/qwen3.8-27b /plan draw")}, noopSender{}, &session.PromptRunOpts{ImageGeneration: true})
	if err == nil || ran {
		t.Fatalf("refused command ran: ran=%v err=%v", ran, err)
	}
	if st.GetSelectedModelID() != "p1/gpt-4o" || st.GetMode() != "agent" || !reflect.DeepEqual(before, st.TurnOverrides()) {
		t.Fatalf("refused command changed session: model=%s mode=%s overrides=%+v", st.GetSelectedModelID(), st.GetMode(), st.TurnOverrides())
	}
}

func TestImagePreparationFailedAdmissionPreservesOverrides(t *testing.T) {
	for _, rearm := range []bool{false, true} {
		for _, recheck := range []bool{false, true} {
			t.Run(fmt.Sprintf("rearm=%v/recheck=%v", rearm, recheck), func(t *testing.T) {
				cfg := settingsTestConfig()
				cfg.FindModelEntry("p1/gpt-4o").ImageGeneration = true
				cfg.FindModelEntry("p1/gpt-5").ImageGeneration = true
				ran := 0
				runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
					ran++
					return string(acp.StopReasonEndTurn), nil
				}
				mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), "", nil)
				created, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
				if err != nil {
					t.Fatal(err)
				}
				st := mgr.SessionByID(created.SessionID)
				st.SetSelectedModelID("nd/qwen3.8-27b")
				st.ArmTurnOverride(session.SettingModel, "p1/gpt-4o", 2)
				before := st.TurnOverrides()
				opts := &session.PromptRunOpts{ImageGeneration: true, SkipTurnLock: true}
				taken, err := mgr.PrepareImageGenerationPrompt(context.Background(), created.SessionID, promptText("draw"), opts)
				if err != nil {
					t.Fatal(err)
				}
				if rearm {
					st.ArmTurnOverride(session.SettingModel, "p1/gpt-5", 3)
					before = st.TurnOverrides()
				}
				hook := func(id string) { mgr.MarkDeletingForTest([]string{id}, true) }
				if recheck {
					mgr.SetTurnAdmissionHookForTest(hook)
				} else {
					mgr.SetTurnEntryHookForTest(hook)
				}
				_, err = mgr.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{SessionID: created.SessionID, Prompt: taken.Prompt}, noopSender{}, opts)
				if err == nil || ran != 0 {
					t.Fatalf("expected admission failure: ran=%d err=%v", ran, err)
				}
				if !reflect.DeepEqual(before, st.TurnOverrides()) {
					t.Fatalf("failed admission changed detour: before=%+v after=%+v", before, st.TurnOverrides())
				}
				if st.TurnSetting(session.SettingModel) != "" {
					t.Fatal("prepared settings remained active")
				}
				st.ResumeTurnSettings()
				if st.EffectiveModelID(cfg) != "nd/qwen3.8-27b" {
					t.Fatal("failed preparation changed the previous turn's settings")
				}
				st.EndTurnSettings()
				mgr.SetTurnEntryHookForTest(nil)
				mgr.SetTurnAdmissionHookForTest(nil)
				mgr.MarkDeletingForTest([]string{created.SessionID}, false)
				_, err = mgr.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{SessionID: created.SessionID, Prompt: taken.Prompt}, noopSender{}, opts)
				if err != nil || ran != 1 {
					t.Fatalf("retry failed: ran=%d err=%v", ran, err)
				}
				after := st.TurnOverrides()
				if len(after) != 1 || after[0].Value != before[0].Value || after[0].TurnsLeft != before[0].TurnsLeft-1 || after[0].Active {
					t.Fatalf("retry consumed wrong detour: before=%+v after=%+v", before, after)
				}
			})
		}
	}
}

func TestImagePreparationDoesNotInstallInlineOverride(t *testing.T) {
	cfg := settingsTestConfig()
	cfg.FindModelEntry("p1/gpt-4o").ImageGeneration = true
	cfg.FindModelEntry("p1/gpt-5").ImageGeneration = true
	mgr := session.NewManager(cfg, noopSender{}, noopRunner, slog.Default(), "", nil)
	created, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	st := mgr.SessionByID(created.SessionID)
	st.ArmTurnOverride(session.SettingModel, "p1/gpt-4o", 2)
	before := st.TurnOverrides()
	opts := &session.PromptRunOpts{ImageGeneration: true, SkipTurnLock: true}
	taken, err := mgr.PrepareImageGenerationPrompt(context.Background(), created.SessionID, promptText("/model p1/gpt-5 --count=2 draw"), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, st.TurnOverrides()) {
		t.Fatalf("preparation installed inline override: %+v", st.TurnOverrides())
	}
	mgr.SetTurnEntryHookForTest(func(id string) { mgr.MarkDeletingForTest([]string{id}, true) })
	_, err = mgr.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{SessionID: created.SessionID, Prompt: taken.Prompt}, noopSender{}, opts)
	if err == nil {
		t.Fatal("expected failed admission")
	}
	if !reflect.DeepEqual(before, st.TurnOverrides()) {
		t.Fatalf("failed admission changed inline/armed overrides: %+v", st.TurnOverrides())
	}
}
