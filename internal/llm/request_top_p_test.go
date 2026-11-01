package llm

import (
	"encoding/json"
	"testing"
)

func TestRequestTopPValidation(t *testing.T) {
	for _, tc := range []struct {
		provider string
		value    float64
		wantErr  bool
	}{
		{"openai", 0, false}, {"openai", 1, false}, {"openai", -0.1, true}, {"openai", 1.1, true},
		{"anthropic", 0.7, true}, {"codex", 0.7, true}, {"devin", 0.7, true}, {"acp", 0.7, true}, {"opencode", 0.7, true},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			opts := RequestOptions{TopP: &tc.value}
			err := opts.Validate(tc.provider, "test-model")
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate(%s, %v) = %v", tc.provider, tc.value, err)
			}
			if !tc.wantErr {
				var in ProviderInput
				opts.Apply(&in)
				if in.TopP == nil || *in.TopP != tc.value {
					t.Fatalf("Apply lost top_p: %+v", in.TopP)
				}
			}
		})
	}
}

func TestOpenAITopPSerialized(t *testing.T) {
	for _, value := range []float64{0, 0.7} {
		p := newOpenAIProvider("test-model", "", "", nil, 0, 0, "")
		p.topP = &value
		for _, stream := range []bool{false, true} {
			raw, err := json.Marshal(p.buildParams([]Message{{Role: RoleUser, Content: "hi"}}, nil, stream))
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatal(err)
			}
			if got, ok := body["top_p"].(float64); !ok || got != value {
				t.Fatalf("stream=%v: top_p=%v in %s, want %v", stream, body["top_p"], raw, value)
			}
		}
	}
}
