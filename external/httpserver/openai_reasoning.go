//go:build http

package httpserver

import (
	"bytes"
	"encoding/json"
)

// chatReasoning normalizes the two history extensions without trimming the
// model's text. Empty and null fields carry no reasoning; conflicting nonempty
// aliases cannot be forwarded without silently changing the caller's history.
func chatReasoning(content, alias json.RawMessage) (string, *chatParameterError) {
	var values [2]string
	for i, raw := range []json.RawMessage{content, alias} {
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
			continue
		}
		if err := json.Unmarshal(raw, &values[i]); err != nil {
			field := "reasoning_content"
			if i == 1 {
				field = "reasoning"
			}
			return "", &chatParameterError{Param: field, Message: field + " must be a string or null"}
		}
	}
	if values[0] != "" && values[1] != "" && values[0] != values[1] {
		return "", &chatParameterError{Param: "reasoning", Message: "reasoning and reasoning_content disagree"}
	}
	if values[0] != "" {
		return values[0], nil
	}
	return values[1], nil
}
