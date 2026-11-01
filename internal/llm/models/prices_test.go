package models

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestPriceSnapshotIsCanonicalAndDistinguishesUnknownModels(t *testing.T) {
	var prices map[string]Price
	if err := json.Unmarshal(priceSnapshot, &prices); err != nil {
		t.Fatal(err)
	}
	if len(prices) < 100 {
		t.Fatalf("only %d embedded prices", len(prices))
	}
	want, err := json.MarshalIndent(prices, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(append(want, '\n'), priceSnapshot) {
		t.Fatal("price snapshot is not canonical")
	}
	if _, ok := LookupPrice("unknown-model"); ok {
		t.Fatal("unknown model has a price")
	}
}

func TestCompactPricesKeepsVendorRatesAndMissingCache(t *testing.T) {
	data := []byte(`{"openai":{"models":{"gpt-test":{"cost":{"input":2,"output":8,"cache_read":0.5}},"free":{"cost":{"input":0,"output":0}},"unknown":{}}},"opencode":{"models":{"gpt-test":{"cost":{"input":9,"output":20}},"gateway":{"cost":{"input":1,"output":3}}}}}`)
	got, err := CompactPrices(data)
	if err != nil {
		t.Fatal(err)
	}
	var prices map[string]Price
	if err := json.Unmarshal(got, &prices); err != nil {
		t.Fatal(err)
	}
	if p := prices["gpt-test"]; p.Input != 2 || p.Output != 8 || p.Cached == nil || *p.Cached != 0.5 {
		t.Fatalf("vendor price: %+v", p)
	}
	if p, ok := prices["free"]; !ok || p.Input != 0 || p.Output != 0 {
		t.Fatalf("explicit free price: %+v, %v", p, ok)
	}
	if _, ok := prices["unknown"]; ok {
		t.Fatal("missing prices became free")
	}
	if p := prices["gateway"]; p.Cached != nil {
		t.Fatal("invented a cache discount")
	}
}
