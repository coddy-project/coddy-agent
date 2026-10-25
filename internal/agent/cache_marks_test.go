package agent

import (
	"encoding/json"
	"testing"
)

// withoutCacheMarks is a request message with its cache_control marks taken
// out, for the tests that hold the history to a byte-for-byte prefix. The
// Anthropic provider moves its rolling mark to the newest turn on every
// request; the provider matches the cached prefix by content, and a mark only
// says where a cache entry is written, so the mark leaving an older message
// does not rewrite what is cached.
func withoutCacheMarks(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	var strip func(any)
	strip = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			delete(x, "cache_control")
			for _, e := range x {
				strip(e)
			}
		case []any:
			for _, e := range x {
				strip(e)
			}
		}
	}
	strip(v)
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
