package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// An OpenAI-compatible upstream may serve GET {api_base}/limits in the
// NeuralDeep hub's schema-1 shape: a real hub, or another Coddy deployment
// fronting a metered account. Coddy's own /v1/limits also carries a coddy
// member with the provider usage update its own surfaces show; it arrives
// here as raw JSON and is decoded one layer up, where the acp types live.

// OpenAILimitsPayload is the decoded answer of GET {api_base}/limits.
type OpenAILimitsPayload struct {
	// Neural is the schema-1 body, kept whenever it validates; it is the
	// fallback for upstreams that carry no coddy member.
	Neural *NeuralDeepUsage
	// Coddy is the raw coddy member, the upstream's provider usage update
	// in Coddy's own shape; empty when the answer does not carry one.
	Coddy json.RawMessage
}

// FetchOpenAILimits reads GET {apiBase}/limits with the row's key. An empty
// key sends no Authorization header at all, which is what an upstream
// without authentication expects.
func FetchOpenAILimits(ctx context.Context, apiBase, key string, proxyOrClient any) (*OpenAILimitsPayload, error) {
	client, err := providerHTTPClientArg(proxyOrClient)
	if err != nil {
		return nil, &ProviderUsageError{Kind: ProviderUsageUnavailable, Detail: redactNeuralDeepSecrets(err.Error())}
	}
	u := strings.TrimRight(strings.TrimSpace(apiBase), "/") + "/limits"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, &ProviderUsageError{Kind: ProviderUsageUnavailable, Detail: redactNeuralDeepSecrets(err.Error())}
	}
	req.Header.Set("Accept", "application/json")
	if strings.TrimSpace(key) != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	reqCtx, cancel := context.WithTimeout(ctx, neuralDeepUsageRequestTimeout)
	defer cancel()
	req = req.WithContext(reqCtx)
	resp, err := client.Do(req)
	if err != nil {
		return nil, &ProviderUsageError{Kind: ProviderUsageUnavailable, Detail: redactNeuralDeepSecrets(err.Error())}
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return nil, &ProviderUsageError{Status: resp.StatusCode, Kind: ProviderUsageUnavailable, Detail: redactNeuralDeepSecrets(err.Error())}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		kind := ProviderUsageUnavailable
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			kind = ProviderUsageUnauthorized
		case http.StatusForbidden:
			kind = NeuralDeepUsageForbidden
		}
		detail := neuralDeepUsageDetail(body)
		if detail == "" {
			detail = strings.TrimSpace(resp.Status)
		}
		return nil, &ProviderUsageError{
			Status:     resp.StatusCode,
			Kind:       kind,
			RetryAfter: parseUsageRetryAfter(resp.Header.Get("Retry-After")),
			Detail:     detail,
		}
	}
	var env struct {
		NeuralDeepUsage
		Coddy json.RawMessage `json:"coddy"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, &ProviderUsageError{Kind: ProviderUsageInvalid, Detail: redactNeuralDeepSecrets(err.Error())}
	}
	if len(env.Coddy) == 0 {
		// Without a coddy member the schema-1 body is all the client gets,
		// so it must validate whole.
		if env.Schema != neuralDeepUsageSchema {
			return nil, &ProviderUsageError{Kind: ProviderUsageInvalid, Detail: fmt.Sprintf("schema %d, want %d", env.Schema, neuralDeepUsageSchema)}
		}
		if err := env.validate(); err != nil {
			return nil, &ProviderUsageError{Kind: ProviderUsageInvalid, Detail: err.Error()}
		}
	}
	payload := &OpenAILimitsPayload{Coddy: env.Coddy}
	if u := env.NeuralDeepUsage; u.Schema == neuralDeepUsageSchema && u.validate() == nil {
		payload.Neural = &u
	}
	if payload.Neural == nil && len(payload.Coddy) == 0 {
		return nil, &ProviderUsageError{Kind: ProviderUsageInvalid, Detail: "the limits answer carries no usable usage"}
	}
	return payload, nil
}

// OpenAIUsageForProvider reads the usage of an openai provider row: GET
// {api_base}/limits with the row's effective key, through the row's proxy.
// An empty api_base resolves to the same official OpenAI endpoint as every
// other request class; its 404 then marks the source unsupported once.
func OpenAIUsageForProvider(ctx context.Context, provider config.ProviderConfig) (*OpenAILimitsPayload, error) {
	key, helperErr := provider.EffectiveAPIKeyContextErr(ctx)
	if helperErr != nil {
		return nil, &ProviderUsageError{Kind: ProviderUsageUnavailable, Detail: "the credential helper could not run: " + redactNeuralDeepSecrets(helperErr.Error())}
	}
	return FetchOpenAILimits(ctx, ProviderEndpoint(provider.Type, provider.APIBase), key, provider.Proxy)
}

// OpenAIUsageFingerprint identifies the upstream an openai provider row
// reads its limits from: the endpoint, the proxy and the credential source
// (never the command's output: the fingerprint must stay free of side
// effects).
func OpenAIUsageFingerprint(provider config.ProviderConfig) string {
	h := sha256.New()
	writeField := func(s string) {
		_, _ = h.Write([]byte(strconv.Itoa(len(s))))
		_, _ = h.Write([]byte(s))
	}
	writeField(strings.TrimRight(strings.TrimSpace(provider.APIBase), "/"))
	writeField(strings.TrimSpace(provider.Proxy))
	writeField("key:" + strings.TrimSpace(provider.APIKey))
	writeField("cmd:" + strings.TrimSpace(provider.APIKeyCommand))
	if env := config.ProviderAPIKeyEnvVarName(provider.Name); env != "" {
		writeField("env:" + strings.TrimSpace(os.Getenv(env)))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
