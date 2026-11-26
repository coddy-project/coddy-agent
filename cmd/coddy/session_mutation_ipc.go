package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/localipc"
)

const (
	sessionManagementIPCPath        = "/coddy-local-ipc/v1/session-management/"
	sessionManagementRootHeader     = "X-Coddy-Session-Root"
	sessionManagementResponseHeader = "X-Coddy-Session-Management"
	sessionMutationBodyLimit        = 1024 * 1024
	sessionMutationTimeout          = 2 * time.Minute
)

func validMutationSessionID(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, "/\\\x00")
}

func mutateStoredSession(ctx context.Context, home, root, id, method string, patch map[string]any) (json.RawMessage, error) {
	return mutateStoredSessionWithTimeout(ctx, home, root, id, method, patch, sessionMutationTimeout)
}

func mutateStoredSessionWithTimeout(ctx context.Context, home, root, id, method string, patch map[string]any, timeout time.Duration) (json.RawMessage, error) {
	if method != http.MethodPatch && method != http.MethodDelete {
		return nil, fmt.Errorf("session mutation: unsupported method %q", method)
	}
	if !filepath.IsAbs(root) || !validMutationSessionID(id) {
		return nil, fmt.Errorf("session mutation: absolute store root and valid session ID required")
	}
	var body []byte
	var err error
	if method == http.MethodPatch {
		body, err = json.Marshal(patch)
		if err != nil {
			return nil, fmt.Errorf("session mutation: encode patch: %w", err)
		}
	}
	client, err := localipc.Connect(ctx, home)
	if err != nil {
		return nil, fmt.Errorf("session mutation: connect to runtime IPC: %w", err)
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, method, "http://local"+sessionManagementIPCPath+url.PathEscape(id), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("session mutation: create request: %w", err)
	}
	req.Header.Set(sessionManagementRootHeader, root)
	req.Header.Set("Content-Type", "application/json")
	httpClient := client.HTTPClient()
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, unknownMutationOutcome(err)
	}
	defer func() { _ = res.Body.Close() }()
	response, err := io.ReadAll(io.LimitReader(res.Body, sessionMutationBodyLimit+1))
	if err != nil {
		return nil, unknownMutationOutcome(err)
	}
	if len(response) > sessionMutationBodyLimit {
		return nil, unknownMutationOutcome(fmt.Errorf("response exceeds %d bytes", sessionMutationBodyLimit))
	}
	if res.Header.Get(sessionManagementResponseHeader) != "1" {
		return nil, fmt.Errorf("session mutation: runtime does not support guarded mutations; restart it")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("session mutation: runtime returned %s: %s", res.Status, strings.TrimSpace(string(response)))
	}
	return json.RawMessage(response), nil
}

func unknownMutationOutcome(err error) error {
	return fmt.Errorf("session mutation: outcome unknown; check the session before retrying: %w", err)
}
