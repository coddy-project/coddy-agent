package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCoddyStreamAssemblesTheAnswerFromTheFinalFrame(t *testing.T) {
	remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
		fw := startStream(w)
		fw.reasoning("Check the file first.")
		fw.text("Hello")
		fw.text(" there")
		fw.heartbeat()
		fw.final(&Response{
			Content: "Hello there.", Reasoning: "Check the file first.", ReasoningSignature: "envelope-1",
			StopReason: "end_turn", InputTokens: 120, OutputTokens: 8, CachedInputTokens: 100,
		})
	})
	p := newTestCoddy(t, coddyInput(remote))
	got := streamOnce(p, []Message{{Role: RoleSystem, Content: "You are the local harness."}, {Role: RoleUser, Content: "Hi"}}, nil)
	if got.err != nil {
		t.Fatalf("Stream: %v", got.err)
	}
	want := &Response{
		Content: "Hello there.", Reasoning: "Check the file first.", ReasoningSignature: "envelope-1",
		StopReason: "end_turn", InputTokens: 120, OutputTokens: 8, CachedInputTokens: 100,
	}
	if !reflect.DeepEqual(got.resp, want) {
		t.Fatalf("the result is the final frame as it is\n got: %+v\nwant: %+v", got.resp, want)
	}
	if len(got.chunks) != 3 {
		t.Fatalf("chunks reach the caller as progress: got %d, want 3 (heartbeats are not chunks): %+v", len(got.chunks), got.chunks)
	}
}

func TestCoddyFinalIsNeverAppendedToWhatTheChunksBuilt(t *testing.T) {
	call := ToolCall{ID: "call_1", Name: "get_weather", InputJSON: `{"city":"Paris"}`}
	remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
		fw := startStream(w)
		fw.text("It is")
		fw.frame(WireChunkFromStream(StreamChunk{ToolCallNamed: &ToolCall{ID: "call_1", Name: "get_weather"}}))
		fw.frame(WireChunkFromStream(StreamChunk{ToolCallDelta: &ToolCall{ID: "call_1", Name: "get_weather", InputJSON: `{"city":`}}))
		fw.toolCall(call)
		fw.final(&Response{Content: "It is", ToolCalls: []ToolCall{call}, StopReason: "tool_use"})
	})
	got := streamOnce(newTestCoddy(t, coddyInput(remote)), userMsg("Weather?"), nil)
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.resp.Content != "It is" {
		t.Fatalf("content %q: the final was appended to the chunks", got.resp.Content)
	}
	if len(got.resp.ToolCalls) != 1 || got.resp.ToolCalls[0] != call {
		t.Fatalf("the tool call runs once, from the final: %+v", got.resp.ToolCalls)
	}
}

func TestCoddyCompleteIsStreamWithTheChunksDropped(t *testing.T) {
	remote := newFakeRemote(t, simpleAnswer("complete answer"))
	p := newTestCoddy(t, coddyInput(remote))
	resp, err := p.Complete(context.Background(), userMsg("Hi"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "complete answer" || resp.InputTokens != 12 {
		t.Fatalf("Complete: %+v", resp)
	}
	if !strings.Contains(remote.request(1).header.Get("Accept"), "text/event-stream") {
		t.Fatal("Complete is also one streamed call: the wire has no blocking form")
	}
}

func TestCoddyRequestShape(t *testing.T) {
	remote := newFakeRemote(t, simpleAnswer("ok"))
	in := coddyInput(remote)
	in.MaxTokens = 4096
	in.Temperature, in.TemperatureSet = 0, true
	in.ReasoningEffort = "high"
	in.ExpectedRevision = "rev-1"
	tools := []ToolDefinition{{Name: "get_weather", Description: "weather", InputSchema: map[string]any{"type": "object"}}}
	msgs := []Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "Hi", ImageParts: []ImagePart{{DataURL: "data:image/png;base64,AAAA", MIMEType: "image/png", Name: "a.png", FilePath: "/home/me/a.png", Size: 4}}},
		{Role: RoleAssistant, Content: "", Reasoning: "r", ReasoningSignature: "env", ToolCalls: []ToolCall{{ID: "c1", Name: "get_weather", InputJSON: `{"city":"Paris"}`}}, Model: "remote/coder", CreatedAt: "2026-10-07T00:00:00Z"},
		{Role: RoleTool, Content: "18C", ToolCallID: "c1", Rules: "folded already"},
	}
	if got := streamOnce(newTestCoddy(t, in), msgs, tools); got.err != nil {
		t.Fatal(got.err)
	}
	rec := remote.request(1)
	if rec.method != http.MethodPost || rec.path != "/coddy/llm/completions" {
		t.Fatalf("%s %s", rec.method, rec.path)
	}
	h := rec.header
	if h.Get("Authorization") != "Bearer shared-token" {
		t.Errorf("Authorization = %q", h.Get("Authorization"))
	}
	if h.Get("Accept-Encoding") != "identity" {
		t.Errorf("Accept-Encoding = %q: the stream is read byte by byte", h.Get("Accept-Encoding"))
	}
	if h.Get("Accept") != "text/event-stream" || h.Get("Content-Type") != "application/json" {
		t.Errorf("Accept %q Content-Type %q", h.Get("Accept"), h.Get("Content-Type"))
	}
	if !strings.EqualFold(h.Get("Expect"), "100-continue") {
		t.Errorf("Expect = %q: a refused call must not upload the history", h.Get("Expect"))
	}
	req := rec.req
	if req.Protocol != CoddyProtocol || req.Model != "coder" {
		t.Errorf("protocol %d model %q", req.Protocol, req.Model)
	}
	if !reflect.DeepEqual(WireMessagesToLLM(req.Messages), []Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "Hi", ImageParts: []ImagePart{{DataURL: "data:image/png;base64,AAAA", MIMEType: "image/png", Name: "a.png"}}},
		{Role: RoleAssistant, Reasoning: "r", ReasoningSignature: "env", ToolCalls: []ToolCall{{ID: "c1", Name: "get_weather", InputJSON: `{"city":"Paris"}`}}},
		{Role: RoleTool, Content: "18C", ToolCallID: "c1"},
	}) {
		t.Errorf("messages on the wire: %s", rec.raw)
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "get_weather" {
		t.Errorf("tools: %+v", req.Tools)
	}
	o := req.Options
	if o.MaxTokens == nil || *o.MaxTokens != 4096 || o.Temperature == nil || *o.Temperature != 0 ||
		o.ReasoningEffort == nil || *o.ReasoningEffort != "high" || o.ExpectedRevision == nil || *o.ExpectedRevision != "rev-1" {
		t.Errorf("options: %s", rec.raw)
	}
	if o.RetryBudgetMS != nil {
		t.Errorf("retry_budget_ms is sent only under agent.wait_for_limit_reset: %s", rec.raw)
	}
	for _, leak := range []string{"/home/me", "remote/coder", "2026-10-07", "folded already"} {
		if strings.Contains(string(rec.raw), leak) {
			t.Errorf("the request carries %q: %s", leak, rec.raw)
		}
	}
}

