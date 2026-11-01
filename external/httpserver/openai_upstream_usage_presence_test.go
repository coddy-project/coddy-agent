//go:build http

package httpserver

import (
	"github.com/tidwall/gjson"
	"testing"
)

func TestOpenAINonStreamingUpstreamUsagePresence(t *testing.T) {
	for _, known := range []bool{false, true} {
		for _, model := range []string{"local/qwen3-1.7b", "agent"} {
			t.Run(model+map[bool]string{true: "/zero", false: "/missing"}[known], func(t *testing.T) {
				s := &openAIStreamCompatState{backendNonStream: true, backendZeroUsage: known, backendNoUsage: !known}
				if err := s.reset(); err != nil {
					t.Fatal(err)
				}
				defer s.close()
				if err := s.startServer(); err != nil {
					t.Fatal(err)
				}
				if err := s.nonStreamingCompletion(model); err != nil {
					t.Fatal(err)
				}
				u := gjson.Get(s.sseBody, "usage")
				if u.Exists() != known {
					t.Fatalf("usage=%s, known=%v: %s", u.Raw, known, s.sseBody)
				}
			})
		}
	}
}
