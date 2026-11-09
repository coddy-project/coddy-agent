package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
)

// Fallback is confined to HTTP refusals before a stream is handed to a caller.
type serviceTierTransport struct {
	base   http.RoundTripper
	tier   string
	notice func(string)
}

func (t *serviceTierTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != "POST" || req.Body == nil || (!strings.HasSuffix(req.URL.Path, "/responses") && !strings.HasSuffix(req.URL.Path, "/chat/completions")) {
		return t.base.RoundTrip(req)
	}
	raw, err := io.ReadAll(req.Body)
	closeErr := req.Body.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	var body map[string]json.RawMessage
	if err = json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("service tier request: %w", err)
	}
	send := func(tier string) (*http.Response, error) {
		value, _ := json.Marshal(tier)
		body["service_tier"] = value
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		next := req.Clone(req.Context())
		next.Body = io.NopCloser(bytes.NewReader(data))
		next.ContentLength = int64(len(data))
		next.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }
		return t.base.RoundTrip(next)
	}
	res, err := send(t.tier)
	if err != nil || res == nil || res.StatusCode < 400 || t.tier == "default" {
		return res, err
	}
	const limit = 64 * 1024
	prefix, readErr := io.ReadAll(io.LimitReader(res.Body, limit+1))
	res.Body = &tierResponseBody{Reader: io.MultiReader(bytes.NewReader(prefix), res.Body), Closer: res.Body}
	if readErr != nil || len(prefix) > limit || !tierRefusal(res.StatusCode, prefix) {
		return res, nil
	}
	if err = res.Body.Close(); err != nil {
		return nil, err
	}
	if state, ok := req.Context().Value(tierAttemptKey{}).(*atomic.Bool); ok {
		state.Store(true)
	}
	if t.notice != nil {
		t.notice("The requested service tier is unavailable; retrying with Standard. Your saved selection is unchanged.")
	}
	return send("default")
}

type tierResponseBody struct {
	io.Reader
	io.Closer
}

func tierRefusal(status int, raw []byte) bool {
	if status != 400 && status != 403 && status != 422 && status != 429 {
		return false
	}
	var body struct {
		Error struct {
			Param string `json:"param"`
			Code  string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return false
	}
	switch body.Error.Code {
	case "unsupported_service_tier", "service_tier_unavailable", "service_tier_not_available", "service_tier_quota_exhausted":
		return true
	}
	if body.Error.Param != "service_tier" {
		return false
	}
	switch body.Error.Code {
	case "unsupported_value", "unsupported_parameter", "invalid_value", "not_available", "quota_exhausted":
		return true
	}
	return false
}

// Requested and provider-reported tiers are separate; an absent report stays unknown.
type tierRecordingProvider struct {
	inner     Provider
	requested string
}

func (p *tierRecordingProvider) Complete(ctx context.Context, m []Message, t []ToolDefinition) (*Response, error) {
	state := &atomic.Bool{}
	ctx = context.WithValue(ctx, tierAttemptKey{}, state)
	r, e := p.inner.Complete(ctx, m, t)
	if e != nil && state.Load() {
		e = &tierFallbackError{e}
	}
	if r != nil {
		r.RequestedServiceTier = p.requested
	}
	return r, e
}
func (p *tierRecordingProvider) Stream(ctx context.Context, m []Message, t []ToolDefinition, f func(StreamChunk)) (*Response, error) {
	state := &atomic.Bool{}
	ctx = context.WithValue(ctx, tierAttemptKey{}, state)
	r, e := p.inner.Stream(ctx, m, t, f)
	if e != nil && state.Load() {
		e = &tierFallbackError{e}
	}
	if r != nil {
		r.RequestedServiceTier = p.requested
	}
	return r, e
}

type tierAttemptKey struct{}
type tierFallbackError struct{ cause error }

func (e *tierFallbackError) Error() string { return e.cause.Error() }
func (e *tierFallbackError) Unwrap() error { return e.cause }
