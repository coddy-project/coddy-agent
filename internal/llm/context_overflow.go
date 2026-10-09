package llm

// Recognising a request the provider refused because it does not fit the
// model's context window.
//
// A provider has no error code for this that all of them share. OpenAI names
// it (context_length_exceeded); Anthropic, vLLM, llama.cpp, Gemini and a long
// tail of local backends and gateways describe it in words, in a 400, a 404, a
// 413 or a 422, and one llama.cpp build reports it as a frame inside an
// otherwise healthy stream. The classifier reads the typed code first, then
// the words, and only for the statuses a refusal of this kind arrives in: a
// limit (429) or an outage (5xx) that happens to say "too many tokens" is the
// wrapper's to retry, not a request to shrink.

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go"
)

// contextOverflowCode is the machine-readable code OpenAI (and Azure, and the
// gateways that copy its error object) sets on the refusal.
const contextOverflowCode = "context_length_exceeded"

// contextOverflowNeedles are the phrases, lower-cased, that mean "the prompt
// does not fit the window" in the answer of a refused request. They are phrases
// of the refusal itself and none is a bare "context" or "tokens": a 400 about a
// tool schema, a max_tokens above the model's output cap, a string or an array
// over its limit says a lot about tokens and is not this.
var contextOverflowNeedles = []string{
	contextOverflowCode,
	// LiteLLM's name for it, in front of whatever the upstream said.
	"contextwindowexceeded",
	// OpenAI, Azure, OpenRouter, DeepSeek, Mistral, vLLM: "This model's
	// maximum context length is N tokens. However, ...".
	"maximum context length",
	// OpenAI's newer wording: "Your input exceeds the context window of this
	// model", and the other tenses of it.
	"exceeds the context window",
	"exceeded the context window",
	"context window exceed",
	// llama.cpp: "the request exceeds the available context size", and the
	// type its newer builds put on the same answer.
	"exceeds the available context size",
	"exceed_context_size_error",
	// vLLM: "The decoder prompt (length N) is longer than the maximum model
	// length of M", and the same refusal in other servers.
	"longer than the maximum model length",
	"is longer than the model's context length",
	// Anthropic, llama.cpp, Vertex: "prompt is too long: N tokens > M maximum".
	"prompt is too long",
	// Anthropic, for the input and max_tokens together: "input length and
	// `max_tokens` exceed context limit: N + M > L".
	"exceed context limit",
	// Bedrock: "Input is too long for requested model."
	"input is too long for requested model",
	// xAI: "This model's maximum prompt length is N but the request contains M
	// tokens."
	"maximum prompt length",
	// Gemini: "The input token count (N) exceeds the maximum number of tokens
	// allowed (M)."
	"exceeds the maximum number of tokens allowed",
	// Moonshot: "Your request exceeded model token limit: N".
	"exceeded model token limit",
	// Cohere: "too many tokens: total number of tokens in the prompt exceeds
	// the limit of N".
	"too many tokens",
	// A gateway in front of a local model: "Request too long: N input tokens
	// over the limit of M".
	"input tokens over the limit",
	// LM Studio: "... the model is loaded with context length of only N
	// tokens, which is not enough. Try to load the model with a larger context
	// length, or provide a shorter input".
	"load the model with a larger context length",
	// A local backend that answers 404 for it: "Context limit is N tokens;
	// prompt=M leaves 0 output tokens, below the minimum 16".
	"context limit is",
}

