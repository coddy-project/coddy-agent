package llm

// The client boundary inside the package: helpers that keep a *http.Client
// parameter (the unexported transports behind the exported proxy-setting
// surface) must refuse a nil client with a descriptive error instead of
// silently falling back to http.DefaultClient or a bare &http.Client{} -
// the exact escape hatch that lets provider requests slip past
// providers[].proxy. The nil path must not exist.

import (
	"context"
	"strings"
	"testing"
)

func wantClientError(t *testing.T, what string, err error) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: nil client accepted, want the required-client error", what)
		return
	}
	if !strings.Contains(err.Error(), "http client") && !strings.Contains(err.Error(), "proxy") {
		t.Errorf("%s: error %q does not name the missing client", what, err)
	}
}

func TestProviderHelpersRefuseNilClient(t *testing.T) {
	ctx := context.Background()

	_, err := devinUnary(ctx, nil, "http://127.0.0.1:1/x", nil)
	wantClientError(t, "devinUnary", err)

	_, _, err = devinPostJSON(ctx, nil, "http://127.0.0.1:1/x", nil)
	wantClientError(t, "devinPostJSON", err)

	_, _, err = exchangeDevinCode(ctx, nil, "code", "verifier")
	wantClientError(t, "exchangeDevinCode", err)

	var out struct{}
	wantClientError(t, "neuralDeepGetJSON",
		neuralDeepGetJSON(ctx, "http://127.0.0.1:1", "/x", "k", nil, &out))

	_, err = pollCodexDeviceToken(ctx, "http://127.0.0.1:1", nil, CodexDeviceLogin{})
	wantClientError(t, "pollCodexDeviceToken", err)

	_, err = exchangeCodexDeviceToken(ctx, "http://127.0.0.1:1", nil, codexDeviceToken{})
	wantClientError(t, "exchangeCodexDeviceToken", err)
}

func TestCodexAuthSourceRequiresClient(t *testing.T) {
	_, err := newCodexAuthSource("", nil).refresh(context.Background(), "refresh-token")
	wantClientError(t, "newCodexAuthSource", err)
	_, err = newManagedCodexAuthSource("", false, nil).refresh(context.Background(), "refresh-token")
	wantClientError(t, "newManagedCodexAuthSource", err)
}