func TestCoddySendsOnlyTheOptionsTheClientSetExplicitly(t *testing.T) {
	remote := newFakeRemote(t, simpleAnswer("ok"))
	if got := streamOnce(newTestCoddy(t, coddyInput(remote)), userMsg("Hi"), nil); got.err != nil {
		t.Fatal(got.err)
	}
	if got := string(remote.request(1).raw); !strings.Contains(got, `"options":{}`) {
		t.Fatalf("a client that set nothing sends an empty options object: %s", got)
	}

	// A configured temperature is dropped next to a reasoning level, the way a
	// local provider drops it; an explicitly requested one is kept.
	for name, tc := range map[string]struct {
		temp   float64
		set    bool
		effort string
		want   bool
	}{
		"configured, no level":  {0.7, false, "", true},
		"configured with level": {0.7, false, "high", false},
		"asked with level":      {0.7, true, "high", true},
		"asked zero":            {0, true, "", true},
		"none":                  {0, false, "", false},
	} {
		t.Run(name, func(t *testing.T) {
			r := newFakeRemote(t, simpleAnswer("ok"))
			in := coddyInput(r)
			in.Temperature, in.TemperatureSet, in.ReasoningEffort = tc.temp, tc.set, tc.effort
			if got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil); got.err != nil {
				t.Fatal(got.err)
			}
			if sent := r.request(1).req.Options.Temperature != nil; sent != tc.want {
				t.Fatalf("temperature sent = %v, want %v: %s", sent, tc.want, r.request(1).raw)
			}
		})
	}
}

type spentLedger struct{ d time.Duration }

func (l *spentLedger) Spent() time.Duration   { return l.d }
func (l *spentLedger) Charge(d time.Duration) { l.d += d }

func TestCoddyRetryBudgetCarriesWhatRemainsOfTheLimitBudget(t *testing.T) {
	remote := newFakeRemote(t, simpleAnswer("ok"))
	in := coddyInput(remote)
	in.RetryBudget, in.RetryBudgetSet = 10*time.Minute, true
	in.LimitLedger = &spentLedger{d: 4 * time.Minute}
	if got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil); got.err != nil {
		t.Fatal(got.err)
	}
	o := remote.request(1).req.Options
	if o.RetryBudgetMS == nil || *o.RetryBudgetMS != (6*time.Minute).Milliseconds() {
		t.Fatalf("retry_budget_ms = %v, want 360000: %s", o.RetryBudgetMS, remote.request(1).raw)
	}

	// An exhausted budget is an explicit zero: the remote reports a quota at once.
	in.LimitLedger = &spentLedger{d: time.Hour}
	r2 := newFakeRemote(t, simpleAnswer("ok"))
	in.BaseURL = r2.url()
	if got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil); got.err != nil {
		t.Fatal(got.err)
	}
	if o := r2.request(1).req.Options; o.RetryBudgetMS == nil || *o.RetryBudgetMS != 0 {
		t.Fatalf("an exhausted budget travels as an explicit 0: %s", r2.request(1).raw)
	}
}

func TestCoddyURLForEveryShapeOfAPIBase(t *testing.T) {
	remote := newFakeRemote(t, simpleAnswer("ok"))
	for base, wantPath := range map[string]string{
		remote.url():                                "/coddy/llm/completions",
		remote.url() + "/":                          "/coddy/llm/completions",
		remote.url() + "/swarm/nodes/n1":            "/swarm/nodes/n1/coddy/llm/completions",
		remote.url() + "/swarm/nodes/n1/":           "/swarm/nodes/n1/coddy/llm/completions",
		"  " + remote.url() + "/relay//":            "/relay/coddy/llm/completions",
		remote.url() + "/deep/mount/with/segments/": "/deep/mount/with/segments/coddy/llm/completions",
	} {
		in := coddyInput(remote)
		in.BaseURL = base
		before := remote.count()
		if got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil); got.err != nil {
			t.Fatalf("base %q: %v", base, got.err)
		}
		if got := remote.request(before + 1).path; got != wantPath {
			t.Errorf("base %q: path %q, want %q", base, got, wantPath)
		}
	}
}

func TestCoddyRefusesAnAPIBaseThatIsNotAnHTTPAddress(t *testing.T) {
	for _, base := range []string{"", "  ", "remote.example", "ftp://remote.example", "https://"} {
		in := ProviderInput{Name: "remote", Type: "coddy", Model: "coder", BaseURL: base, ProxyURL: "none"}
		if _, err := NewProvider(in); err == nil {
			t.Errorf("api_base %q was accepted", base)
		}
	}
}

func TestCoddyWithoutAKeySendsNoAuthorization(t *testing.T) {
	remote := newFakeRemote(t, simpleAnswer("ok"))
	in := coddyInput(remote)
	in.APIKey = ""
	if got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil); got.err != nil {
		t.Fatal(got.err)
	}
	if v := remote.request(1).header.Get("Authorization"); v != "" {
		t.Fatalf("Authorization = %q", v)
	}
}

func TestCoddyExpectContinueKeepsAnUploadFromAFullRemote(t *testing.T) {
	var cl *countingListener
	// A remote that answers busy without reading the body (the slot is taken
	// before the body is read).
	srv := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		answerWire(w, http.StatusTooManyRequests, WireError{Status: 429, Kind: WireKindBusy}, "Retry-After", "1")
	}, &cl)
	in := ProviderInput{Name: "remote", Type: "coddy", Model: "coder", BaseURL: srv.URL, ProxyURL: "none"}
	big := []Message{{Role: RoleUser, Content: bigText(3 << 20)}}
	got := streamOnce(newTestCoddy(t, in), big, nil)
	if CoddyErrorKind(got.err) != WireKindBusy {
		t.Fatalf("want the spent busy wait, got %v", got.err)
	}
	if n := cl.read.Load(); n > 256<<10 {
		t.Fatalf("the remote read %d bytes of a 3 MiB history it had refused: Expect: 100-continue did not hold the upload back", n)
	}
}

