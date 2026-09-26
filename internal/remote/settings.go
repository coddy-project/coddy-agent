package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// SetQueueModePreference saves the operator's answer to the first queued send
// as agent.queue_mode in the server's config.yaml. The server takes the whole
// document on PUT, so the one key is changed on the document it answers with,
// kept as raw JSON: a field this console does not know - a server newer than
// the console - goes back exactly as it came instead of being dropped.
func (h *Handler) SetQueueModePreference(mode session.QueueMode) error {
	if !session.ValidQueueMode(mode) {
		return fmt.Errorf("invalid queue mode %q", mode)
	}
	var doc map[string]json.RawMessage
	if err := h.getJSON(h.controlCtx, "/coddy/config", &doc); err != nil {
		return err
	}
	agent := map[string]json.RawMessage{}
	if raw, ok := doc["agent"]; ok && len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &agent); err != nil {
			return fmt.Errorf("read agent config: %w", err)
		}
	}
	value, err := json.Marshal(string(mode))
	if err != nil {
		return err
	}
	agent["queue_mode"] = value
	if doc == nil {
		doc = map[string]json.RawMessage{}
	}
	if doc["agent"], err = json.Marshal(agent); err != nil {
		return err
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	req, err := h.newRequest(h.controlCtx, http.MethodPut, "/coddy/config", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	res, err := h.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	answer, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		return h.remoteError(res, answer)
	}
	return nil
}

// QueueModePreference reads agent.queue_mode from the server's config.
func (h *Handler) QueueModePreference() (session.QueueMode, error) {
	var doc config.ConfigJSON
	if err := h.getJSON(h.controlCtx, "/coddy/config", &doc); err != nil {
		return "", err
	}
	return session.QueueMode(doc.Agent.QueueMode), nil
}

// ApplySessionSettings changes the remote session's settings through PATCH
// /coddy/sessions/{id}, the setter the server's browser uses, and mirrors
// the snapshot it answers with. The notice arrives on the events stream
// (event: session_settings), like a change any other client makes.
//
// The server pins a session on its first prompt. Until then there is nothing
// to patch: the change is mirrored here and held, and it rides in at the
// start of that prompt as the commands that ask for it (FormatSettingsCommands),
// which the server takes before the turn like any typed command.
func (h *Handler) ApplySessionSettings(ctx context.Context, sessionID string, ch session.SettingsChange) (acp.SessionSettings, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return acp.SessionSettings{}, fmt.Errorf("remote: settings need a session id")
	}
	body := map[string]interface{}{}
	if ch.Model != nil {
		body["selectedModelId"] = *ch.Model
	}
	if ch.Reasoning != nil {
		body["selectedReasoning"] = *ch.Reasoning
	}
	if ch.Mode != nil {
		body["mode"] = *ch.Mode
	}
	if ch.PermissionMode != nil {
		body["permissionMode"] = *ch.PermissionMode
	}
	if ch.Turns > 0 {
		body["turns"] = ch.Turns
	}
	var out struct {
		Settings acp.SessionSettings `json:"settings"`
	}
	err := h.patchJSONInto(ctx, "/coddy/sessions/"+url.PathEscape(sid), body, &out)
	st := h.session(sid)
	if isNotFound(err) {
		h.mu.Lock()
		st.pendingSettings = append(st.pendingSettings, ch)
		if ch.Turns == 0 {
			if ch.Model != nil {
				st.modelID = *ch.Model
			}
			if ch.Reasoning != nil {
				st.reasoning = *ch.Reasoning
			}
			if ch.Mode != nil {
				st.mode = *ch.Mode
			}
			if ch.PermissionMode != nil {
				st.permissionMode = *ch.PermissionMode
			}
		}
		snap := h.localSettingsLocked(sid, st)
		h.mu.Unlock()
		if sender := h.currentSender(); sender != nil {
			_ = sender.SendSessionUpdate(sid, acp.SessionSettingsUpdate{
				SessionUpdate: acp.UpdateTypeSessionSettings,
				Settings:      snap,
				Notice:        session.SettingsChangeNotice(ch) + " (from the first message)",
				Source:        "remote",
			})
		}
		return snap, nil
	}
	if err != nil {
		return acp.SessionSettings{}, err
	}
	h.mirrorSettings(sid, out.Settings)
	return out.Settings, nil
}

// EnqueueFollowUp queues what the operator typed during a remote turn. The
// server takes settings commands off its start (they apply at once) and
// answers without a message when nothing was left to queue.
func (h *Handler) EnqueueFollowUp(_ context.Context, sessionID, text, _ string) (session.QueuedMessage, bool, string, error) {
	return h.EnqueueFollowUpWithMode(context.Background(), sessionID, text, "", session.QueueModeSteer, nil)
}

func (h *Handler) EnqueueFollowUpWithMode(_ context.Context, sessionID, text, _ string, mode session.QueueMode, parts []acp.ImagePartRef) (session.QueuedMessage, bool, string, error) {
	fence := h.queueRequestFence(sessionID)
	var out struct {
		queueResponse
		Notice string `json:"notice"`
	}
	if err := h.postJSON(h.controlCtx, queuePath(sessionID), map[string]interface{}{"text": text, "mode": mode, "inline_files": parts}, &out); err != nil {
		return session.QueuedMessage{}, false, "", translateQueueError(err)
	}
	h.publishQueue(sessionID, out.queueResponse, fence)
	if out.Message == nil {
		return session.QueuedMessage{}, false, out.Notice, nil
	}
	return *out.Message, true, out.Notice, nil
}

