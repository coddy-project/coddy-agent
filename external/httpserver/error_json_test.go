//go:build http

package httpserver

import (
	"encoding/json"
	"fmt"
	"testing"
)

// An error body is JSON whatever the message holds: the colour codes a
// command printed, a file name that is not UTF-8, a quote. %q, which the
// handlers used to format it with, wrote the first two in Go's escapes, and
// the client could not parse the answer.
func TestErrorJSONIsJSONForAnyMessage(t *testing.T) {
	for _, msg := range []string{
		"folder not found: /srv/a b",
		"exit status 1: \x1b[31mred\x1b[0m",
		"open /tmp/\xff\xfe: no such file",
		`say "hi" <b>&</b> \ done`,
		"line one\nline two\ttab",
	} {
		body := errorJSON(msg)
		var got struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatalf("errorJSON(%q) = %s, not JSON: %v", msg, body, err)
		}
		if want := string([]rune(msg)); got.Error.Message != want {
			t.Errorf("errorJSON(%q) reads back %q, want %q", msg, got.Error.Message, want)
		}
	}
	// A message %q wrote as valid JSON keeps its bytes.
	for _, msg := range []string{"folder not found: /srv/a b", `say "hi" <b>&</b>`, "a\nb"} {
		if got, want := errorJSON(msg), fmt.Sprintf(`{"error":{"message":%q}}`, msg); got != want {
			t.Errorf("errorJSON(%q) = %s, want the former %s", msg, got, want)
		}
	}
}