func TestCoddyPartialAnswerAndTypedErrorSurviveAFailureAfterOutput(t *testing.T) {
	call := ToolCall{ID: "call_1", Name: "get_weather", InputJSON: `{"city":"Paris"}`}
	remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
		fw := startStream(w)
		fw.reasoning("thinking ")
		fw.text("It is ")
		fw.text("sunny")
		fw.frame(WireChunkFromStream(StreamChunk{ToolCallNamed: &ToolCall{ID: "call_2", Name: "later"}}))
		fw.frame(WireChunkFromStream(StreamChunk{ToolCallDelta: &ToolCall{ID: "call_2", Name: "later", InputJSON: `{"a":`}}))
		fw.toolCall(call)
		fw.fail(WireError{Status: 502, Kind: WireKindUpstream, Cause: WireCauseStall, Emitted: true})
	})
	got := streamOnce(newTestCoddy(t, coddyInput(remote)), userMsg("Hi"), nil)
	if got.err == nil || got.resp == nil {
		t.Fatalf("want the partial answer next to the error, got resp=%v err=%v", got.resp, got.err)
	}
	if got.resp.Content != "It is sunny" || got.resp.Reasoning != "thinking " {
		t.Fatalf("partial text/reasoning: %+v", got.resp)
	}
	if len(got.resp.ToolCalls) != 1 || got.resp.ToolCalls[0] != call {
		t.Fatalf("the partial carries the tool calls whose chunk arrived and drops the unfinished ones: %+v", got.resp.ToolCalls)
	}
	if !IsStreamStalled(got.err) || !IsStreamTruncated(got.err) || !IsTransientProviderError(got.err) {
		t.Fatalf("predicates of a stall after output: stalled=%v truncated=%v transient=%v (%v)",
			IsStreamStalled(got.err), IsStreamTruncated(got.err), IsTransientProviderError(got.err), got.err)
	}
	if remote.count() != 1 {
		t.Fatalf("a failure after output is never repeated by the wrapper: %d requests", remote.count())
	}
}

func TestCoddyTheFramesAfterTheTerminalEventAreDiscarded(t *testing.T) {
	remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
		fw := startStream(w)
		fw.text("first")
		fw.final(&Response{Content: "first", StopReason: "end_turn"})
		fw.text("junk")
		fw.final(&Response{Content: "second final"})
		fw.fail(WireError{Kind: WireKindUpstream})
	})
	got := streamOnce(newTestCoddy(t, coddyInput(remote)), userMsg("Hi"), nil)
	if got.err != nil || got.resp.Content != "first" {
		t.Fatalf("result %+v err %v", got.resp, got.err)
	}
	if len(got.chunks) != 1 {
		t.Fatalf("chunks after the terminal event reached the caller: %+v", got.chunks)
	}
}

func TestCoddyAnErrorFrameEndsTheStreamOnce(t *testing.T) {
	remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
		fw := startStream(w)
		fw.fail(WireError{Status: 502, Kind: WireKindUpstream, Cause: WireCauseStatus})
		fw.final(&Response{Content: "late"})
	})
	got := streamOnce(newTestCoddy(t, coddyInput(remote)), userMsg("Hi"), nil)
	if got.err == nil || got.resp != nil {
		t.Fatalf("an error ends the call: resp=%v err=%v", got.resp, got.err)
	}
}

func TestCoddyCommentsAndBlankLinesAreNotFrames(t *testing.T) {
	remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
		fw := startStream(w)
		fw.heartbeat()
		fw.raw(": hb\n\n: another comment\n\n\n\n")
		fw.text("a")
		fw.heartbeat()
		fw.final(&Response{Content: "a", StopReason: "end_turn"})
	})
	got := streamOnce(newTestCoddy(t, coddyInput(remote)), userMsg("Hi"), nil)
	if got.err != nil || got.resp.Content != "a" || len(got.chunks) != 1 {
		t.Fatalf("resp %+v chunks %d err %v", got.resp, len(got.chunks), got.err)
	}
}

// A stream that ends without its terminal event is a cut: a transport failure,
// retried only while nothing was emitted.
func TestCoddyEOFWithoutATerminalEventIsRetriedOnlyBeforeOutput(t *testing.T) {
	t.Run("before output", func(t *testing.T) {
		remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, n int, _ WireRequest) {
			fw := startStream(w)
			if n < 3 {
				fw.heartbeat()
				return
			}
			fw.text("ok")
			fw.final(&Response{Content: "ok", StopReason: "end_turn"})
		})
		in := coddyInput(remote)
		in.RetryMax, in.RetryBase = 3, time.Millisecond
		got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
		if got.err != nil || got.resp.Content != "ok" {
			t.Fatalf("resp %+v err %v", got.resp, got.err)
		}
		if remote.count() != 3 {
			t.Fatalf("%d requests, want 3", remote.count())
		}
	})
	t.Run("before output, spent retries", func(t *testing.T) {
		remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
			startStream(w).heartbeat()
		})
		in := coddyInput(remote)
		in.RetryMax, in.RetryBase = 2, time.Millisecond
		got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
		if !IsStreamTruncated(got.err) {
			t.Fatalf("want a truncated stream, got %v", got.err)
		}
		if remote.count() != 3 {
			t.Fatalf("%d requests, want RetryMax+1 = 3", remote.count())
		}
	})
	t.Run("after output", func(t *testing.T) {
		remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
			fw := startStream(w)
			fw.text("half an ans")
		})
		in := coddyInput(remote)
		in.RetryMax, in.RetryBase = 3, time.Millisecond
		got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
		if !IsStreamTruncated(got.err) || !IsTransientProviderError(got.err) {
			t.Fatalf("want a truncated transient stream, got %v", got.err)
		}
		if got.resp == nil || got.resp.Content != "half an ans" {
			t.Fatalf("the partial answer is kept: %+v", got.resp)
		}
		if remote.count() != 1 {
			t.Fatalf("a stream cut after output is not repeated: %d requests", remote.count())
		}
	})
}

// Complete drops the chunks, so nothing reached a caller: a cut after output is
// as safe to repeat as one before it (the Devin provider's contract).
func TestCoddyCompleteRepeatsACutStreamBecauseNothingReachedTheCaller(t *testing.T) {
	remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, n int, _ WireRequest) {
		fw := startStream(w)
		fw.text("part")
		if n == 1 {
			return
		}
		fw.final(&Response{Content: "part and the rest", StopReason: "end_turn"})
	})
	in := coddyInput(remote)
	in.RetryMax, in.RetryBase = 3, time.Millisecond
	resp, err := newTestCoddy(t, in).Complete(context.Background(), userMsg("Hi"), nil)
	if err != nil || resp.Content != "part and the rest" {
		t.Fatalf("resp %+v err %v", resp, err)
	}
	if remote.count() != 2 {
		t.Fatalf("%d requests, want 2", remote.count())
	}
}

