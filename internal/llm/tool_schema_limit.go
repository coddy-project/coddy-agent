package llm

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// Tool schema limits are request validation failures, separate from a model's
// context window. Require both a tool-schema location and an explicit size or
// complexity refusal. In particular, Anthropic's bare compilation error also
// applies to output formats and does not establish that tools were rejected.
var toolSchemaLocation = regexp.MustCompile(`(?i)\b(?:tools?(?:\[[0-9]+\]|\.[0-9]+)?\.(?:function\.)?(?:parameters|input_schema)|tools?(?:\[[0-9]+\]|\.[0-9]+)?\.function[_]?declarations(?:\[[0-9]+\]|\.[0-9]+)?\.parameters(?:JsonSchema|_json_schema)?|function[_]?declarations(?:\[[0-9]+\]|\.[0-9]+)?\.parameters|tool schemas?\b|schema for (?:function|tool) ['"])`)

var toolSchemaLimitPhrases = []string{
	"schema exceeds maximum allowed size",
	"schema depth exceeds maximum limit",
	"schema is too large",
	"schema is too complex",
	"schema too large",
	"schema too complex",
}

// IsToolSchemaLimit reports a provider's explicit refusal of oversized or
// excessively complex tool parameter schemas. It assumes no byte, token,
// property or nesting cap. Generic invalid schemas and output-format limits
// remain ordinary provider errors. A known auth, quota or outage status wins
// over words in its body, as do cancellation and context overflow.
func IsToolSchemaLimit(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var acpErr *ACPError
	var undecodable *streamUndecodableError
	var truncated *streamTruncatedError
	if errors.As(err, &acpErr) || errors.As(err, &undecodable) || errors.As(err, &truncated) {
		return false
	}
	switch UpstreamStatus(err) {
	case 0, 400, 413, 422:
	default:
		return false
	}
	if IsContextOverflow(err) {
		return false
	}
	var request *toolSchemaRequestError
	for current := err; current != nil; current = errors.Unwrap(current) {
		text := strings.ToLower(toolSchemaDiagnostic(current))
		if strings.Contains(text, "output_config.format") || strings.Contains(text, "response_format") || strings.Contains(text, "text.format") {
			return false
		}
		if !toolSchemaLocation.MatchString(text) {
			if errors.As(err, &request) && request.toolOnly && strings.TrimSpace(text) == "schema is too complex for compilation." {
				return true
			}
			continue
		}
		if hasToolSchemaLimitPhrase(text) {
			return true
		}
	}
	return false
}

