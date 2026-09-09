package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
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

// catalogModelIDRe accepts the catalog ids a provider publishes. A login that
// writes a catalog into config.yaml interpolates the id into a UCI config path
// (`models[model=<provider>/<id>]`), so anything outside this safe alphabet is
// skipped rather than staged.
var catalogModelIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// codexModelsClientVersion is the numeric compatibility sentinel accepted by
// the Codex models endpoint and used by Codex source/test builds. It is a Codex
// protocol version, not the independently versioned Coddy application version.
const codexModelsClientVersion = "0.0.0"

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
	if in.Type == "codex" {
		entries, err := fetchCodexCatalog(ctx, in)
		if err != nil {
			return nil, err
		}
		return normalizeCodexModels(entries), nil
	}
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
		if in.Type == "neuraldeep" {
			// Same credential order as completions: explicit key wins, the
			// stored hub login fills in when the key is absent.
			in.APIKey = neuralDeepEffectiveKey(in.APIKey, in.AuthPath)
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

// fetchCodexCatalog returns the raw Codex model catalog: fetched online with
// the signed-in credential when the provider has one, read from the Codex CLI
// cache otherwise.
func fetchCodexCatalog(ctx context.Context, in ProviderInput) ([]codexModelCacheEntry, error) {
	if strings.TrimSpace(in.AuthPath) != "" {
		return fetchCodexCatalogOnline(ctx, in, codexBaseURL())
	}
	return readCodexCatalogCache()
}

// fetchCodexCatalogOnline fetches the model catalog with the same OAuth
// credential used for completions. The base URL is a parameter only for tests;
// fetchCodexCatalog always supplies the fixed official Codex backend.
func fetchCodexCatalogOnline(ctx context.Context, in ProviderInput, baseURL string) ([]codexModelCacheEntry, error) {
	hc, err := HTTPClientForOptionalProxy(in.ProxyURL)
	if err != nil {
		return nil, err
	}
	if hc == nil {
		hc = &http.Client{}
	}
	ctx, cancel := context.WithTimeout(ctx, modelListTimeout)
	defer cancel()
	cred, err := newManagedCodexAuthSource(in.AuthPath, hc).Credential(ctx)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/models?client_version=" + codexModelsClientVersion
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+cred.AccessToken)
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set("originator", "codex_cli_rs")
	if strings.TrimSpace(cred.AccountID) != "" {
		req.Header.Set("chatgpt-account-id", cred.AccountID)
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
		Models []codexModelCacheEntry `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("list models: decode: %w", err)
	}
	return parsed.Models, nil
}

// codexModelCacheEntry is one row of the Codex model catalog, as served by the
// backend and cached by the Codex CLI.
type codexModelCacheEntry struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
	// Visibility is "list" for the models Codex offers in its own picker and
	// "hide" for the internal ones (gpt-reserve, codex-auto-review). An empty
	// value means the catalog does not say, which is not the same as hidden:
	// caches written by older Codex builds carry no visibility at all.
	Visibility string `json:"visibility"`
	// Priority is how Codex itself ranks the catalog, lowest first. It decides
	// which model a fresh sign-in adopts as the default.
	Priority int `json:"priority"`
}

// codexVisibilityHidden marks a catalog row Codex keeps out of its own picker.
const codexVisibilityHidden = "hide"

// visibleCodexModels drops the hidden rows, the unnamed ones, and the
// duplicates, keeping the catalog order for callers that rank it themselves.
func visibleCodexModels(models []codexModelCacheEntry) []codexModelCacheEntry {
	seen := make(map[string]struct{}, len(models))
	out := make([]codexModelCacheEntry, 0, len(models))
	for _, m := range models {
		id := strings.TrimSpace(m.Slug)
		if id == "" || m.Visibility == codexVisibilityHidden {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		m.Slug = id
		out = append(out, m)
	}
	return out
}

// rankedCodexModels orders the offered catalog the way Codex ranks it, lowest
// priority number first, with the id breaking ties so the order is stable for
// a cache that carries no priorities at all.
func rankedCodexModels(models []codexModelCacheEntry) []codexModelCacheEntry {
	out := visibleCodexModels(models)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return out[i].Slug < out[j].Slug
	})
	return out
}

// normalizeCodexModels renders the offered catalog as model entries, sorted by
// id like every other provider's list.
func normalizeCodexModels(models []codexModelCacheEntry) []ModelEntry {
	visible := visibleCodexModels(models)
	out := make([]ModelEntry, 0, len(visible))
	for _, m := range visible {
		out = append(out, ModelEntry{ID: m.Slug, Name: strings.TrimSpace(m.DisplayName)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// readCodexCatalogCache reads the models the Codex CLI advertises from its local
// cache (~/.codex/models_cache.json), which is maintained by `codex`. It is the
// model source when no Coddy-managed credential exists to query the backend with.
func readCodexCatalogCache() ([]codexModelCacheEntry, error) {
	path := codexModelsCachePath()
	if path == "" {
		return nil, fmt.Errorf("list models: could not locate Codex models cache (set CODEX_HOME)")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("list models: read %s (run `codex` once to populate): %w", path, err)
	}
	var cache struct {
		Models []codexModelCacheEntry `json:"models"`
	}
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, fmt.Errorf("list models: parse %s: %w", path, err)
	}
	return cache.Models, nil
}
