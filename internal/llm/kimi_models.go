package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// KimiModel carries only metadata advertised by the selected catalog. Nil
// ImageInput means that the catalog did not declare image support.
type KimiModel struct {
	ID               string
	DisplayName      string
	ContextWindow    int
	ImageInput       *bool
	Protocol         string
	ThinkingType     string
	ReasoningLevels  []string
	DefaultReasoning string
}

// ListKimiModels discovers models without allowing the caller to choose the
// credential destination. Empty mode and region mean subscription and global.
func ListKimiModels(ctx context.Context, mode, region, credential, proxy string) ([]KimiModel, error) {
	endpoint, err := kimiModelsEndpoint(mode, region)
	if err != nil {
		return nil, err
	}
	client, err := HTTPClientForProviderProxy(proxy)
	if err != nil {
		return nil, err
	}
	client.Timeout = 30 * time.Second
	return fetchKimiModels(ctx, mode, credential, endpoint, client)
}

func kimiModelsEndpoint(mode, region string) (string, error) {
	if mode == "" {
		mode = "subscription"
	}
	if region == "" {
		region = "global"
	}
	switch mode {
	case "subscription":
		switch region {
		case "global":
			return "https://api.kimi.ai/coding/v1/models", nil
		case "china":
			return "https://api.kimi.com/coding/v1/models", nil
		}
	case "platform":
		switch region {
		case "global":
			return "https://api.moonshot.ai/v1/models", nil
		case "china":
			return "https://api.moonshot.cn/v1/models", nil
		}
	default:
		return "", errors.New("invalid Kimi mode")
	}
	return "", errors.New("invalid Kimi region")
}

type kimiModelsItem struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Limit       struct {
		Context int `json:"context"`
	} `json:"limit"`
	ContextLength int     `json:"context_length"`
	Protocol      *string `json:"protocol"`
	Modalities    *struct {
		Input []string `json:"input"`
	} `json:"modalities"`
	Image     *bool `json:"supports_image_in"`
	Reasoning *struct {
		Type   string `json:"type"`
		Effort *struct {
			Valid   []string `json:"valid"`
			Default string   `json:"default"`
		} `json:"effort"`
	} `json:"reasoning"`
	ThinkingType string `json:"supports_thinking_type"`
	ThinkEfforts *struct {
		Support bool     `json:"support"`
		Valid   []string `json:"valid_efforts"`
		Default string   `json:"default_effort"`
	} `json:"think_efforts"`
}

func fetchKimiModels(ctx context.Context, mode, credential, endpoint string, client *http.Client) ([]KimiModel, error) {
	if client == nil {
		return nil, errors.New("kimi models: HTTP client is required")
	}
	if strings.TrimSpace(credential) == "" {
		return nil, errors.New("kimi: credential is required")
	}
	if mode != "" && mode != "subscription" && mode != "platform" {
		return nil, errors.New("invalid Kimi mode")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, errors.New("invalid Kimi models request")
	}
	if err := setKimiIdentityHeaders(req, ""); err != nil {
		return nil, errors.New("kimi models: cannot set identity headers")
	}
	req.Header.Set("Authorization", "Bearer "+credential)
	req.Header.Set("Accept", "application/json")
	safeClient := *client
	safeClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := safeClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("kimi models: request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, &httpStatusError{label: "Kimi models request failed", status: resp.StatusCode, header: resp.Header.Clone()}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20+1))
	if err != nil {
		return nil, errors.New("kimi models: response read failed")
	}
	if len(body) > 4<<20 {
		return nil, errors.New("kimi models: response too large")
	}
	var payload struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &payload) != nil || len(payload.Data) == 0 || string(payload.Data) == "null" {
		return nil, errors.New("invalid Kimi models response")
	}
	var items []kimiModelsItem
	if json.Unmarshal(payload.Data, &items) != nil {
		return nil, errors.New("invalid Kimi models response")
	}
	out := make([]KimiModel, 0, len(items))
	seen := make(map[string]bool)
	for _, item := range items {
		if item.ID == "" {
			continue
		}
		if seen[item.ID] {
			return nil, errors.New("duplicate Kimi model ID")
		}
		seen[item.ID] = true
		contextLength := item.Limit.Context
		if mode == "platform" {
			contextLength = item.ContextLength
		}
		if contextLength <= 0 {
			return nil, errors.New("kimi model has invalid context window")
		}
		protocol := "kimi"
		if item.Protocol != nil {
			protocol = *item.Protocol
			if protocol == "response" {
				protocol = "openai_responses"
			}
		}
		m := KimiModel{ID: item.ID, DisplayName: item.DisplayName, ContextWindow: contextLength, Protocol: protocol}
		if mode == "platform" {
			m.ImageInput = item.Image
			m.ThinkingType = item.ThinkingType
			if e := item.ThinkEfforts; e != nil && e.Support {
				m.ReasoningLevels = e.Valid
				m.DefaultReasoning = e.Default
			}
		} else {
			if item.Modalities != nil && item.Modalities.Input != nil {
				b := false
				for _, v := range item.Modalities.Input {
					if v == "image" {
						b = true
					}
				}
				m.ImageInput = &b
			}
			if item.Reasoning == nil {
				m.ThinkingType = "no"
			} else {
				m.ThinkingType = item.Reasoning.Type
				if m.ThinkingType == "none" {
					m.ThinkingType = "no"
				}
				if e := item.Reasoning.Effort; e != nil {
					m.ReasoningLevels = e.Valid
					m.DefaultReasoning = e.Default
				}
			}
		}
		if m.DefaultReasoning != "" && len(m.ReasoningLevels) > 0 {
			found := false
			for _, v := range m.ReasoningLevels {
				if v == m.DefaultReasoning {
					found = true
				}
			}
			if !found {
				m.DefaultReasoning = ""
			}
		}
		out = append(out, m)
	}
	return out, nil
}
