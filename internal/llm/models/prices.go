package models

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"sync"
)

// Price is the catalog's USD list price per million tokens. A missing cache
// price means the ordinary input price applies, not a free cache read.
type Price struct {
	Input  float64  `json:"input"`
	Output float64  `json:"output"`
	Cached *float64 `json:"cached,omitempty"`
}

const PricesPath = "internal/llm/models/prices.json"

//go:embed prices.json
var priceSnapshot []byte
var priceOnce sync.Once
var priceTable map[string]Price

// LookupPrice reads the embedded catalog, without any network access. Vendor
// list prices are estimates; account-specific prices belong in overrides.
func LookupPrice(id string) (Price, bool) {
	priceOnce.Do(func() {
		if err := json.Unmarshal(priceSnapshot, &priceTable); err != nil {
			panic("models: embedded prices.json: " + err.Error())
		}
	})
	p, ok := priceTable[id]
	if !ok {
		for _, prefix := range []string{"openai/", "anthropic/"} {
			if strings.HasPrefix(id, prefix) {
				p, ok = priceTable[strings.TrimPrefix(id, prefix)]
				break
			}
		}
	}
	return p, ok
}

// CompactPrices follows the limits catalog's supported providers and prefers
// the model vendor over gateways. Ties use provider name, never map order.
func CompactPrices(api []byte) ([]byte, error) {
	var catalog map[string]struct {
		Models map[string]struct {
			Cost *struct {
				Input  *float64 `json:"input"`
				Output *float64 `json:"output"`
				Cached *float64 `json:"cache_read"`
			} `json:"cost"`
		} `json:"models"`
	}
	if err := json.Unmarshal(api, &catalog); err != nil {
		return nil, err
	}
	prices := map[string]Price{}
	owners := map[string]string{}
	valid := func(v float64) bool { return v >= 0 && !math.IsInf(v, 0) && !math.IsNaN(v) }
	for _, provider := range slices.Sorted(maps.Keys(catalog)) {
		vendor, supported := providers[provider]
		if !supported {
			continue
		}
		for id, model := range catalog[provider].Models {
			c := model.Cost
			if strings.TrimSpace(id) == "" || c == nil || c.Input == nil || c.Output == nil || !valid(*c.Input) || !valid(*c.Output) || c.Cached != nil && !valid(*c.Cached) {
				continue
			}
			if old, seen := owners[id]; seen && (!vendor || providers[old]) {
				continue
			}
			prices[id] = Price{Input: *c.Input, Output: *c.Output, Cached: c.Cached}
			owners[id] = provider
		}
	}
	if len(prices) == 0 {
		return nil, fmt.Errorf("models.dev lists no supported model prices")
	}
	b, err := json.MarshalIndent(prices, "", "  ")
	return append(b, '\n'), err
}
