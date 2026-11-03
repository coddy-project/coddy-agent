//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cucumber/godog"
	"github.com/tidwall/gjson"
)

type reasoningReplayState struct {
	aggregatorReasoningState
	requests chan []byte
	response []byte
	stream   bool
}

func (s *reasoningReplayState) start() error {
	if err := s.reset(); err != nil {
		return err
	}
	s.requests = make(chan []byte, 16)
	s.backendTS = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[{"id":"test-model","context_length":128000}]}`)
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		s.requests <- raw
		if gjson.GetBytes(raw, "stream").Bool() {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl-replay\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"reasoning_content\":\"provider reasoning\",\"content\":\"OK\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-replay","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"OK","reasoning_content":"provider reasoning"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":4,"total_tokens":16}}`)
	}))
	return s.aCoddyServerOnTheAggregator("test/test-model")
}

func (s *reasoningReplayState) post(messages, stream string) error {
	s.stream = stream == "true"
	body := fmt.Sprintf(`{"model":"test/test-model","messages":%s,"stream":%s}`, messages, stream)
	res, err := http.Post(s.ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	s.response, err = io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d: %s", res.StatusCode, s.response)
	}
	return nil
}

func (s *reasoningReplayState) replay(field, stream string) error {
	return s.post(fmt.Sprintf(`[{"role":"user","content":"read"},{"role":"assistant","content":null,%q:"client reasoning","tool_calls":[{"id":"call_read","type":"function","function":{"name":"read","arguments":"{\"path\":\"note.txt\"}"}}]},{"role":"tool","tool_call_id":"call_read","content":"fixture"}]`, field), stream)
}

func (s *reasoningReplayState) history() error {
	select {
	case raw := <-s.requests:
		msgs := gjson.GetBytes(raw, "messages").Array()
		if len(msgs) != 3 || msgs[1].Get("reasoning_content").String() != "client reasoning" || msgs[1].Get("tool_calls.0.id").String() != "call_read" || msgs[1].Get("tool_calls.0.function.name").String() != "read" || msgs[1].Get("tool_calls.0.function.arguments").String() != `{"path":"note.txt"}` || msgs[2].Get("tool_call_id").String() != "call_read" || msgs[2].Get("content").String() != "fixture" {
			return fmt.Errorf("history was changed: %s", raw)
		}
		return nil
	default:
		return fmt.Errorf("no upstream request")
	}
}

func (s *reasoningReplayState) answer() error {
	if !s.stream {
		if gjson.GetBytes(s.response, "choices.0.message.content").String() != "OK" || gjson.GetBytes(s.response, "choices.0.message.reasoning_content").String() != "provider reasoning" {
			return fmt.Errorf("answer lost reasoning: %s", s.response)
		}
		return nil
	}
	var text, reasoning strings.Builder
	frames := parseSSEFrames(string(s.response))
	for _, frame := range frames {
		text.WriteString(gjson.Get(frame.data, "choices.0.delta.content").String())
		reasoning.WriteString(gjson.Get(frame.data, "choices.0.delta.reasoning_content").String())
	}
	if text.String() != "OK" || reasoning.String() != "provider reasoning" || len(frames) == 0 || frames[len(frames)-1].data != "[DONE]" {
		return fmt.Errorf("bad stream: %s", s.response)
	}
	return nil
}

func TestOpenAIReasoningReplayFeature(t *testing.T) {
	s := &reasoningReplayState{}
	suite := godog.TestSuite{Name: "reasoning-replay", ScenarioInitializer: func(sc *godog.ScenarioContext) {
		sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
			s.close()
			return ctx, nil
		})
		sc.Step(`^a recording reasoning upstream and a direct Coddy model$`, s.start)
		sc.Step(`^a client replays assistant reasoning as "([^"]+)" with streaming (true|false)$`, s.replay)
		sc.Step(`^the upstream receives the reasoning and the matching tool history$`, s.history)
		sc.Step(`^the client receives the answer and its reasoning$`, s.answer)
	}, Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/openai_reasoning_replay.feature"}, TestingT: t, Strict: true}}
	if suite.Run() != 0 {
		t.Fatal("reasoning replay feature failed")
	}
}

func TestOpenAIReasoningReplayMessages(t *testing.T) {
	for _, role := range []string{"assistant", "user", "system", "developer", "tool"} {
		for _, field := range []string{"reasoning_content", "reasoning"} {
			for _, value := range []string{`"client reasoning"`, `""`, `null`} {
				t.Run(role+"/"+field+"/"+value, func(t *testing.T) {
					s := &reasoningReplayState{}
					if err := s.start(); err != nil {
						t.Fatal(err)
					}
					defer s.close()
					extra := ""
					if role == "tool" {
						extra = `,"tool_call_id":"call_read"`
					}
					messages := fmt.Sprintf(`[{"role":%q,"content":"prior",%q:%s%s},{"role":"user","content":"next"}]`, role, field, value, extra)
					if err := s.post(messages, "false"); err != nil {
						t.Fatal(err)
					}
					var raw []byte
					select {
					case raw = <-s.requests:
					default:
						t.Fatal("no upstream request")
					}
					got := gjson.GetBytes(raw, "messages.0.reasoning_content").String()
					want := ""
					if role == "assistant" && value == `"client reasoning"` {
						want = "client reasoning"
					}
					if got != want {
						t.Fatalf("reasoning=%q want %q: %s", got, want, raw)
					}
				})
			}
		}
	}
}

func TestOpenAIReasoningReplayValidation(t *testing.T) {
	for _, fields := range []string{`"reasoning_content":1`, `"reasoning":{}`, `"reasoning_content":"one","reasoning":"two"`} {
		t.Run(fields, func(t *testing.T) {
			raw := json.RawMessage(`{"messages":[{"role":"assistant","content":"hi",` + fields + `}]}`)
			_, err := validateChatParameters(raw, false)
			if err == nil || !strings.HasPrefix(err.Param, "messages[0].reasoning") {
				t.Fatalf("expected reasoning error, got %v", err)
			}
		})
	}
	for _, fields := range []string{`"reasoning_content":"same","reasoning":"same"`, `"reasoning_content":"","reasoning":"fallback"`, `"reasoning_content":null,"reasoning":"fallback"`} {
		raw := json.RawMessage(`{"messages":[{"role":"assistant","content":"hi",` + fields + `}]}`)
		if _, err := validateChatParameters(raw, false); err != nil {
			t.Errorf("valid aliases %s: %v", fields, err)
		}
	}
}
