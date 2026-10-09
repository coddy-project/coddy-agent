package acp

import (
	"encoding/json"
	"testing"
)

// A request id is a number, a string or null, and goes back exactly as it
// came; anything else is not an id.
func TestRequestIDKeepsTheIDAsItCame(t *testing.T) {
	for _, in := range []string{`1`, `1.0`, `-7`, `"abc"`, `null`, ` 42 `} {
		got, ok := requestID(json.RawMessage(in))
		if !ok {
			t.Fatalf("%s: refused", in)
		}
		if want := json.RawMessage(in); string(got) != string(trimmedJSON(want)) {
			t.Fatalf("%s: got %s", in, got)
		}
	}
	for _, in := range []string{`{}`, `[1]`, `true`, ``, `{"a":`} {
		if _, ok := requestID(json.RawMessage(in)); ok {
			t.Fatalf("%q: accepted", in)
		}
	}
}

func trimmedJSON(raw json.RawMessage) string {
	s := string(raw)
	for len(s) > 0 && s[0] == ' ' {
		s = s[1:]
	}
	for len(s) > 0 && s[len(s)-1] == ' ' {
		s = s[:len(s)-1]
	}
	return s
}
