package agent

// The limit a request the provider refused has to fit under, read off the
// refusal (compact_overflow.go). How the loop recovers is in
// context_overflow_recovery_test.go.

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestRecoveryLimitTokens(t *testing.T) {
	for _, tc := range []struct {
		name     string
		window   int
		detail   llm.OverflowDetail
		estimate int
		want     int
	}{
		{name: "no figures: the estimate of the refused request is the ceiling", window: 128000, estimate: 40000, want: 40000},
		{name: "no figures and no estimate: the window", window: 49152, want: 49152},
		{name: "the provider's limit below the window, estimate agrees", window: 128000,
			detail: llm.OverflowDetail{Prompt: 51402, Limit: 49152}, estimate: 51402, want: 49152},
		{name: "the provider counts denser than Coddy: the limit is scaled to Coddy's units", window: 128000,
			detail: llm.OverflowDetail{Prompt: 51402, Limit: 49152}, estimate: 40000, want: 38249},
		{name: "a provider that counts fewer tokens than Coddy is not trusted to", window: 128000,
			detail: llm.OverflowDetail{Prompt: 30000, Limit: 49152}, estimate: 40000, want: 40000},
		{name: "the limit without the prompt size", window: 128000,
			detail: llm.OverflowDetail{Limit: 32768}, estimate: 40000, want: 32768},
		{name: "a limit above the window never raises it", window: 20000,
			detail: llm.OverflowDetail{Prompt: 25000, Limit: 49152}, estimate: 0, want: 20000},
		{name: "nothing known", want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := recoveryLimitTokens(tc.window, tc.detail, tc.estimate); got != tc.want {
				t.Fatalf("limit = %d, want %d", got, tc.want)
			}
		})
	}
}