// takePendingSettings returns and forgets the settings held for a session
// the server has not created yet, as the command lines that ask for them.
func (h *Handler) takePendingSettings(st *sessionState) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(st.pendingSettings) == 0 {
		return ""
	}
	text := session.FormatSettingsCommands(st.pendingSettings)
	st.pendingSettings = nil
	return text
}

// mirrorSettings adopts a snapshot of the server's: the values the next
// prompt sends and the footer shows.
func (h *Handler) mirrorSettings(sessionID string, snap acp.SessionSettings) {
	st := h.session(sessionID)
	h.mu.Lock()
	defer h.mu.Unlock()
	if snap.Version != 0 && snap.Version < st.settingsVersion {
		return
	}
	st.settingsVersion = snap.Version
	if snap.Model != "" {
		st.modelID = snap.Model
	}
	st.reasoning = snap.Reasoning
	if snap.Mode != "" {
		st.mode = snap.Mode
	}
	st.permissionMode = snap.PermissionMode
	if snap.ConfiguredPermissionMode != "" {
		h.serverPermission = snap.ConfiguredPermissionMode
	}
	// Every snapshot names the choices of its model, none included: an
	// empty list clears what an older one offered.
	st.reasoningChoices = append([]string(nil), snap.ReasoningChoices...)
	st.choicesModel = snap.Model
	st.overrides = append([]acp.TurnOverride(nil), snap.Overrides...)
}

// SessionSettings returns what this client holds of a session's settings:
// the server's last snapshot it adopted - a loaded session's, a change's -
// with what it holds for a session the server has not created yet. The
// console shows it on entering the session.
func (h *Handler) SessionSettings(sessionID string) (acp.SessionSettings, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return acp.SessionSettings{}, fmt.Errorf("remote: settings need a session id")
	}
	st := h.session(sid)
	h.mu.Lock()
	defer h.mu.Unlock()
	snap := h.localSettingsLocked(sid, st)
	snap.Version = st.settingsVersion
	snap.ConfiguredPermissionMode = h.serverPermission
	if snap.PermissionMode == "" {
		snap.PermissionMode = h.serverPermission
	}
	if st.choicesModel == snap.Model && len(st.reasoningChoices) > 0 {
		snap.ReasoningChoices = append([]string(nil), st.reasoningChoices...)
	}
	snap.Overrides = append(append([]acp.TurnOverride(nil), st.overrides...), snap.Overrides...)
	return snap, nil
}

// localSettingsLocked builds a snapshot from what this client holds, for a
// session the server does not have yet. h.mu is held.
func (h *Handler) localSettingsLocked(sessionID string, st *sessionState) acp.SessionSettings {
	model := st.modelID
	if model == "" {
		model = h.defModel
	}
	snap := acp.SessionSettings{
		SessionID:      sessionID,
		Model:          model,
		Reasoning:      st.reasoning,
		Mode:           st.mode,
		PermissionMode: st.permissionMode,
	}
	for _, m := range h.models {
		if m.ID == model {
			snap.ReasoningChoices = append([]string(nil), m.ReasoningLevels...)
		}
	}
	for _, ch := range st.pendingSettings {
		if ch.Turns == 0 {
			continue
		}
		for setting, value := range map[string]*string{
			session.SettingModel: ch.Model, session.SettingReasoning: ch.Reasoning,
			session.SettingMode: ch.Mode, session.SettingPermissionMode: ch.PermissionMode,
		} {
			if value != nil {
				snap.Overrides = append(snap.Overrides, acp.TurnOverride{Setting: setting, Value: *value, TurnsLeft: ch.Turns})
			}
		}
	}
	return snap
}

// applySettingsEvent forwards a server's event: session_settings to the
// console, after mirroring it.
func (h *Handler) applySettingsEvent(data string) {
	var payload struct {
		SessionID string              `json:"sessionId"`
		Settings  acp.SessionSettings `json:"settings"`
		Notice    string              `json:"notice"`
		Source    string              `json:"source"`
	}
	if json.Unmarshal([]byte(data), &payload) != nil || payload.SessionID == "" {
		return
	}
	h.mirrorSettings(payload.SessionID, payload.Settings)
	if sender := h.currentSender(); sender != nil {
		_ = sender.SendSessionUpdate(payload.SessionID, acp.SessionSettingsUpdate{
			SessionUpdate: acp.UpdateTypeSessionSettings,
			Settings:      payload.Settings,
			Notice:        payload.Notice,
			Source:        payload.Source,
		})
	}
}

// patchJSONInto is patchJSON decoding the answer into out.
func (h *Handler) patchJSONInto(ctx context.Context, path string, in interface{}, out interface{}) error {
	ctx, cancel := context.WithTimeout(ctx, restTimeout)
	defer cancel()
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := h.newRequest(ctx, http.MethodPatch, path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	res, err := h.hc.Do(req)
	if err != nil {
		return fmt.Errorf("remote coddy %s: %w", h.opts.BaseURL, err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		return h.remoteError(res, body)
	}
	if out == nil || len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}
