package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/memory"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const persistBodyWireMax = 12_000

func truncatePersistBodyForWire(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= persistBodyWireMax {
		return s
	}
	return strings.TrimSpace(s[:persistBodyWireMax]) + "\n\n…"
}

func (a *Agent) memoryRowID(turn int) string {
	return fmt.Sprintf("mem-%d", turn)
}

func (a *Agent) sendMemoryPhase(rowID, phase, status string, turn int, durationMs int64, outcome *memory.BeforeTurnOutcome, recallReadPaths []string) {
	upd := acp.MemoryPhaseUpdate{
		SessionUpdate: acp.UpdateTypeMemoryPhase,
		MemoryRowID:   rowID,
		Phase:         phase,
		Status:        status,
		UserTurnIndex: turn,
		DurationMs:    durationMs,
	}
	if status == "completed" && len(recallReadPaths) > 0 {
		upd.RecallReadPaths = recallReadPaths
	}
	if outcome != nil && status == "completed" && outcome.Persist.Saved {
		upd.PersistSaved = true
		upd.PersistRelativePath = outcome.Persist.RelativePath
		upd.PersistTitle = outcome.Persist.Title
		if strings.TrimSpace(outcome.Persist.Body) != "" {
			upd.PersistSavedBody = truncatePersistBodyForWire(outcome.Persist.Body)
		}
	}
	_ = a.server.SendSessionUpdate(a.state.GetID(), upd)
}

func (a *Agent) sendMemoryChunk(rowID, phase, kind, delta string) {
	if delta == "" {
		return
	}
	_ = a.server.SendSessionUpdate(a.state.GetID(), acp.MemoryMessageChunkUpdate{
		SessionUpdate: acp.UpdateTypeMemoryMessageChunk,
		MemoryRowID:   rowID,
		Phase:         phase,
		Kind:          kind,
		Delta:         delta,
	})
}

func (a *Agent) runMemoryBeforeTurn(ctx context.Context, userText string) {
	if !a.cfg.Memory.Enabled {
		return
	}
	sid := a.state.GetID()
	mr := strings.TrimSpace(a.cfg.Memory.Model)
	if mr == "" {
		mr = a.state.EffectiveModelID(a.cfg)
	}
	turn := session.CountUserTurns(a.state.GetMessages())
	rowID := a.memoryRowID(turn)
	sd := strings.TrimSpace(a.state.GetPersistedSessionDir())

	a.log.Info("memory copilot run starting",
		"session_id", sid,
		"memory_row_id", rowID,
		"user_turn_index", turn,
		"model", mr,
	)

	opts := &memory.RunBeforeTurnOptions{
		OnPhaseStart: func() {
			a.sendMemoryPhase(rowID, "memory", "started", turn, 0, nil, nil)
		},
		OnStream: func(kind memory.StreamKind, delta string) {
			if kind != memory.StreamKindText {
				return
			}
			a.sendMemoryChunk(rowID, "memory", "text", delta)
		},
	}

	outcome, dur, err := memory.RunBeforeTurn(ctx, a.log, a.cfg, a.state.GetCWD(), userText, mr, opts)
	if err != nil {
		a.log.Warn("memory copilot run failed",
			"session_id", sid,
			"memory_row_id", rowID,
			"user_turn_index", turn,
			"duration_ms", dur,
			"error", err,
		)
		a.sendMemoryPhase(rowID, "memory", "completed", turn, dur, nil, outcome.ReadPaths)
		return
	}
	a.log.Info("memory copilot run finished",
		"session_id", sid,
		"memory_row_id", rowID,
		"user_turn_index", turn,
		"duration_ms", dur,
		"mode", outcome.Mode,
		"persist_saved", outcome.Persist.Saved,
	)
	a.sendMemoryPhase(rowID, "memory", "completed", turn, dur, &outcome, outcome.ReadPaths)

	ctxText := strings.TrimSpace(outcome.ContextText)
	if ctxText != "" {
		a.state.SetMemoryCopilotBlock(ctxText)
	}

	if sd == "" {
		return
	}
	row := session.MemoryTurnTraceJSON{
		UserTurnIndex:       turn,
		MemoryRowID:         rowID,
		MemoryMode:          outcome.Mode,
		MemoryDurationMs:    dur,
		MemoryContextText:   ctxText,
		RecallReadPaths:     outcome.ReadPaths,
		PersistSaved:        outcome.Persist.Saved,
		PersistScope:        outcome.Persist.Scope,
		PersistRelativePath: outcome.Persist.RelativePath,
		PersistTitle:        outcome.Persist.Title,
		PersistReason:       outcome.Persist.Reason,
		PersistSavedBody:    outcome.Persist.Body,
		PersistFinalText:    strings.TrimSpace(outcome.Persist.RawFinalText),
	}
	_ = session.AppendMemoryTurn(sd, row)
}
