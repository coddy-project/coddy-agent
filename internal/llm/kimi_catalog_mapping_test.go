package llm

import "testing"

func TestKimiProductionCatalogPreservesDefaultAndUnknownProtocols(t *testing.T) {
	entries := kimiCatalogEntries([]KimiModel{
		{ID: "default", Protocol: "kimi"},
		{ID: "other", Protocol: "future-protocol"},
	})
	if len(entries) != 2 || entries[0].ID != "default" || entries[0].KimiProtocol != "kimi" || entries[1].KimiProtocol != "future-protocol" {
		t.Fatalf("unexpected catalog entries: %#v", entries)
	}
}
