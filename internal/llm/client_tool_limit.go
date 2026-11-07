package llm

// Recognising a provider's refusal of the tools a request offered: more of
// them than it accepts, or one whose parameters schema is too large.
//
// A session offers the model its built-in tools plus every tool of every MCP
// server attached to it, and nothing on the way counts them against what the
// far side takes. The refusal arrives as a plain 400 whose words are the
// server's, so the classifier reads the typed code first - Coddy's own server
// sets one (issue #577), and OpenAI's validator sets array_above_max_length on
// param "tools" - and then the words, and only for the statuses a refusal of
// this kind arrives in: a limit (429) or an outage that happens to mention
// tools is the wrapper's to retry, not a request to send fewer tools.

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/openai/openai-go"
)

const (
	// tooManyToolsCode and toolParametersTooLargeCode are what a Coddy server
	// names the two bounds in error.code.
	tooManyToolsCode           = "too_many_tools"
	toolParametersTooLargeCode = "tool_parameters_too_large"
	// arrayAboveMaxLengthCode is OpenAI's, and says only that some array was
	// too long: it counts next to param "tools" and nowhere else.
	arrayAboveMaxLengthCode = "array_above_max_length"
)

// clientToolLimitNeedles are the phrases, lower-cased, that mean "the tools
// this request offered are more than I take, or one of them is too large" in
// the answer of a refused request. Each is specific to the tool list: a 400
// about a message, a parameter or the context window says nothing of the kind.
var clientToolLimitNeedles = []string{
	// Coddy, before and after the bounds became configurable: "at most 128
	// tools may be offered", and with the count, "..., 177 were offered: ...".
	"tools may be offered",
	// OpenAI's validator and the gateways that copy it: "Invalid 'tools':
	// array too long. Expected an array with maximum length 128, but got an
	// array with length 177 instead."
	"'tools': array too long",
	`"tools": array too long`,
	// A gateway that says it in words.
	"too many tools",
	// Coddy's refusal of one tool's schema: tool "x": parameters exceed N bytes.
	"parameters exceed",
}

// ClientToolLimit is what such a refusal says about the sizes involved. A
// field is zero or empty where the refusal does not give it.
type ClientToolLimit struct {
	// Offered is how many tools the provider counted in the request and Limit
	// the most it takes. For a schema refusal Offered is 0 and Limit is the
	// largest parameters schema in bytes.
	Offered, Limit int
	// Tool names the one tool whose parameters schema was refused. It is empty
	// for a refusal of how many tools were offered, which is what tells the
	// two apart.
	Tool string
}

// SchemaRefusal reports whether the refusal was of one tool's parameters
// rather than of how many tools the request carried.
func (l ClientToolLimit) SchemaRefusal() bool { return l.Tool != "" }

// IsClientToolLimit reports whether err is a provider's refusal of the tools a
// request offered. Such a request fails the same way every time it is sent as
// it is, so the resilient wrapper does not retry it; what helps is offering
// fewer tools, or raising the bound where the operator owns the far side.
//
// A typed code is enough in any status, because no provider sets one of these
// for anything else; the words count only in a 400 or a 422. A cancellation or
// a deadline is never one, whatever it wraps.
func IsClientToolLimit(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var oai *openai.Error
	if errors.As(err, &oai) {
		switch oai.Code {
		case tooManyToolsCode, toolParametersTooLargeCode:
			return true
		case arrayAboveMaxLengthCode:
			if strings.TrimSpace(oai.Param) == "tools" {
				return true
			}
		}
	}
	switch UpstreamStatus(err) {
	case 400, 422:
	default:
		return false
	}
	text := strings.ToLower(clientText(err))
	for _, needle := range clientToolLimitNeedles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

var (
	// The bound on how many tools may be offered, in the wording of each
	// server. The first group of a pattern is the number.
	clientToolLimitPatterns = mustCompileAll(
		`at most (\d+) tools`,
		`maximum length (\d+)`,
		`limit of (\d+) tools`,
		`takes (\d+) tools`,
	)
	// How many the request carried, likewise.
	clientToolOfferedPatterns = mustCompileAll(
		`(\d+) were offered`,
		`array with length (\d+)`,
		`got (\d+) tools`,
	)
	// The bound on one tool's parameters, and the tool it named. The name is
	// read off the answer as it came, so its letters keep their case, and the
	// quotes around it may still be escaped: what a client error carries is
	// the JSON of the error object as often as the decoded message.
	clientToolSchemaLimitPatterns = mustCompileAll(`parameters exceed (\d+) bytes`)
	clientToolSchemaNamePattern   = regexp.MustCompile(`tool \\?"([^"\\]{1,200})\\?": parameters exceed`)
)

// ClientToolLimitDetailOf reads the sizes out of a refusal IsClientToolLimit
// recognises. It is the zero value for any other error and for a refusal that
// gives no numbers, which leaves the agent with its own count of what it sent.
func ClientToolLimitDetailOf(err error) ClientToolLimit {
	if !IsClientToolLimit(err) {
		return ClientToolLimit{}
	}
	raw := clientText(err)
	text := strings.ToLower(raw)
	var d ClientToolLimit
	if m := clientToolSchemaNamePattern.FindStringSubmatch(raw); m != nil {
		return ClientToolLimit{Tool: m[1], Limit: firstNumber(clientToolSchemaLimitPatterns, text)}
	}
	d.Limit = firstNumber(clientToolLimitPatterns, text)
	d.Offered = firstNumber(clientToolOfferedPatterns, text)
	return d
}
