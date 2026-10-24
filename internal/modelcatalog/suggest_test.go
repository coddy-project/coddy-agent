package modelcatalog

import "testing"

func TestSuggest(t *testing.T) {
	sonnet := Entry{Context: 200000, Output: 64000, Image: true}
	opus := Entry{Context: 1000000, Output: 128000, Image: true}
	gpt5 := Entry{Context: 400000, Input: 272000, Output: 128000, Image: true}
	gpt4o := Entry{Context: 128000, Output: 16384, Image: true}
	small := Entry{Context: 64000, Output: 32000}
	cases := []struct {
		name      string
		entry     Entry
		reported  int
		threshold int
		want      Suggestion
	}{
		// One window shared by prompt and answer: the answer gets the part
		// above the compaction threshold, a fifth of the window at 80.
		{"sonnet, shared window", sonnet, 0, 80, Suggestion{ContextWindow: 200000, MaxOutputTokens: 64000, MaxTokens: 40000, Multimodal: true}},
		{"opus, the output limit is smaller", opus, 0, 80, Suggestion{ContextWindow: 1000000, MaxOutputTokens: 128000, MaxTokens: 128000, Multimodal: true}},
		{"gpt-4o, the output limit is smaller", gpt4o, 0, 80, Suggestion{ContextWindow: 128000, MaxOutputTokens: 16384, MaxTokens: 16384, Multimodal: true}},
		{"a small window caps the answer", small, 0, 80, Suggestion{ContextWindow: 64000, MaxOutputTokens: 32000, MaxTokens: 12800}},
		// Separate budgets: the prompt has limit.input, the answer limit.output.
		{"gpt-5, separate budgets", gpt5, 0, 80, Suggestion{ContextWindow: 272000, MaxOutputTokens: 128000, MaxTokens: 128000, Multimodal: true}},
		{"separate budgets ignore the threshold", gpt5, 0, 95, Suggestion{ContextWindow: 272000, MaxOutputTokens: 128000, MaxTokens: 128000, Multimodal: true}},
		// The threshold the operator configured moves the share.
		{"sonnet at a threshold of 90", sonnet, 0, 90, Suggestion{ContextWindow: 200000, MaxOutputTokens: 64000, MaxTokens: 20000, Multimodal: true}},
		{"no room above a threshold of 100", sonnet, 0, 100, Suggestion{ContextWindow: 200000, MaxOutputTokens: 64000, Multimodal: true}},
		{"a threshold out of range reads as the default", sonnet, 0, 0, Suggestion{ContextWindow: 200000, MaxOutputTokens: 64000, MaxTokens: 40000, Multimodal: true}},
		// The provider's own window is what requests measure against.
		{"a larger reported window", sonnet, 1000000, 80, Suggestion{ContextWindow: 200000, MaxOutputTokens: 64000, MaxTokens: 64000, Multimodal: true}},
		{"a smaller reported window", sonnet, 100000, 80, Suggestion{ContextWindow: 200000, MaxOutputTokens: 64000, MaxTokens: 20000, Multimodal: true}},
		{"no output limit, no max_tokens", Entry{Context: 8192}, 0, 80, Suggestion{ContextWindow: 8192}},
	}
	for _, tc := range cases {
		if got := Suggest(tc.entry, tc.reported, tc.threshold); got != tc.want {
			t.Errorf("%s: Suggest = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}