// The wire statuses of the table: every kind surfaces typed, the wrapper never
// multiplies the request, and a deterministic upstream failure costs the remote
// one request per call (its own retries are its business), not sixteen.
func TestCoddyTheWrapperNeverMultipliesRequests(t *testing.T) {
	for name, frame := range map[string]WireError{
		"upstream status":    {Status: 502, Kind: WireKindUpstream, Cause: WireCauseStatus},
		"upstream stall":     {Status: 502, Kind: WireKindUpstream, Cause: WireCauseStall},
		"upstream cut":       {Status: 502, Kind: WireKindUpstream, Cause: WireCauseTruncated},
		"upstream timeout":   {Status: 504, Kind: WireKindUpstream, Cause: WireCauseTimeout},
		"rate":               {Status: 429, Kind: WireKindRate, RetryAfterS: 3},
		"invalid":            {Status: 400, Kind: WireKindInvalid},
		"auth":               {Status: 401, Kind: WireKindAuth},
		"quota without time": {Status: 429, Kind: WireKindQuota},
	} {
		t.Run(name, func(t *testing.T) {
			remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
				startStream(w).fail(frame)
			})
			in := coddyInput(remote)
			in.RetryMax, in.RetryBase = 3, time.Millisecond
			got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
			if got.err == nil {
				t.Fatal("no error")
			}
			if remote.count() != 1 {
				t.Fatalf("one Stream call cost the remote %d requests", remote.count())
			}
			if CoddyErrorKind(got.err) != frame.Kind {
				t.Fatalf("kind %q, want %q (%v)", CoddyErrorKind(got.err), frame.Kind, got.err)
			}
		})
	}
}

func TestCoddyTheSameFailuresBeforeTheStreamAreNeverMultipliedEither(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		err    WireError
	}{
		"invalid": {400, WireError{Status: 400, Kind: WireKindInvalid, Message: "bad"}},
		"unknown": {404, WireError{Status: 404, Kind: WireKindInvalid}},
		"auth":    {403, WireError{Status: 403, Kind: WireKindAuth}},
		"rate":    {429, WireError{Status: 429, Kind: WireKindRate, RetryAfterS: 1}},
		"stale":   {400, WireError{Status: 400, Kind: WireKindInvalid, Code: WireCodeStaleRevision, Revision: "r2"}},
	} {
		t.Run(name, func(t *testing.T) {
			remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
				answerWire(w, tc.status, tc.err)
			})
			in := coddyInput(remote)
			in.RetryMax, in.RetryBase = 3, time.Millisecond
			got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
			if CoddyErrorKind(got.err) != tc.err.Kind {
				t.Fatalf("kind %q, want %q (%v)", CoddyErrorKind(got.err), tc.err.Kind, got.err)
			}
			if remote.count() != 1 {
				t.Fatalf("%d requests, want 1", remote.count())
			}
		})
	}
}

func TestCoddyAQuotaWithAResetIsTheTypedLimitTheAgentWaitsFor(t *testing.T) {
	reset := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
		startStream(w).fail(WireError{Status: 429, Kind: WireKindQuota, ResetAt: reset.Format(time.RFC3339)})
	})
	in := coddyInput(remote)
	in.RetryMax, in.RetryBase = 3, time.Millisecond
	got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
	var qr *QuotaResetError
	if !errors.As(got.err, &qr) {
		t.Fatalf("want a QuotaResetError, got %T %v", got.err, got.err)
	}
	if !qr.ResetAt.Equal(reset) || qr.Delay < time.Hour+50*time.Minute || qr.Delay > 2*time.Hour {
		t.Fatalf("reset %v delay %v", qr.ResetAt, qr.Delay)
	}
	if remote.count() != 1 {
		t.Fatalf("a quota is not retried: %d requests", remote.count())
	}
	if UpstreamStatus(got.err) != 429 {
		t.Fatalf("UpstreamStatus = %d", UpstreamStatus(got.err))
	}
	if d, ok := UpstreamRetryAfter(got.err); !ok || d != qr.Delay {
		t.Fatalf("UpstreamRetryAfter = %v %v", d, ok)
	}
	if IsTransientProviderError(got.err) {
		t.Fatal("a limit is not transient")
	}

	// The same answered before the stream.
	r2 := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
		answerWire(w, 429, WireError{Status: 429, Kind: WireKindQuota, RetryAfterS: 3600})
	})
	in.BaseURL = r2.url()
	got = streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
	if !errors.As(got.err, &qr) || qr.Delay != time.Hour {
		t.Fatalf("a pause alone becomes the reset: %T %v", got.err, got.err)
	}
}

func TestCoddyAQuotaAfterOutputKeepsThePartialAnswer(t *testing.T) {
	remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
		fw := startStream(w)
		fw.text("partial")
		fw.fail(WireError{Status: 429, Kind: WireKindQuota, ResetAt: time.Now().Add(time.Hour).Format(time.RFC3339), Emitted: true})
	})
	got := streamOnce(newTestCoddy(t, coddyInput(remote)), userMsg("Hi"), nil)
	var qr *QuotaResetError
	if !errors.As(got.err, &qr) || got.resp == nil || got.resp.Content != "partial" {
		t.Fatalf("resp %+v err %v", got.resp, got.err)
	}
}

func TestCoddyTimeoutIsNotTransientAndKeepsTheOutputAfterText(t *testing.T) {
	remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
		fw := startStream(w)
		fw.text("half")
		fw.fail(WireError{Status: 504, Kind: WireKindUpstream, Cause: WireCauseTimeout, Emitted: true})
	})
	in := coddyInput(remote)
	in.RetryMax, in.RetryBase = 3, time.Millisecond
	got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
	if IsTransientProviderError(got.err) || isRetryableLLMError(got.err) {
		t.Fatalf("a timeout is retried by nobody: %v", got.err)
	}
	if !IsStreamTruncated(got.err) || got.resp == nil || got.resp.Content != "half" {
		t.Fatalf("the partial answer is kept like a truncated stream: %+v %v", got.resp, got.err)
	}
	if remote.count() != 1 {
		t.Fatalf("%d requests", remote.count())
	}
}