func hasToolSchemaLimitPhrase(text string) bool {
	for _, phrase := range toolSchemaLimitPhrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

// Read only the provider's diagnostic fields, not serialized request details.
// Decoding also makes quoted function names identical across SDKs and native
// adapters. A non-JSON diagnostic retains the existing text fallback.
func toolSchemaDiagnostic(err error) string {
	text := overflowText(err)
	var stream *streamServerError
	if errors.As(err, &stream) && stream.diagnostic != "" {
		text = stream.diagnostic
	}
	var raw *httpStatusError
	if errors.As(err, &raw) {
		if raw.diagnostic != "" {
			text = raw.diagnostic
		} else if raw.body != "" {
			text = raw.body
		}
	}
	var fields struct {
		Message, Param string
		Error          json.RawMessage
	}
	if json.Unmarshal([]byte(text), &fields) == nil {
		if len(fields.Error) > 0 {
			if json.Unmarshal(fields.Error, &fields) != nil {
				return text
			}
		}
		if fields.Message != "" {
			return strings.TrimSpace(fields.Param + " " + fields.Message)
		}
	}
	return text
}

// The names come from the adapter's constructed wire params, not a caller's
// pre-filtered tool list. No schemas, credentials or request text are retained.
type toolSchemaRequestError struct {
	cause       error
	names       []string
	googleNames map[string]string
	toolOnly    bool
}

func (e *toolSchemaRequestError) Error() string { return e.cause.Error() }
func (e *toolSchemaRequestError) Unwrap() error { return e.cause }

func withToolSchemaRequest(err error, params any, allowBareComplexity bool) error {
	if err == nil || !hasToolSchemaLimitPhrase(strings.ToLower(toolSchemaDiagnostic(err))) {
		return err
	}
	var data []byte
	switch typed := params.(type) {
	case []byte:
		data = typed
	case json.RawMessage:
		data = typed
	default:
		var marshalErr error
		data, marshalErr = json.Marshal(params)
		if marshalErr != nil {
			return err
		}
	}
	var wire struct {
		Tools []struct {
			Name                 string
			Function             *struct{ Name string }
			FunctionDeclarations []struct{ Name string } `json:"functionDeclarations"`
		} `json:"tools"`
		ResponseFormat json.RawMessage                  `json:"response_format"`
		OutputConfig   struct{ Format json.RawMessage } `json:"output_config"`
		Text           struct{ Format json.RawMessage } `json:"text"`
	}
	if json.Unmarshal(data, &wire) != nil {
		return err
	}
	var names []string
	googleNames := make(map[string]string)
	for groupIndex, tool := range wire.Tools {
		switch {
		case tool.Function != nil:
			names = append(names, tool.Function.Name)
		case len(tool.FunctionDeclarations) > 0:
			for declarationIndex, declaration := range tool.FunctionDeclarations {
				names = append(names, declaration.Name)
				googleNames[strconv.Itoa(groupIndex)+":"+strconv.Itoa(declarationIndex)] = declaration.Name
			}
		default:
			names = append(names, tool.Name)
		}
	}
	if len(names) == 0 {
		return err
	}
	return &toolSchemaRequestError{cause: err, names: names, googleNames: googleNames, toolOnly: allowBareComplexity && len(wire.ResponseFormat) == 0 && len(wire.OutputConfig.Format) == 0 && len(wire.Text.Format) == 0}
}

// ToolSchemaLimitDetail contains only facts a refusal establishes. Empty
// fields are unknown. Tool is matched to an offered name, never guessed from
// schema size or from an index whose adapter may have changed the ordering.
type ToolSchemaLimitDetail struct {
	Tool  string
	Limit string
}

var (
	toolSchemaFunction    = regexp.MustCompile(`(?i)schema for (?:function|tool) ['"]([a-zA-Z0-9_-]{1,128})['"]`)
	toolSchemaDepth       = regexp.MustCompile(`(?i)schema depth exceeds maximum limit of ([0-9]{1,9})\b`)
	toolSchemaIndex       = regexp.MustCompile(`(?i)\btools(?:\[([0-9]+)\]|\.([0-9]+))\.(?:function\.)?(?:parameters|input_schema)\b`)
	toolSchemaGoogleIndex = regexp.MustCompile(`(?i)\btools(?:\[([0-9]+)\]|\.([0-9]+))\.function[_]?declarations(?:\[([0-9]+)\]|\.([0-9]+))\.parameters(?:JsonSchema|_json_schema)?\b`)
	toolSchemaSafeName    = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)
)

// ToolSchemaLimitDetailOf reads safe diagnostic details for the actual tools
// offered on this call. It never copies arbitrary provider text or schemas
// into the user-facing explanation.
func ToolSchemaLimitDetailOf(err error, tools []ToolDefinition) ToolSchemaLimitDetail {
	var detail ToolSchemaLimitDetail
	if !IsToolSchemaLimit(err) {
		return detail
	}
	text := toolSchemaDiagnostic(err)
	// A safe display wrapper may hide the provider's text while preserving it
	// through Unwrap. Find the diagnostic that established this classification.
	for current := err; current != nil; current = errors.Unwrap(current) {
		if candidate := toolSchemaDiagnostic(current); hasToolSchemaLimitPhrase(strings.ToLower(candidate)) {
			text = candidate
		}
	}
	name := ""
	if m := toolSchemaFunction.FindStringSubmatch(text); m != nil {
		name = m[1]
	}
	var request *toolSchemaRequestError
	captured := errors.As(err, &request)
	if name == "" && captured {
		if m := toolSchemaGoogleIndex.FindStringSubmatch(text); m != nil {
			key := firstNonEmpty(m[1], m[2]) + ":" + firstNonEmpty(m[3], m[4])
			name = request.googleNames[key]
		} else if m := toolSchemaIndex.FindStringSubmatch(text); m != nil {
			index, parseErr := strconv.Atoi(firstNonEmpty(m[1], m[2]))
			if parseErr == nil && index >= 0 && index < len(request.names) {
				name = request.names[index]
			}
		} else if request.toolOnly && len(request.names) == 1 {
			name = request.names[0]
		}
	}
	// A provider can name a function that the adapter never sent. Every
	// attribution needs one unambiguous match in both the actual wire names
	// and the caller's offered definitions, including named diagnostics.
	if captured && toolSchemaSafeName.MatchString(name) {
		matches := 0
		for _, tool := range tools {
			if tool.Name == name {
				matches++
			}
		}
		wireMatches := 0
		for _, wireName := range request.names {
			if wireName == name {
				wireMatches++
			}
		}
		if matches == 1 && wireMatches == 1 {
			detail.Tool = name
		}
	}
	if m := toolSchemaDepth.FindStringSubmatch(text); m != nil {
		detail.Limit = "maximum schema depth " + m[1]
	}
	return detail
}
