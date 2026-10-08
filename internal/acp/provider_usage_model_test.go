package acp_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// The usage of a model a remote Coddy shares belongs to one alias, not to the
// provider row: the update names it, and every other type leaves it out.
func TestProviderUsageUpdateModelRoundTrip(t *testing.T) {
	in := acp.ProviderUsageUpdate{
		SessionUpdate: "provider_usage",
		Provider:      "remote",
		ProviderType:  "coddy",
		Model:         "coder",
		Windows:       []acp.UsageWindow{{ID: "session", Label: "3h", UsedPercent: 62, ResetInSec: 90}},
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"model":"coder"`) {
		t.Fatalf("the alias is not on the wire under model: %s", raw)
	}
	var back acp.ProviderUsageUpdate
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Model != "coder" || back.Provider != "remote" || back.ProviderType != "coddy" || len(back.Windows) != 1 || back.Windows[0].UsedPercent != 62 {
		t.Fatalf("round trip: %+v", back)
	}
}

func TestProviderUsageUpdateWithoutAModelOmitsTheKey(t *testing.T) {
	raw, err := json.Marshal(acp.ProviderUsageUpdate{SessionUpdate: "provider_usage", Provider: "hub", ProviderType: "neuraldeep"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"model"`) {
		t.Fatalf("a provider that is not coddy must not carry a model key: %s", raw)
	}
	// A frame an older peer wrote has none and decodes to an empty alias.
	var back acp.ProviderUsageUpdate
	if err := json.Unmarshal(raw, &back); err != nil || back.Model != "" {
		t.Fatalf("decode: %+v %v", back, err)
	}
}
