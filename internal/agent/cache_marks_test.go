package agent

import (
	"encoding/json"
	"fmt"
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

// cacheMarkPositions names where a whole request body carries its cache marks:
// "system" for the one on the system prompt, "messages[i]" for one on a block
// of message i, in that order and once per mark. withoutCacheMarks above drops
// the marks so a request prefix can still be compared byte for byte; this
// reads them instead, because a comparison that strips them no longer sees a
// mark that went missing, that drifted onto the request-only turn context, or
// that appeared a third time somewhere in the history.
func cacheMarkPositions(t *testing.T, raw string) []string {
	t.Helper()
	marks, err := cacheMarks(raw)
	if err != nil {
		t.Fatal(err)
	}
	return marks
}

// cacheMarks is cacheMarkPositions for a caller with no *testing.T, which is
// what a godog step is.
func cacheMarks(raw string) ([]string, error) {
	body, err := decodeRequestBody(raw)
	if err != nil {
		return nil, err
	}
	var out []string
	n, err := countCacheMarks(body.System)
	if err != nil {
		return nil, err
	}
	for range n {
		out = append(out, "system")
	}
	for i, m := range body.Messages {
		n, err := countCacheMarks(m)
		if err != nil {
			return nil, err
		}
		for range n {
			out = append(out, fmt.Sprintf("messages[%d]", i))
		}
	}
	return out, nil
}

// requestMessageCount is how many messages a request body carries, so a step
// can name its last one - the turn context block, which no later request
// repeats and which therefore must never be marked.
func requestMessageCount(raw string) (int, error) {
	body, err := decodeRequestBody(raw)
	if err != nil {
		return 0, err
	}
	return len(body.Messages), nil
}

type requestBody struct {
	System   json.RawMessage   `json:"system"`
	Messages []json.RawMessage `json:"messages"`
}

func decodeRequestBody(raw string) (requestBody, error) {
	var body requestBody
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		return requestBody{}, fmt.Errorf("decode request body: %w", err)
	}
	return body, nil
}

// countCacheMarks counts the cache_control keys anywhere inside one piece of a
// request body.
func countCacheMarks(raw json.RawMessage) (int, error) {
	if len(raw) == 0 {
		return 0, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0, fmt.Errorf("decode request part: %w", err)
	}
	n := 0
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, e := range x {
				if k == "cache_control" {
					n++
					continue
				}
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(v)
	return n, nil
}
