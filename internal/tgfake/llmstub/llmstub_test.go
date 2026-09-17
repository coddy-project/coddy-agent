package llmstub

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModels(t *testing.T) {
	srv := httptest.NewServer((&Server{Model: "unit"}).Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Data []struct{ ID string } `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || len(body.Data) != 1 || body.Data[0].ID != "unit" {
		t.Fatalf("models: %v %+v", err, body)
	}
}

func post(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestNonStreamAnswerSelection(t *testing.T) {
	stub := &Server{Answers: []string{"first", "second"}, Rules: []Rule{{Match: "magic", Answer: "rule wins"}}}
	srv := httptest.NewServer(stub.Handler())
	defer srv.Close()

	read := func(prompt string) string {
		resp := post(t, srv.URL, `{"model":"x","messages":[{"role":"user","content":`+prompt+`}]}`)
		defer func() { _ = resp.Body.Close() }()
		var out struct {
			Choices []struct {
				Message struct{ Content string } `json:"message"`
			} `json:"choices"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || len(out.Choices) != 1 {
			t.Fatalf("completion: %v %+v", err, out)
		}
		return out.Choices[0].Message.Content
	}
	if got := read(`"hello"`); got != "first" {
		t.Fatalf("round robin 1: %q", got)
	}
	if got := read(`[{"type":"text","text":"say the MAGIC word"}]`); got != "rule wins" {
		t.Fatalf("rule over round robin: %q", got)
	}
	if got := read(`"again"`); got != "first" {
		t.Fatalf("round robin continues past rule hits: %q", got)
	}
	if got := read(`"and"`); got != "second" {
		t.Fatalf("round robin 2: %q", got)
	}
	echo := &Server{}
	if got := echo.Answer(" hi there "); got != "You said: hi there" {
		t.Fatalf("default echo: %q", got)
	}
}

func TestStreamFrames(t *testing.T) {
	stub := &Server{Answers: []string{"one two three"}, ChunkWords: 1}
	srv := httptest.NewServer(stub.Handler())
	defer srv.Close()
	resp := post(t, srv.URL, `{"model":"x","stream":true,"messages":[{"role":"user","content":"go"}]}`)
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type %q", ct)
	}
	var frames []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if line := sc.Text(); strings.HasPrefix(line, "data: ") {
			frames = append(frames, strings.TrimPrefix(line, "data: "))
		}
	}
	// role, three words, stop, usage, DONE
	if len(frames) != 7 || frames[6] != "[DONE]" {
		t.Fatalf("frames: %d %v", len(frames), frames)
	}
	var content strings.Builder
	for _, f := range frames[:5] {
		var c struct {
			Choices []struct {
				Delta        map[string]any `json:"delta"`
				FinishReason *string        `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(f), &c); err != nil {
			t.Fatalf("frame %q: %v", f, err)
		}
		if s, _ := c.Choices[0].Delta["content"].(string); s != "" {
			content.WriteString(s)
		}
	}
	if content.String() != "one two three" {
		t.Fatalf("streamed content %q", content.String())
	}
	if !strings.Contains(frames[4], `"finish_reason":"stop"`) || !strings.Contains(frames[5], `"usage"`) {
		t.Fatalf("tail frames: %v", frames[4:])
	}
}

func TestSplitWords(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want []string
	}{
		{"one two three", 1, []string{"one ", "two ", "three"}},
		{"one two three", 2, []string{"one two ", "three"}},
		{"a\nb", 1, []string{"a\n", "b"}},
		{"", 1, nil},
		{"solo", 3, []string{"solo"}},
	}
	for _, tc := range cases {
		got := splitWords(tc.in, tc.n)
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("splitWords(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}
