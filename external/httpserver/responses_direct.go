//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// handleDirectResponses serves caller-owned history, not a ReAct session. The
// session still owns cancellation, the turn lock and the saved transcript.
func (s *Server) handleDirectResponses(w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	if err := validateResponsesDirectFields(raw); err != nil {
		writeOpenAIValidationError(w, err.Message, err.Param)
		return
	}
	var req responsesDirectRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		writeOpenAIValidationError(w, "invalid Responses request", "")
		return
	}
	req.Model = strings.TrimSpace(req.Model)
	cfg := s.activeCfg()
	if !httpModelListed(cfg, req.Model) {
		writeUnknownModelError(w)
		return
	}
	if err := coerceMetadataJSON(req.Metadata); err != nil {
		writeOpenAIValidationError(w, "invalid metadata", "metadata")
		return
	}
	if completionMetadataForbidden(req.Metadata) {
		writeOpenAIValidationError(w, "metadata.model is not allowed for direct completion", "metadata.model")
		return
	}
	if len(req.Attachments) > 0 {
		writeOpenAIValidationError(w, "attachments are only supported for agent, plan, or ask model", "attachments")
		return
	}
	if err := directACPRefusal(cfg, req.Model); err != nil {
		writeErrorReply(w, err)
		return
	}
	messages, err := responsesInputToMessages(req.Input)
	if err != nil {
		writeOpenAIValidationError(w, err.Error(), "input")
		return
	}
	last := len(messages) - 1
	if messages[last].Role != llm.RoleUser && messages[last].Role != llm.RoleTool {
		writeOpenAIValidationError(w, "input must end with a user message or function_call_output", "input")
		return
	}
	if configuredModelMultimodal(cfg, req.Model) && len(req.InlineFiles) > 0 && messages[last].Role != llm.RoleUser {
		writeOpenAIValidationError(w, "inline_files require a final user message", "inline_files")
		return
	}
	tools, err := responsesToolsToLLM(req.Tools, req.ToolChoice, clientToolLimitsOf(cfg))
	if err != nil {
		writeClientToolError(w, err)
		return
	}
	opts, err := s.responsesGenerationOptions(s.directCapabilityConfig(r.Context(), cfg, req.Model), req.Model, &req)
	if err != nil {
		writeOpenAIValidationError(w, err.Error(), responsesOptionParam(err))
		return
	}
	if req.Instructions != nil && *req.Instructions != "" {
		messages = append([]llm.Message{{Role: llm.RoleSystem, Content: *req.Instructions}}, messages...)
	}
	st, sid, created, err := s.resolveDirectSession(r.Context(), r)
	if err != nil {
		switch {
		case errors.Is(err, errSessionNotFound):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "session not found", "type": "invalid_request_error", "param": "X-Coddy-Session-ID", "code": nil}})
		case errors.Is(err, errInvalidSessionHeader), errors.Is(err, errInvalidOriginHeader):
			writeOpenAIValidationError(w, err.Error(), "X-Coddy-Session-ID")
		default:
			writeErrorReply(w, err)
		}
		return
	}
	if created {
		w.Header().Set("X-Coddy-Session-ID", sid)
	}
	if rejectSubagentTurn(w, st) {
		return
	}
	unlock, ok := s.acquireComposerTurnLock(w, sid, st)
	if !ok {
		return
	}
	defer unlock()
	if configuredModelMultimodal(cfg, req.Model) && len(req.InlineFiles) > 0 {
		parts := inlineFilesToImageParts(req.InlineFiles)
		if err := session.SavePartsToAssets(parts, st.GetPersistedSessionDir()); err != nil {
			s.log.Error("responses inline assets", "error", err)
			writeErrorReply(w, err)
			return
		}
		messages[len(messages)-1].ImageParts = parts
	}
	st.ReplaceMessagesFromClient(messages)
	ctx, cancel := context.WithCancel(r.Context())
	st.SetCancel(cancel)
	defer cancel()
	var tierStream *responsesStream
	opts.ServiceTierNotice = func(text string) {
		if tierStream != nil {
			tierStream.event("response.service_tier_notice", map[string]any{"message": text, "requested_service_tier": req.ServiceTier, "service_tier": "default"})
		}
		st.AppendUILogNotice(session.CountUserTurns(st.GetMessages()), text)
	}
	provider, err := s.directYAMLProvider(ctx, st, req.Model, opts)
	if err != nil {
		writeErrorReply(w, err)
		return
	}
	id, createdAt := responsesItemID("resp_"), time.Now().Unix()
	if req.Stream {
		writeSSEHeaders(w)
		stream := newResponsesStream(w, cancel)
		tierStream = stream
		stream.event("response.created", map[string]any{"response": responsesObject(id, req.Model, createdAt, "in_progress", []any{}, nil, &req, nil, nil)})
		stream.event("response.in_progress", map[string]any{"response": responsesObject(id, req.Model, createdAt, "in_progress", []any{}, nil, &req, nil, nil)})
		stop := stream.keepalive(ctx)
		response, callErr := provider.Stream(ctx, messages, tools, stream.chunk)
		stop()
		if response == nil && callErr == nil {
			callErr = errors.New("provider returned no response")
		}
		if response != nil {
			st.AddMessage(directAssistantMessage(response, req.Model))
		}
		output, streamErr := stream.finish(response, callErr)
		callErr = responsesStreamError(callErr, streamErr)
		status, incomplete := responsesStatus(response)
		var errorObject any
		if callErr != nil {
			status = "failed"
			errorObject = replyForError(callErr).body["error"]
			s.log.Error("responses stream", "error", callErr)
		}
		stream.event("response."+status, map[string]any{"response": responsesTierFields(responsesObject(id, req.Model, createdAt, status, output, responsesUsage(response), &req, errorObject, incomplete), response)})
		return
	}
	response, err := provider.Complete(ctx, messages, tools)
	if err != nil {
		s.log.Error("responses completion", "error", err)
		writeErrorReply(w, err)
		return
	}
	if response == nil {
		writeErrorReply(w, errors.New("provider returned no response"))
		return
	}
	st.AddMessage(directAssistantMessage(response, req.Model))
	status, incomplete := responsesStatus(response)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(responsesTierFields(responsesObject(id, req.Model, createdAt, status, responsesOutputItems(response), responsesUsage(response), &req, nil, incomplete), response)); err != nil {
		s.log.Debug("write Responses answer", "error", err)
	}
}

func responsesOptionParam(err error) string {
	for _, field := range []string{"service_tier", "temperature", "top_p", "reasoning"} {
		if strings.Contains(err.Error(), field) {
			return field
		}
	}
	return "max_output_tokens"
}

func responsesStreamError(callErr, streamErr error) error {
	if streamErr != nil && (callErr == nil || errors.Is(callErr, context.Canceled)) {
		return streamErr
	}
	return callErr
}
