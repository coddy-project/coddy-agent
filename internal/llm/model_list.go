package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// ModelEntry is one model advertised by a provider's model-listing endpoint.
type ModelEntry struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// modelListTimeout bounds a single provider model-listing request.
const modelListTimeout = 15 * time.Second

// defaultModelListBaseURL returns the base URL used for model listing when the
// provider config leaves api_base empty.
func defaultModelListBaseURL(providerType string) string {
	switch providerType {
	case "anthropic":
		return "https://api.anthropic.com"
	case "neuraldeep":
		return neuralDeepBaseURL
	default: // openai and openai-compatible
		return "https://api.openai.com/v1"
	}
}

// ListModels fetches the models advertised by a provider's HTTP API. openai,
// neuraldeep, and other OpenAI-compatible providers are queried at {base}/models
// with a Bearer token; anthropic providers at {base}/v1/models with x-api-key +
// anthropic-version. The response is expected in the common {"data":[{"id":...}]}
// shape. Entries are de-duplicated and sorted by id. A non-2xx response returns
// an error so callers can surface auth or connectivity failures (and fall back to
// manual entry).
func ListModels(ctx context.Context, in ProviderInput) ([]ModelEntry, error) {
	var url string
	switch in.Type {
	case "openai", "anthropic", "neuraldeep":
		base := strings.TrimRight(providerBaseURL(in.Type, in.BaseURL), "/")
		if base == "" {
			base = defaultModelListBaseURL(in.Type)
		}
		if in.Type == "anthropic" {
			url = base + "/v1/models"
		} else {
			url = base + "/models"
		}
	default:
		return nil, &UnsupportedProviderError{Provider: in.Type}
	}

	hc, err := HTTPClientForOptionalProxy(in.ProxyURL)
	if err != nil {
		return nil, err
	}
	if hc == nil {
		hc = &http.Client{}
	}

	ctx, cancel := context.WithTimeout(ctx, modelListTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if in.Type == "anthropic" {
		if in.APIKey != "" {
			req.Header.Set("x-api-key", in.APIKey)
		}
		req.Header.Set("anthropic-version", "2023-06-01")
	} else if in.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+in.APIKey)
	}

	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("list models: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	var parsed struct {
		Data []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("list models: decode: %w", err)
	}

	seen := make(map[string]struct{}, len(parsed.Data))
	out := make([]ModelEntry, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		name := strings.TrimSpace(m.Name)
		if name == "" {
			name = strings.TrimSpace(m.DisplayName)
		}
		out = append(out, ModelEntry{ID: id, Name: name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