func TestCoddyHopAnswersBeforeOutputAreClassifiedByStatus(t *testing.T) {
	hop := func(w http.ResponseWriter, status int, node, reason string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "swarm node " + node + ": " + reason, "node": node, "reason": reason}})
	}
	t.Run("a relay 502 is retried", func(t *testing.T) {
		remote := newFakeRemote(t, func(w http.ResponseWriter, r *http.Request, n int, req WireRequest) {
			if n < 3 {
				hop(w, http.StatusBadGateway, "n1", "node is registered but not reachable")
				return
			}
			simpleAnswer("ok")(w, r, n, req)
		})
		in := coddyInput(remote)
		in.RetryMax, in.RetryBase = 3, time.Millisecond
		got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
		if got.err != nil || got.resp.Content != "ok" || remote.count() != 3 {
			t.Fatalf("resp %+v err %v after %d requests", got.resp, got.err, remote.count())
		}
	})
	t.Run("a relay 502 that never recovers costs RetryMax+1", func(t *testing.T) {
		remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
			hop(w, http.StatusBadGateway, "n1", "node is registered but not reachable")
		})
		in := coddyInput(remote)
		in.RetryMax, in.RetryBase = 2, time.Millisecond
		got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
		if got.err == nil || !strings.Contains(got.err.Error(), "n1") || remote.count() != 3 {
			t.Fatalf("err %v after %d requests", got.err, remote.count())
		}
	})
	t.Run("a relay 404 hop error is invalid and not retried", func(t *testing.T) {
		remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
			hop(w, http.StatusNotFound, "zz", "no such node in this relay")
		})
		in := coddyInput(remote)
		in.RetryMax, in.RetryBase = 3, time.Millisecond
		got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
		if CoddyErrorKind(got.err) != WireKindInvalid || remote.count() != 1 {
			t.Fatalf("err %v (%q) after %d requests", got.err, CoddyErrorKind(got.err), remote.count())
		}
	})
	t.Run("a plain-text 401 is auth", func(t *testing.T) {
		remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		})
		in := coddyInput(remote)
		in.RetryMax, in.RetryBase = 3, time.Millisecond
		got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
		if CoddyErrorKind(got.err) != WireKindAuth || remote.count() != 1 {
			t.Fatalf("err %v after %d requests", got.err, remote.count())
		}
		if !strings.Contains(got.err.Error(), "refused the credential") {
			t.Fatalf("message: %v", got.err)
		}
	})
	t.Run("a gateway html 503 is retried", func(t *testing.T) {
		remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("<html><body><h1>503 Service Unavailable</h1></body></html>"))
		})
		in := coddyInput(remote)
		in.RetryMax, in.RetryBase = 1, time.Millisecond
		got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
		if got.err == nil || remote.count() != 2 {
			t.Fatalf("err %v after %d requests", got.err, remote.count())
		}
		if strings.Contains(got.err.Error(), "<html") {
			t.Fatalf("an HTML page is not an error message: %v", got.err)
		}
	})
	t.Run("any other status is not retried", func(t *testing.T) {
		for _, status := range []int{400, 413, 429, 500} {
			remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
				http.Error(w, "nope", status)
			})
			in := coddyInput(remote)
			in.RetryMax, in.RetryBase = 3, time.Millisecond
			got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
			if got.err == nil || remote.count() != 1 {
				t.Fatalf("status %d: err %v after %d requests", status, got.err, remote.count())
			}
			if IsTransientProviderError(got.err) {
				t.Fatalf("status %d: a plain answer must not start the agent's recovery", status)
			}
		}
	})
}

func TestCoddyAOKAnswerThatIsNotAnEventStreamIsTheNoSharedModelsError(t *testing.T) {
	for name, ct := range map[string]string{"html": "text/html; charset=utf-8", "json": "application/json", "none": ""} {
		t.Run(name, func(t *testing.T) {
			remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
				if ct != "" {
					w.Header().Set("Content-Type", ct)
				}
				_, _ = w.Write([]byte("<!doctype html><html><body>Coddy</body></html>"))
			})
			in := coddyInput(remote)
			in.RetryMax, in.RetryBase = 3, time.Millisecond
			got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
			if CoddyErrorKind(got.err) != CoddyKindUnsupported || !strings.Contains(got.err.Error(), "does not offer shared models") {
				t.Fatalf("err %v", got.err)
			}
			if remote.count() != 1 {
				t.Fatalf("not retried, but %d requests", remote.count())
			}
		})
	}
}

func TestCoddyFrameLimit(t *testing.T) {
	t.Run("one frame over 16 MiB", func(t *testing.T) {
		remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
			fw := startStream(w)
			fw.raw("data: {\"type\":\"chunk\",\"text_delta\":\"" + bigText(CoddyMaxFrameBytes+1024) + "\"}\n\n")
			fw.final(&Response{Content: "late"})
		})
		in := coddyInput(remote)
		in.RetryMax, in.RetryBase = 3, time.Millisecond
		got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
		if CoddyErrorKind(got.err) != CoddyKindProtocol || !strings.Contains(got.err.Error(), "16 MiB") {
			t.Fatalf("err %v", got.err)
		}
		if isRetryableLLMError(got.err) || remote.count() != 1 {
			t.Fatalf("the oversize frame is not retryable: %d requests", remote.count())
		}
		if len(got.chunks) != 0 {
			t.Fatal("an oversize frame is never delivered")
		}
	})
	t.Run("a frame built of many small lines", func(t *testing.T) {
		remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
			fw := startStream(w)
			line := "data: " + bigText(1000) + "\n"
			for i := 0; i < (CoddyMaxFrameBytes/len(line))+50; i++ {
				fw.raw(line)
			}
		})
		got := streamOnce(newTestCoddy(t, coddyInput(remote)), userMsg("Hi"), nil)
		if CoddyErrorKind(got.err) != CoddyKindProtocol {
			t.Fatalf("err %v", got.err)
		}
	})
	t.Run("a frame just under the limit is read", func(t *testing.T) {
		remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
			fw := startStream(w)
			fw.final(&Response{Content: bigText(CoddyMaxFrameBytes - 4096), StopReason: "end_turn"})
		})
		got := streamOnce(newTestCoddy(t, coddyInput(remote)), userMsg("Hi"), nil)
		if got.err != nil || len(got.resp.Content) != CoddyMaxFrameBytes-4096 {
			t.Fatalf("err %v", got.err)
		}
	})
}

