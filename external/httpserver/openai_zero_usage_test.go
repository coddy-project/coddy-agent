//go:build http

package httpserver

import (
	"github.com/tidwall/gjson"
	"testing"
)

func TestOpenAIExplicitZeroUsage(t *testing.T) {
	for _, model := range []string{"local/qwen3-1.7b", "agent"} {
		for _, stream := range []bool{false, true} {
			t.Run(model+map[bool]string{true: "/stream", false: "/json"}[stream], func(t *testing.T) {
				s := &openAIStreamCompatState{backendZeroUsage: true}
				if err := s.reset(); err != nil {
					t.Fatal(err)
				}
				defer s.close()
				if err := s.startServer(); err != nil {
					t.Fatal(err)
				}
				if stream {
					if err := s.openAIClientStreamsAskingForUsage(model); err != nil {
						t.Fatal(err)
					}
					found := false
					for _, chunk := range s.chunks() {
						if u := gjson.Get(chunk, "usage"); u.Exists() && u.Get("total_tokens").Exists() && u.Get("total_tokens").Int() == 0 {
							found = true
						}
					}
					if !found {
						t.Fatalf("explicit zero usage missing: %s", s.sseBody)
					}
				} else {
					if err := s.nonStreamingCompletion(model); err != nil {
						t.Fatal(err)
					}
					if u := gjson.Get(s.sseBody, "usage"); !u.Exists() || !u.Get("total_tokens").Exists() || u.Get("total_tokens").Int() != 0 {
						t.Fatalf("explicit zero usage missing: %s", s.sseBody)
					}
				}
			})
		}
	}
}
