//go:build http

package httpserver

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestDirectAssistantMessageKeepsReasoning(t *testing.T) {
	response := &llm.Response{Content: "answer", Reasoning: " exact reasoning\n", ReasoningSignature: "signature", ReasoningTokens: 7}
	message := directAssistantMessage(response, "p/model")
	if message.Reasoning != response.Reasoning || message.ReasoningSignature != response.ReasoningSignature || message.ReasoningTokens != response.ReasoningTokens {
		t.Fatalf("direct history lost reasoning: %+v", message)
	}
	if message.Model != "p/model" || message.Content != "answer" {
		t.Fatalf("unexpected direct history: %+v", message)
	}
}
