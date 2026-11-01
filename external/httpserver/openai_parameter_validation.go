//go:build http

package httpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

type chatParameterError struct {
	Param   string
	Message string
}

func (e *chatParameterError) Error() string { return e.Message }

// validateChatParameters checks the original JSON before the typed request can
// discard fields. The returned options must be validated against the provider.
func validateChatParameters(raw json.RawMessage, profile bool) (llm.RequestOptions, *chatParameterError) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return llm.RequestOptions{}, &chatParameterError{Message: "request must be a JSON object"}
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var opts llm.RequestOptions
	for _, key := range keys {
		value := fields[key]
		switch key {
		case "model", "stream":
		case "messages":
			if err := validateChatMessageFields(value); err != nil {
				return opts, err
			}
		case "metadata":
			if !profile {
				var metadata map[string]json.RawMessage
				if json.Unmarshal(value, &metadata) == nil {
					if _, ok := metadata["reasoning"]; ok {
						return opts, unsupportedChatParameter("metadata.reasoning")
					}
				}
			}
		case "max_tokens", "max_completion_tokens", "temperature", "reasoning_effort", "tools", "tool_choice", "top_p":
			if profile {
				return opts, unsupportedChatParameter(key)
			}
			switch key {
			case "tools":
				if !validToolFields(value) {
					return opts, unsupportedChatParameter(key)
				}
			case "top_p":
				if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
					continue
				}
				var v float64
				if json.Unmarshal(value, &v) != nil || v < 0 || v > 1 {
					return opts, &chatParameterError{Param: key, Message: "top_p must be a number between 0 and 1"}
				}
				opts.TopP = &v
			case "tool_choice":
				if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
					continue
				}
				var choice string
				if json.Unmarshal(value, &choice) != nil || (choice != "auto" && choice != "none") {
					return opts, unsupportedChatParameter(key)
				}
			}
		case "stream_options":
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				continue
			}
			var stream bool
			if json.Unmarshal(fields["stream"], &stream) != nil || !stream {
				return opts, unsupportedChatParameter(key)
			}
			var options map[string]json.RawMessage
			if json.Unmarshal(value, &options) != nil || options == nil {
				return opts, &chatParameterError{Param: key, Message: "stream_options must be an object"}
			}
			for name, item := range options {
				if name != "include_usage" {
					return opts, unsupportedChatParameter(key)
				}
				var v bool
				if json.Unmarshal(item, &v) != nil {
					return opts, &chatParameterError{Param: key, Message: "stream_options.include_usage must be a boolean"}
				}
			}
		default:
			return opts, unsupportedChatParameter(key)
		}
	}
	return opts, nil
}

func validateChatMessageFields(raw json.RawMessage) *chatParameterError {
	var messages []map[string]json.RawMessage
	if json.Unmarshal(raw, &messages) != nil {
		return nil // The request decoder reports malformed messages.
	}
	for i, msg := range messages {
		base := fmt.Sprintf("messages[%d]", i)
		if err := rejectUnknownFields(msg, base, "role", "content", "tool_calls", "tool_call_id"); err != nil {
			return err
		}
		var calls []map[string]json.RawMessage
		if json.Unmarshal(msg["tool_calls"], &calls) == nil {
			for j, call := range calls {
				path := fmt.Sprintf("%s.tool_calls[%d]", base, j)
				if err := rejectUnknownFields(call, path, "id", "type", "function"); err != nil {
					return err
				}
				var function map[string]json.RawMessage
				if json.Unmarshal(call["function"], &function) == nil {
					if err := rejectUnknownFields(function, path+".function", "name", "arguments"); err != nil {
						return err
					}
				}
			}
		}
		var parts []map[string]json.RawMessage
		if json.Unmarshal(msg["content"], &parts) == nil {
			for j, part := range parts {
				path := fmt.Sprintf("%s.content[%d]", base, j)
				if err := rejectUnknownFields(part, path, "type", "text", "image_url"); err != nil {
					return err
				}
				var image map[string]json.RawMessage
				if json.Unmarshal(part["image_url"], &image) == nil {
					if err := rejectUnknownFields(image, path+".image_url", "url"); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func rejectUnknownFields(fields map[string]json.RawMessage, path string, allowed ...string) *chatParameterError {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		found := false
		for _, candidate := range allowed {
			if key == candidate {
				found = true
				break
			}
		}
		if !found {
			return unsupportedChatParameter(path + "." + key)
		}
	}
	return nil
}

func validToolFields(raw json.RawMessage) bool {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return true
	}
	var tools []map[string]json.RawMessage
	if json.Unmarshal(raw, &tools) != nil {
		return false
	}
	for _, tool := range tools {
		if tool == nil {
			return false
		}
		for key, value := range tool {
			switch key {
			case "type":
			case "function":
				var function map[string]json.RawMessage
				if json.Unmarshal(value, &function) != nil || function == nil {
					return false
				}
				for field := range function {
					if field != "name" && field != "description" && field != "parameters" {
						return false
					}
				}
			default:
				return false
			}
		}
	}
	return true
}

func unsupportedChatParameter(param string) *chatParameterError {
	return &chatParameterError{Param: param, Message: fmt.Sprintf("parameter %q is not supported", param)}
}
