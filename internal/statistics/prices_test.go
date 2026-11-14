package statistics

import (
	"github.com/EvilFreelancer/coddy-agent/internal/llm/models"
	"math"
	"testing"
	"time"
)

func TestCatalogEstimateAndCustomTariffs(t *testing.T) {
	p, ok := models.LookupPrice("gpt-5")
	if !ok {
		t.Fatal("embedded catalog has no gpt-5 price")
	}
	event := Event{ID: "price", At: time.Now(), Kind: "call", Provider: "custom-account", Model: "gpt-5", Input: Int(1000000), Output: Int(100000), Cached: Int(500000)}
	store := New(t.TempDir())
	if err := store.Append(event); err != nil {
		t.Fatal(err)
	}
	events, err := store.Events()
	if err != nil {
		t.Fatal(err)
	}
	if events[0].CostUSD != nil {
		t.Fatal("catalog price was frozen into the journal")
	}
	ApplyPrice(&event, nil)
	cached := p.Input
	if p.Cached != nil {
		cached = *p.Cached
	}
	want := p.Input*0.5 + cached*0.5 + p.Output*0.1
	if event.CostUSD == nil || math.Abs(*event.CostUSD-want) > 1e-9 {
		t.Fatalf("catalog estimate = %v, want %v", event.CostUSD, want)
	}
	event.CostUSD = nil
	ApplyPrice(&event, map[string]Price{"gpt-5": {Input: 99}, "custom-account/gpt-5": {}})
	if event.CostUSD == nil || *event.CostUSD != 0 {
		t.Fatal("explicit free account tariff did not win")
	}
	unknown := Event{Model: "unknown-model", Input: Int(1), Output: Int(1)}
	ApplyPrice(&unknown, nil)
	if unknown.CostUSD != nil {
		t.Fatal("unknown tariff became free")
	}
}
