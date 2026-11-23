package llm

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
)

// KimiCredentialSource returns a current credential for the identity selected
// by the caller. It is invoked immediately before each inference request.
type KimiCredentialSource func(context.Context) (string, error)

// KimiInferenceTransport sends an already encoded request to one fixed route.
// Encoding and decoding the selected wire protocol belong to the caller.
type KimiInferenceTransport struct {
	endpoint           string
	usageEndpoint      string
	source             KimiCredentialSource
	client             *http.Client
	anthropic          bool
	recovery           *KimiSubscriptionSource
	catalogTicket      *kimiCredentialTicket
	validateCredential func(context.Context, string) error
}

// NewKimiInferenceTransport binds one credential source to a region, mode and
// advertised protocol. It does not infer a protocol for a model.
func NewKimiInferenceTransport(mode, region, protocol, proxy string, source KimiCredentialSource) (*KimiInferenceTransport, error) {
	client, err := HTTPClientForProviderProxy(proxy)
	if err != nil {
		return nil, err
	}
	return newKimiInferenceTransport(mode, region, protocol, source, client)
}

func newKimiInferenceTransport(mode, region, protocol string, source KimiCredentialSource, client *http.Client) (*KimiInferenceTransport, error) {
	if source == nil || client == nil {
		return nil, errors.New("kimi: credential source and HTTP client required")
	}
	if mode == "" {
		mode = "subscription"
	}
	if region == "" {
		region = "global"
	}
	var base string
	switch mode {
	case "subscription":
		switch region {
		case "global":
			base = "https://api.kimi.ai/coding/v1"
		case "china":
			base = "https://api.kimi.com/coding/v1"
		}
	case "platform":
		switch region {
		case "global":
			base = "https://api.moonshot.ai/v1"
		case "china":
			base = "https://api.moonshot.cn/v1"
		}
	}
	if base == "" {
		return nil, errors.New("kimi: invalid inference mode or region")
	}
	usageEndpoint := ""
	if mode == "subscription" {
		usageEndpoint = base + "/usages"
	}
	if mode == "platform" && protocol != "kimi" {
		return nil, errors.New("kimi: unsupported platform inference protocol")
	}
	switch protocol {
	case "kimi":
		base += "/chat/completions"
	case "anthropic":
		base += "/messages?beta=true"
	case "openai_responses":
		base += "/responses"
	default:
		return nil, errors.New("kimi: unsupported inference protocol")
	}
	safe := *client
	safe.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &KimiInferenceTransport{endpoint: base, usageEndpoint: usageEndpoint, source: source, client: &safe, anthropic: protocol == "anthropic"}, nil
}

// Do posts a wire-format inference body. The caller must close the response.
func (t *KimiInferenceTransport) Do(ctx context.Context, body io.Reader) (*http.Response, error) {
	return t.do(ctx, body, false, "")
}

func (t *KimiInferenceTransport) do(ctx context.Context, body io.Reader, contextManagement bool, label string) (*http.Response, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, errors.New("kimi: cannot read inference request")
	}
	var ticket kimiCredentialTicket
	if t.catalogTicket != nil {
		ticket = *t.catalogTicket
	} else if t.recovery != nil {
		ticket, err = t.recovery.ticket(ctx)
	} else {
		ticket.token, err = t.source(ctx)
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		for _, safe := range []error{ErrKimiCLILoginRequired, errKimiInvalidDeviceID, errKimiRefreshTemporarilyUnavailable} {
			if errors.Is(err, safe) {
				return nil, safe
			}
		}
		return nil, errors.New("kimi: credential unavailable")
	}
	finish := func(resp *http.Response, requestErr error) (*http.Response, error) {
		if requestErr != nil || label == "" || resp == nil || (resp.StatusCode >= 200 && resp.StatusCode < 300) {
			return resp, requestErr
		}
		return nil, t.statusError(ctx, label, resp, ticket.token)
	}
	resp, err := t.send(ctx, data, ticket, contextManagement)
	if err != nil || resp.StatusCode != http.StatusUnauthorized || t.recovery == nil {
		return finish(resp, err)
	}
	if err := resp.Body.Close(); err != nil {
		return nil, errors.New("kimi: cannot close rejected response")
	}
	ticket, err = t.recovery.recoverTicket(ctx, ticket)
	if err != nil {
		for _, safe := range []error{ErrKimiCLILoginRequired, errKimiInvalidDeviceID, errKimiRefreshTemporarilyUnavailable} {
			if errors.Is(err, safe) {
				return nil, safe
			}
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("kimi: credential recovery failed")
	}
	if t.catalogTicket != nil {
		if t.validateCredential == nil {
			return nil, errors.New("kimi: catalog validation unavailable")
		}
		if err := t.validateCredential(ctx, ticket.token); err != nil {
			return nil, err
		}
	}
	return finish(t.send(ctx, data, ticket, contextManagement))
}

func (t *KimiInferenceTransport) send(ctx context.Context, data []byte, ticket kimiCredentialTicket, contextManagement bool) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("kimi: invalid inference request")
	}
	if err := setKimiIdentityHeaders(req, ticket.deviceID); err != nil {
		return nil, err
	}
	token := ticket.token
	if strings.TrimSpace(token) == "" || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("kimi: invalid credential")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if t.anthropic {
		req.Header.Set("anthropic-version", "2023-06-01")
		beta := "interleaved-thinking-2025-05-14"
		if contextManagement {
			beta = "context-management-2025-06-27"
		}
		req.Header.Set("anthropic-beta", beta)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("kimi: inference request failed")
	}
	// Annotate the response using the request we actually sent, not wire data.
	// The key and value never leave this process or retain a credential snapshot.
	resp.Request = req.WithContext(context.WithValue(req.Context(), kimiResponseSourceKey{}, ticket.kind))
	return resp, nil
}

type kimiResponseSourceKey struct{}

// kimiResponseCredentialSource returns the locally selected source for this
// response. Platform keys and opaque credential functions have no source label.
func kimiResponseCredentialSource(resp *http.Response) string {
	if resp == nil || resp.Request == nil {
		return ""
	}
	kind, _ := resp.Request.Context().Value(kimiResponseSourceKey{}).(KimiSourceKind)
	switch kind {
	case KimiSourceManaged, KimiSourceCLI:
		return string(kind)
	default:
		return ""
	}
}
