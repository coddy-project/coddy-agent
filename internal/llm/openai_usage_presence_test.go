package llm

import "testing"

func TestOpenAIUsageKnown(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		known bool
	}{
		{`{}`, false}, {`{"usage":null}`, false},
		{`{"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`, true},
		{`{"usage":{"prompt_tokens":8}}`, false},
		{`{"usage":{"prompt_tokens":8,"completion_tokens":null}}`, false},
	} {
		if got := openAIUsageKnown(tc.raw); got != tc.known {
			t.Errorf("%s: got %v want %v", tc.raw, got, tc.known)
		}
	}
}
