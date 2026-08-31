package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// restTimeout bounds plain REST calls; streaming requests manage their own
// lifetime through the turn context.
const restTimeout = 30 * time.Second

type errorEnvelope struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// apiError carries the HTTP status so callers can branch on 404 vs failure.
type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string { return e.message }

// isNotFound reports whether err is a remote 404.
func isNotFound(err error) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.status == http.StatusNotFound
}

// newRequest builds a request with auth and JSON headers against the remote base.
func (h *Handler) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, h.opts.BaseURL+path, body)
	if err != nil {
		return nil, err
	}
	if h.opts.Token != "" {
		req.Header.Set("Authorization", "Bearer "+h.opts.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json, text/event-stream")
	return req, nil
}

// remoteError turns a non-2xx response into a readable error. The auth gate
// answers with plain-text "unauthorized", everything else uses the JSON
// error envelope.
func (h *Handler) remoteError(res *http.Response, body []byte) error {
	if res.StatusCode == http.StatusUnauthorized {
		return &apiError{status: res.StatusCode, message: fmt.Sprintf("remote coddy %s: unauthorized (check --remote-token or CODDY_REMOTE_TOKEN)", h.opts.BaseURL)}
	}
	var env errorEnvelope
	if json.Unmarshal(body, &env) == nil && strings.TrimSpace(env.Error.Message) != "" {
		return &apiError{status: res.StatusCode, message: "remote coddy: " + env.Error.Message}
	}
	msg := strings.TrimSpace(string(body))
	if len(msg) > 200 {
		msg = msg[:200] + "…"
	}
	if msg == "" {
		msg = res.Status
	}
	return &apiError{status: res.StatusCode, message: "remote coddy: " + msg}
}

// getJSON performs GET path and decodes the response into out.
func (h *Handler) getJSON(ctx context.Context, path string, out interface{}) error {
	ctx, cancel := context.WithTimeout(ctx, restTimeout)
	defer cancel()
	req, err := h.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	res, err := h.hc.Do(req)
	if err != nil {
		return fmt.Errorf("remote coddy %s: %w", h.opts.BaseURL, err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if res.StatusCode != http.StatusOK {
		return h.remoteError(res, body)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

// postJSON performs POST path with the given body; out may be nil (204/200).
func (h *Handler) postJSON(ctx context.Context, path string, in interface{}, out interface{}) error {
	ctx, cancel := context.WithTimeout(ctx, restTimeout)
	defer cancel()
	var rd io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := h.newRequest(ctx, http.MethodPost, path, rd)
	if err != nil {
		return err
	}
	res, err := h.hc.Do(req)
	if err != nil {
		return fmt.Errorf("remote coddy %s: %w", h.opts.BaseURL, err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNoContent {
		return h.remoteError(res, body)
	}
	if out == nil || len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}

// patchJSON performs PATCH path with the given body.
func (h *Handler) patchJSON(ctx context.Context, path string, in interface{}) error {
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
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNoContent {
		return h.remoteError(res, body)
	}
	return nil
}

// ---- models ----

type modelRow struct {
	ID               string   `json:"id"`
	OwnedBy          string   `json:"owned_by"`
	Multimodal       bool     `json:"multimodal,omitempty"`
	ReasoningLevels  []string `json:"reasoning_levels,omitempty"`
	ReasoningDefault string   `json:"reasoning_default,omitempty"`
}

type modelsResponse struct {
	Data              []modelRow `json:"data"`
	DefaultAgentModel string     `json:"default_agent_model"`
}

// ensureModels fetches the remote model catalog once. It doubles as the
// connectivity and authentication probe.
func (h *Handler) ensureModels(ctx context.Context) error {
	h.mu.Lock()
	loaded := len(h.models) > 0
	h.mu.Unlock()
	if loaded {
		return nil
	}
	var res modelsResponse
	if err := h.getJSON(ctx, "/v1/models", &res); err != nil {
		return err
	}
	var models []remoteModel
	for _, row := range res.Data {
		if row.OwnedBy == "coddy" {
			continue // "agent"/"plan"/"ask" profiles, not selectable backends
		}
		models = append(models, remoteModel{ID: row.ID, OwnedBy: row.OwnedBy, Multimodal: row.Multimodal})
	}
	h.mu.Lock()
	h.models = models
	h.defModel = res.DefaultAgentModel
	if h.defModel == "" && len(models) > 0 {
		h.defModel = models[0].ID
	}
	h.mu.Unlock()
	return nil
}

// ---- sessions ----

type sessionListResponse struct {
	Sessions []struct {
		ID        string `json:"id"`
		Title     string `json:"title,omitempty"`
		UpdatedAt string `json:"updatedAt,omitempty"`
		CWD       string `json:"cwd,omitempty"`
	} `json:"sessions"`
	NextCursor string `json:"nextCursor,omitempty"`
}

func (h *Handler) listSessions(ctx context.Context, cursor string) (*sessionListResponse, error) {
	q := url.Values{"limit": {"100"}}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	var res sessionListResponse
	if err := h.getJSON(ctx, "/coddy/sessions?"+q.Encode(), &res); err != nil {
		return nil, err
	}
	return &res, nil
}

type messageRow struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	Reasoning  string `json:"reasoning,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	ToolCalls  []struct {
		ID       string `json:"id"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls,omitempty"`
	CompactionSummary bool `json:"compaction_summary,omitempty"`
}

type messagesResponse struct {
	Messages        []messageRow `json:"messages"`
	SelectedModelID string       `json:"selectedModelId,omitempty"`
	Model           string       `json:"model,omitempty"`
	Mode            string       `json:"mode,omitempty"`
}

func (h *Handler) sessionMessages(ctx context.Context, id string) (*messagesResponse, error) {
	var res messagesResponse
	if err := h.getJSON(ctx, "/coddy/sessions/"+url.PathEscape(id)+"/messages", &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (h *Handler) cancelSession(ctx context.Context, id string) error {
	return h.postJSON(ctx, "/coddy/sessions/"+url.PathEscape(id)+"/cancel", nil, nil)
}

func (h *Handler) patchSelectedModel(ctx context.Context, id, model string) error {
	return h.patchJSON(ctx, "/coddy/sessions/"+url.PathEscape(id), map[string]interface{}{
		"selectedModelId": model,
	})
}

// ToolCallResult fetches the persisted full output of one tool call (ctrl+o
// expand in the console). Mirrors Manager.ToolCallResult.
func (h *Handler) ToolCallResult(sessionID, toolCallID string) (string, bool) {
	var res struct {
		Result string `json:"result"`
	}
	path := "/coddy/sessions/" + url.PathEscape(sessionID) + "/tool-calls/" + url.PathEscape(toolCallID)
	if err := h.getJSON(context.Background(), path, &res); err != nil {
		return "", false
	}
	if res.Result == "" {
		return "", false
	}
	return res.Result, true
}

// ---- command catalogs ----

type commandItemsResponse struct {
	Items []struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
	} `json:"items"`
}

// commandCatalog merges built-in commands and skill slash commands from the
// remote server into one ACP catalog (best effort; errors are logged).
func (h *Handler) commandCatalog(ctx context.Context, sessionID string) []acp.AvailableCommand {
	seen := map[string]bool{}
	var out []acp.AvailableCommand
	var builtins commandItemsResponse
	if err := h.getJSON(ctx, "/coddy/commands", &builtins); err != nil {
		h.log.Warn("remote commands catalog", "error", err)
	}
	for _, it := range builtins.Items {
		if it.Name == "" || seen[it.Name] {
			continue
		}
		seen[it.Name] = true
		out = append(out, acp.AvailableCommand{Name: it.Name, Description: it.Description})
	}
	q := url.Values{"page": {"1"}, "page_size": {"200"}}
	req, err := h.newRequest(ctx, http.MethodGet, "/coddy/slash-commands?"+q.Encode(), nil)
	if err == nil {
		if sessionID != "" {
			req.Header.Set("X-Coddy-Session-ID", sessionID)
		}
		rctx, cancel := context.WithTimeout(ctx, restTimeout)
		defer cancel()
		req = req.WithContext(rctx)
		if res, derr := h.hc.Do(req); derr == nil {
			body, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
			_ = res.Body.Close()
			var skillsPage commandItemsResponse
			if res.StatusCode == http.StatusOK && json.Unmarshal(body, &skillsPage) == nil {
				for _, it := range skillsPage.Items {
					if it.Name == "" || seen[it.Name] {
						continue
					}
					seen[it.Name] = true
					out = append(out, acp.AvailableCommand{Name: it.Name, Description: it.Description})
				}
			}
		} else {
			h.log.Warn("remote slash-commands catalog", "error", derr)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