func TestCoddyUndecodableFrames(t *testing.T) {
	t.Run("not JSON is final", func(t *testing.T) {
		remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
			startStream(w).raw("data: this is not json\n\n")
		})
		in := coddyInput(remote)
		in.RetryMax, in.RetryBase = 3, time.Millisecond
		got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
		var und *streamUndecodableError
		if !errors.As(got.err, &und) || remote.count() != 1 {
			t.Fatalf("err %v after %d requests", got.err, remote.count())
		}
	})
	t.Run("cut inside its JSON is a truncation", func(t *testing.T) {
		remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
			startStream(w).raw(`data: {"type":"chunk","text_delta":"abc`)
		})
		in := coddyInput(remote)
		in.RetryMax, in.RetryBase = 1, time.Millisecond
		got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
		if !IsStreamTruncated(got.err) || remote.count() != 2 {
			t.Fatalf("err %v after %d requests", got.err, remote.count())
		}
	})
	t.Run("an unknown frame type is ignored", func(t *testing.T) {
		remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
			fw := startStream(w)
			fw.raw(`data: {"type":"future","x":1}` + "\n\n")
			fw.final(&Response{Content: "ok", StopReason: "end_turn"})
		})
		got := streamOnce(newTestCoddy(t, coddyInput(remote)), userMsg("Hi"), nil)
		if got.err != nil || got.resp.Content != "ok" {
			t.Fatalf("resp %+v err %v", got.resp, got.err)
		}
	})
}

func TestCoddyStopCancelsTheStreamAndKeepsWhatArrived(t *testing.T) {
	gate := make(chan struct{})
	remote := newFakeRemote(t, func(w http.ResponseWriter, r *http.Request, _ int, _ WireRequest) {
		fw := startStream(w)
		fw.text("so far")
		select {
		case <-r.Context().Done():
		case <-gate:
		}
	})
	defer close(gate)
	ctx, cancel := context.WithCancel(context.Background())
	seen := make(chan struct{}, 1)
	done := make(chan collected, 1)
	go func() {
		var c collected
		c.resp, c.err = newTestCoddy(t, coddyInput(remote)).Stream(ctx, userMsg("Hi"), nil, func(ch StreamChunk) {
			c.chunks = append(c.chunks, ch)
			select {
			case seen <- struct{}{}:
			default:
			}
		})
		done <- c
	}()
	<-seen
	cancel()
	select {
	case got := <-done:
		if !errors.Is(got.err, context.Canceled) {
			t.Fatalf("err %v", got.err)
		}
		if got.resp == nil || got.resp.Content != "so far" {
			t.Fatalf("what arrived before Stop is kept: %+v", got.resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not end the call")
	}
}

// What the byte-level liveness guard of the HTTP client sees: every byte
// counts, comments included.
func TestCoddyLivenessGuardCountsHeartbeatsAndCutsSilence(t *testing.T) {
	t.Run("a remote that only heartbeats is not cut", func(t *testing.T) {
		remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
			fw := startStream(w)
			for i := 0; i < 8; i++ {
				fw.heartbeat()
				time.Sleep(40 * time.Millisecond)
			}
			fw.text("late but alive")
			fw.final(&Response{Content: "late but alive", StopReason: "end_turn"})
		})
		in := coddyInput(remote)
		in.StreamIdleTimeout = 150 * time.Millisecond
		got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
		if got.err != nil || got.resp.Content != "late but alive" {
			t.Fatalf("resp %+v err %v", got.resp, got.err)
		}
	})
	t.Run("silence before output is cut and repeated", func(t *testing.T) {
		remote := newFakeRemote(t, func(w http.ResponseWriter, r *http.Request, n int, _ WireRequest) {
			fw := startStream(w)
			fw.heartbeat()
			if n == 1 {
				<-r.Context().Done()
				return
			}
			fw.text("ok")
			fw.final(&Response{Content: "ok", StopReason: "end_turn"})
		})
		in := coddyInput(remote)
		in.StreamIdleTimeout = 100 * time.Millisecond
		in.RetryMax, in.RetryBase = 2, time.Millisecond
		got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
		if got.err != nil || got.resp.Content != "ok" || remote.count() != 2 {
			t.Fatalf("resp %+v err %v after %d requests", got.resp, got.err, remote.count())
		}
	})
	t.Run("silence after output keeps the text and is not repeated", func(t *testing.T) {
		remote := newFakeRemote(t, func(w http.ResponseWriter, r *http.Request, _ int, _ WireRequest) {
			fw := startStream(w)
			fw.text("so far")
			<-r.Context().Done()
		})
		in := coddyInput(remote)
		in.StreamIdleTimeout = 100 * time.Millisecond
		in.RetryMax, in.RetryBase = 3, time.Millisecond
		got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
		if !IsStreamStalled(got.err) || got.resp == nil || got.resp.Content != "so far" {
			t.Fatalf("resp %+v err %v", got.resp, got.err)
		}
		if remote.count() != 1 {
			t.Fatalf("a stall after output is not repeated: %d requests", remote.count())
		}
	})
	t.Run("a row with local stream false still has the idle guard", func(t *testing.T) {
		remote := newFakeRemote(t, func(w http.ResponseWriter, r *http.Request, _ int, _ WireRequest) {
			startStream(w).heartbeat()
			<-r.Context().Done()
		})
		in := coddyInput(remote)
		in.DisableStream = true
		in.StreamIdleTimeout = 100 * time.Millisecond
		in.RetryMax, in.RetryDisabled = 0, true
		got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
		if !IsStreamStalled(got.err) {
			t.Fatalf("err %v", got.err)
		}
	})
	t.Run("a remote whose backoff outlasts the client idle timeout is fine while it beats", func(t *testing.T) {
		remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
			fw := startStream(w)
			for i := 0; i < 12; i++ {
				time.Sleep(30 * time.Millisecond)
				fw.heartbeat()
			}
			fw.final(&Response{Content: "ok", StopReason: "end_turn"})
		})
		in := coddyInput(remote)
		in.StreamIdleTimeout = 100 * time.Millisecond
		got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
		if got.err != nil || got.resp.Content != "ok" {
			t.Fatalf("err %v", got.err)
		}
	})
}

// The first-token timer lives in the agent and is not armed for a coddy row,
// but the provider is the other half: nothing in it bounds the wait for the
// first chunk except the liveness guard and the request bound.
func TestCoddyWaitsForASlowFirstChunkWhileTheRemoteBeats(t *testing.T) {
	remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
		fw := startStream(w)
		for i := 0; i < 5; i++ {
			time.Sleep(60 * time.Millisecond)
			fw.heartbeat()
		}
		fw.text("answer")
		fw.final(&Response{Content: "answer", StopReason: "end_turn"})
	})
	in := coddyInput(remote)
	in.StreamIdleTimeout = 200 * time.Millisecond
	in.CallBudget = 0
	got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
	if got.err != nil || got.resp.Content != "answer" {
		t.Fatalf("err %v", got.err)
	}
}