// IsContextOverflow reports whether err is a provider's refusal of a request
// that does not fit the model's context window - the prompt, or the prompt and
// the completion it asked room for, is larger than the window. Such a request
// fails the same way every time it is sent as it is, so the resilient wrapper
// does not retry it; what helps is sending less.
//
// An error without a status (an in-stream frame, a Codex failure event, a
// provider that flattens its answer to text) is judged by its words alone; one
// with a status only when that is 400, 404, 413 or 422, and a typed
// context_length_exceeded code is enough in any status. A cancellation or a
// deadline is never one, whatever it wraps.
func IsContextOverflow(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var oai *openai.Error
	if errors.As(err, &oai) && oai.Code == contextOverflowCode {
		return true
	}
	switch UpstreamStatus(err) {
	case 0, 400, 404, 413, 422:
	default:
		return false
	}
	text := strings.ToLower(overflowText(err))
	for _, needle := range contextOverflowNeedles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

// overflowText is what the provider said, for the classifier to read. A typed
// client error contributes the body it kept and nothing else - its own message
// also holds the address the request went to, and the provider label a caller
// wrapped it in is no part of the answer - while any other error is its text.
func overflowText(err error) string {
	var oai *openai.Error
	if errors.As(err, &oai) {
		if raw := oai.RawJSON(); raw != "" {
			return raw
		}
		return oai.Message
	}
	var ant *anthropic.Error
	if errors.As(err, &ant) {
		return ant.RawJSON()
	}
	return err.Error()
}

// OverflowDetail is what a refusal says about the sizes involved, in the
// provider's own tokens. A field is zero when the refusal does not give it.
type OverflowDetail struct {
	// Prompt is the size the provider counted for the request. Where the
	// provider counts the room asked for the completion in the same figure
	// (a request of N tokens against a window of M), it is that figure.
	Prompt int
	// Limit is the largest request the provider accepts.
	Limit int
}

var (
	// The limit, in the wording of each provider. The first group of a pattern
	// is the number.
	overflowLimitPatterns = mustCompileAll(
		`maximum context length is (\d+)`,
		`context limit is (\d+)`,
		`"n_ctx":\s*(\d+)`,
		`tokens > (\d+) maximum`,
		`over the limit of (\d+)`,
	)
	// The size of the request, likewise.
	overflowPromptPatterns = mustCompileAll(
		`prompt=(\d+)`,
		`resulted in (\d+) tokens`,
		`you requested (?:about )?(\d+) tokens`,
		`your request has (\d+) input tokens`,
		`prompt is too long: (\d+) tokens`,
		`"n_prompt_tokens":\s*(\d+)`,
		`(\d+) input tokens over the limit`,
	)
	// Anthropic's refusal of an input that fits with no room left for the
	// completion: "N + M > L", the input, the max_tokens it asked, the window.
	overflowSumPattern = regexp.MustCompile(`(\d+) \+ (\d+) > (\d+)`)
)

func mustCompileAll(patterns ...string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(patterns))
	for i, p := range patterns {
		out[i] = regexp.MustCompile(p)
	}
	return out
}

// OverflowDetailOf reads the sizes out of a refusal IsContextOverflow
// recognises: how large the provider found the request and how large it may
// be. It is the zero value for any other error and for a refusal that gives no
// numbers; the first gives the agent nothing to say about sizes, the second
// leaves it with its own estimate.
func OverflowDetailOf(err error) OverflowDetail {
	if !IsContextOverflow(err) {
		return OverflowDetail{}
	}
	text := strings.ToLower(overflowText(err))
	var d OverflowDetail
	if m := overflowSumPattern.FindStringSubmatch(text); m != nil {
		d.Prompt = atoiOrZero(m[1]) + atoiOrZero(m[2])
		d.Limit = atoiOrZero(m[3])
	}
	if d.Limit == 0 {
		d.Limit = firstNumber(overflowLimitPatterns, text)
	}
	if d.Prompt == 0 {
		d.Prompt = firstNumber(overflowPromptPatterns, text)
	}
	return d
}

// firstNumber is the number captured by the first pattern that matches text.
func firstNumber(patterns []*regexp.Regexp, text string) int {
	for _, re := range patterns {
		if m := re.FindStringSubmatch(text); m != nil {
			if n := atoiOrZero(m[1]); n > 0 {
				return n
			}
		}
	}
	return 0
}

func atoiOrZero(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