func TestCoddyRequestBoundCutsARemoteThatNeverAnswers(t *testing.T) {
	t.Run("no headers", func(t *testing.T) {
		release := make(chan struct{})
		remote := newFakeRemote(t, func(w http.ResponseWriter, r *http.Request, _ int, _ WireRequest) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		})
		defer close(release)
		cp, err := newCoddyProvider(coddyInput(remote), remote.srv.Client())
		if err != nil {
			t.Fatal(err)
		}
		cp.requestBound = 150 * time.Millisecond
		start := time.Now()
		_, err = cp.Stream(context.Background(), userMsg("Hi"), nil, nil)
		var bound *coddyBoundError
		if !errors.As(err, &bound) {
			t.Fatalf("err %T %v", err, err)
		}
		if time.Since(start) > 3*time.Second {
			t.Fatalf("the bound did not cut the call: %v", time.Since(start))
		}
		if !isRetryableLLMError(err) {
			t.Fatal("an unanswered request is retried like a silent stream")
		}
	})
	t.Run("a peer that stops reading the upload", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = ln.Close() }()
		var mu sync.Mutex
		var conns []net.Conn
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				mu.Lock()
				conns = append(conns, c) // never read, never write
				mu.Unlock()
			}
		}()
		defer func() {
			mu.Lock()
			defer mu.Unlock()
			for _, c := range conns {
				_ = c.Close()
			}
		}()
		in := ProviderInput{Name: "remote", Type: "coddy", Model: "coder", BaseURL: "http://" + ln.Addr().String(), ProxyURL: "none"}
		cp, err := newCoddyProvider(in, mustProviderClient(t))
		if err != nil {
			t.Fatal(err)
		}
		cp.requestBound = 300 * time.Millisecond
		start := time.Now()
		_, err = cp.Stream(context.Background(), []Message{{Role: RoleUser, Content: bigText(16 << 20)}}, nil, nil)
		var bound *coddyBoundError
		if !errors.As(err, &bound) {
			t.Fatalf("err %T %v", err, err)
		}
		if time.Since(start) > 5*time.Second {
			t.Fatalf("a peer that stops reading was not cut by R: %v", time.Since(start))
		}
	})
	t.Run("Stop is not reported as the bound", func(t *testing.T) {
		release := make(chan struct{})
		remote := newFakeRemote(t, func(w http.ResponseWriter, r *http.Request, _ int, _ WireRequest) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		})
		defer close(release)
		cp, _ := newCoddyProvider(coddyInput(remote), remote.srv.Client())
		ctx, cancel := context.WithCancel(context.Background())
		go func() { time.Sleep(50 * time.Millisecond); cancel() }()
		_, err := cp.Stream(ctx, userMsg("Hi"), nil, nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("an answered request is not cut by the bound while it streams", func(t *testing.T) {
		remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
			fw := startStream(w)
			time.Sleep(300 * time.Millisecond)
			fw.text("slow")
			fw.final(&Response{Content: "slow", StopReason: "end_turn"})
		})
		cp, _ := newCoddyProvider(coddyInput(remote), remote.srv.Client())
		cp.requestBound = 100 * time.Millisecond
		resp, err := cp.Stream(context.Background(), userMsg("Hi"), nil, nil)
		if err != nil || resp.Content != "slow" {
			t.Fatalf("resp %+v err %v", resp, err)
		}
	})
}

func TestCoddyRefusesARequestOverTheBodyLimitWithoutSendingIt(t *testing.T) {
	remote := newFakeRemote(t, simpleAnswer("ok"))
	got := streamOnce(newTestCoddy(t, coddyInput(remote)), []Message{{Role: RoleUser, Content: bigText(CoddyMaxRequestBytes + 1)}}, nil)
	if CoddyErrorKind(got.err) != WireKindInvalid || remote.count() != 0 {
		t.Fatalf("err %v, %d requests", got.err, remote.count())
	}
}

func TestCoddyALongStreamOfNormalFramesIsNotLimitedByTheFrameSize(t *testing.T) {
	remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
		fw := startStream(w)
		piece := bigText(1 << 20)
		for i := 0; i < 20; i++ {
			fw.text(piece)
		}
		fw.final(&Response{Content: "ok", StopReason: "end_turn"})
	})
	var seen int
	resp, err := newTestCoddy(t, coddyInput(remote)).Stream(context.Background(), userMsg("Hi"), nil, func(c StreamChunk) { seen += len(c.TextDelta) })
	if err != nil || resp.Content != "ok" || seen != 20<<20 {
		t.Fatalf("err %v, saw %d bytes: the limit is per event, not per stream", err, seen)
	}
}

func TestCoddyAConnectionThatCannotBeMadeIsRetriedLikeAnyTransportFailure(t *testing.T) {
	remote := newFakeRemote(t, simpleAnswer("ok"))
	in := coddyInput(remote)
	in.RetryMax, in.RetryBase = 2, time.Millisecond
	remote.srv.Close()
	allowance := NewRetryAllowance(10)
	got := streamCtx(WithRetryAllowance(context.Background(), allowance), newTestCoddy(t, in), userMsg("Hi"), nil)
	if got.err == nil || got.resp != nil {
		t.Fatalf("resp %+v err %v", got.resp, got.err)
	}
	if snap := allowance.Snapshot(); snap.Attempts != 3 || snap.TransportRetries != 2 {
		t.Fatalf("a refused connection is tried RetryMax+1 times: %+v", snap)
	}
	if CoddyErrorKind(got.err) != "" {
		t.Fatalf("a transport failure is not a coddy answer: %v", got.err)
	}
}

// The client counts the chunks it passed on itself and never trusts the frame.
func TestCoddyEmittedIsTheClientsOwnCount(t *testing.T) {
	t.Run("the frame denies output the client delivered", func(t *testing.T) {
		remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
			fw := startStream(w)
			fw.text("shown")
			fw.fail(WireError{Status: 502, Kind: WireKindUpstream, Cause: WireCauseStatus, Emitted: false})
		})
		got := streamOnce(newTestCoddy(t, coddyInput(remote)), userMsg("Hi"), nil)
		if !IsStreamTruncated(got.err) {
			t.Fatalf("the text was shown, so the partial answer is kept: %v", got.err)
		}
	})
	t.Run("the frame claims output nobody was given", func(t *testing.T) {
		remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
			startStream(w).fail(WireError{Status: 502, Kind: WireKindUpstream, Cause: WireCauseStatus, Emitted: true})
		})
		got := streamOnce(newTestCoddy(t, coddyInput(remote)), userMsg("Hi"), nil)
		if IsStreamTruncated(got.err) {
			t.Fatalf("nothing reached the caller: %v", got.err)
		}
	})
}

func TestCoddyCauseCountsForTheUpstreamKindOnly(t *testing.T) {
	remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
		startStream(w).fail(WireError{Status: 429, Kind: WireKindRate, Cause: WireCauseStall, RetryAfterS: 2})
	})
	got := streamOnce(newTestCoddy(t, coddyInput(remote)), userMsg("Hi"), nil)
	if IsStreamStalled(got.err) || IsTransientProviderError(got.err) {
		t.Fatalf("a rate error is never a stall: %v", got.err)
	}
	if d, ok := UpstreamRetryAfter(got.err); !ok || d != 2*time.Second {
		t.Fatalf("the pause of a rate error reaches the agent's recovery delay: %v %v", d, ok)
	}
}

func TestCoddyProtocolMismatchIsTheRemotesInvalidAnswerAsItIs(t *testing.T) {
	remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
		answerWire(w, http.StatusBadRequest, WireError{Status: 400, Kind: WireKindInvalid, Code: WireCodeProtocolMismatch,
			Message: "this remote speaks protocol 2, the request is protocol 1"})
	})
	got := streamOnce(newTestCoddy(t, coddyInput(remote)), userMsg("Hi"), nil)
	if CoddyErrorCode(got.err) != WireCodeProtocolMismatch || !strings.Contains(got.err.Error(), "protocol 2") {
		t.Fatalf("err %v", got.err)
	}
}

func TestCoddyACredentialInAPIBaseNeverReachesAnError(t *testing.T) {
	remote := newFakeRemote(t, simpleAnswer("ok"))
	remote.srv.Close()
	in := coddyInput(remote)
	in.BaseURL = strings.Replace(remote.url(), "http://", "http://user:hunter2@", 1)
	in.RetryDisabled = true
	got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
	if got.err == nil || strings.Contains(got.err.Error(), "hunter2") {
		t.Fatalf("err %v", got.err)
	}
	if _, err := ListModels(context.Background(), in); err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("list err %v", err)
	}
}

// After the headers the request bound R is finished with, and the byte guard
// of the client watches event streams only, so the body of a refusal, of a
// gateway's answer or of a 200 that is not a stream is read under a bound of
// its own: a hop that sends a few bytes of a long body and stalls ends the call
// at that bound, not when the context does.
func TestCoddyAnAnswerThatStallsMidBodyIsCutByTheBodyBound(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		ctype  string
	}{
		{"busy answer", http.StatusTooManyRequests, "application/json"},
		{"gateway answer", http.StatusBadGateway, "text/html"},
		{"success that is not a stream", http.StatusOK, "text/html"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := make(chan struct{})
			remote := newFakeRemote(t, func(w http.ResponseWriter, r *http.Request, _ int, _ WireRequest) {
				w.Header().Set("Content-Type", tc.ctype)
				w.Header().Set("Content-Length", "1000")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte("0123456789a"))
				w.(http.Flusher).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
				}
			})
			t.Cleanup(func() { close(release) })
			cp, err := newCoddyProvider(coddyInput(remote), remote.srv.Client())
			if err != nil {
				t.Fatal(err)
			}
			cp.answerBound = 200 * time.Millisecond

			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			start := time.Now()
			_, err = cp.Stream(ctx, userMsg("Hi"), nil, nil)

			var bound *coddyBoundError
			if !errors.As(err, &bound) {
				t.Fatalf("err %T %v after %v, want the bound error", err, err, time.Since(start))
			}
			if bound.after != 200*time.Millisecond {
				t.Errorf("the bound reports %v", bound.after)
			}
			if took := time.Since(start); took > 2*time.Second {
				t.Fatalf("the stalled answer held the call for %v", took)
			}
			if remote.count() != 1 {
				t.Fatalf("%d requests", remote.count())
			}
		})
	}

	t.Run("a slow but finished answer is read whole", func(t *testing.T) {
		remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"kind":"invalid",`))
			w.(http.Flusher).Flush()
			time.Sleep(100 * time.Millisecond)
			_, _ = w.Write([]byte(`"message":"read to the end"}`))
		})
		cp, _ := newCoddyProvider(coddyInput(remote), remote.srv.Client())
		cp.answerBound = 2 * time.Second
		_, err := cp.Stream(context.Background(), userMsg("Hi"), nil, nil)
		if CoddyErrorKind(err) != WireKindInvalid || !strings.Contains(err.Error(), "read to the end") {
			t.Fatalf("err %v", err)
		}
	})

	t.Run("Stop during the body is the context's error", func(t *testing.T) {
		release := make(chan struct{})
		remote := newFakeRemote(t, func(w http.ResponseWriter, r *http.Request, _ int, _ WireRequest) {
			w.Header().Set("Content-Length", "1000")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("0123456789a"))
			w.(http.Flusher).Flush()
			select {
			case <-release:
			case <-r.Context().Done():
			}
		})
		t.Cleanup(func() { close(release) })
		cp, _ := newCoddyProvider(coddyInput(remote), remote.srv.Client())
		ctx, cancel := context.WithCancel(context.Background())
		go func() { time.Sleep(100 * time.Millisecond); cancel() }()
		_, err := cp.Stream(ctx, userMsg("Hi"), nil, nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err %T %v", err, err)
		}
	})
}

// An error frame that names a kind outside the wire's six is the error of an
// answer no rule classifies, and no control character of its fields reaches the
// text a terminal or a log would show.
func TestCoddyAnErrorFrameOfAHostileRemoteCarriesNoControlCharacters(t *testing.T) {
	// The JSON escapes of the two control characters, as a remote would write them.
	const hostile = `\u001b]0;pwned\u0007`
	for name, frame := range map[string]string{
		"kind":    `{"type":"error","kind":"` + hostile + `","message":"` + hostile + `"}`,
		"cause":   `{"type":"error","kind":"upstream","cause":"` + hostile + `"}`,
		"code":    `{"type":"error","kind":"invalid","code":"` + hostile + `"}`,
		"message": `{"type":"error","kind":"invalid","message":"` + hostile + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
				startStream(w).raw("data: " + frame + "\n\n")
			})
			in := coddyInput(remote)
			in.RetryMax, in.RetryBase = 3, time.Millisecond
			got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
			if got.err == nil {
				t.Fatal("no error")
			}
			if strings.ContainsAny(got.err.Error(), "\x1b\x07") || strings.ContainsAny(CoddyErrorKind(got.err)+CoddyErrorCode(got.err), "\x1b\x07") {
				t.Fatalf("a control character reaches the error: %q kind %q code %q", got.err.Error(), CoddyErrorKind(got.err), CoddyErrorCode(got.err))
			}
			if name == "kind" && CoddyErrorKind(got.err) != CoddyKindOther {
				t.Fatalf("kind %q, want %q", CoddyErrorKind(got.err), CoddyKindOther)
			}
			if remote.count() != 1 {
				t.Fatalf("%d requests: a coddy error is never retried", remote.count())
			}
		})
	}
}
