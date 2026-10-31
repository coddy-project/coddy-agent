package agent

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

var requestCallSequence atomic.Uint64

// requestLatency belongs to one loop request, including transport retries. It
// retains fixed-size clocks only, plus the HTTP collector's bounded records.
// A nil receiver is the disabled path: no collector, clock reads or trace logs.
type requestLatency struct {
	log                               *slog.Logger
	session                           string
	id                                uint64
	contextRefresh                    time.Duration
	projection, pairing               time.Duration
	start, returned                   time.Time
	meaningful, reasoning, text, tool time.Time
	http                              *llm.HTTPTrace
	dispatched                        bool
}

func (a *Agent) newRequestLatency(ctx context.Context) *requestLatency {
	if a.log == nil || !a.log.Enabled(ctx, slog.LevelDebug) {
		return nil
	}
	return &requestLatency{log: a.log, session: a.state.GetID(), id: requestCallSequence.Add(1)}
}
func (d *requestLatency) clock() time.Time {
	if d == nil {
		return time.Time{}
	}
	return time.Now()
}
func (d *requestLatency) addContext(start time.Time) {
	if d != nil {
		d.contextRefresh += time.Since(start)
	}
}
func (d *requestLatency) projected(start time.Time) {
	if d != nil {
		d.projection = time.Since(start)
	}
}
func (d *requestLatency) paired(start time.Time) {
	if d != nil {
		d.pairing = time.Since(start)
	}
}
func (d *requestLatency) begin(ctx context.Context, agent bool) context.Context {
	if d == nil {
		return ctx
	}
	d.start = time.Now()
	// An ACP provider is an entire agent, not an HTTP model request.
	if !agent {
		ctx, d.http = llm.WithHTTPTrace(ctx)
	}
	return ctx
}
func (d *requestLatency) chunk(c llm.StreamChunk, now time.Time) {
	if d == nil {
		return
	}
	if c.ReasoningDelta != "" && d.reasoning.IsZero() {
		d.reasoning = now
	}
	if c.TextDelta != "" && d.text.IsZero() {
		d.text = now
	}
	if (c.ToolCall != nil || c.ToolCallNamed != nil || c.ToolCallDelta != nil || c.AgentTool != nil) && d.tool.IsZero() {
		d.tool = now
	}
	if d.meaningful.IsZero() && (!d.reasoning.IsZero() || !d.text.IsZero() || !d.tool.IsZero()) {
		d.meaningful = now
	}
}
func latencyMS(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
func (d *requestLatency) callID() uint64 {
	if d == nil {
		return 0
	}
	return d.id
}

func (d *requestLatency) attrs() []any {
	if d == nil {
		return nil
	}
	return []any{"session", d.session, "call_id", d.id}
}
func (d *requestLatency) finish(ctx context.Context) {
	if d == nil {
		return
	}
	d.returned = time.Now()
	attrs := append(d.attrs(), "context_refresh_ms", latencyMS(d.contextRefresh), "outbound_projection_ms", latencyMS(d.projection), "pairing_ms", latencyMS(d.pairing), "model_call_ms", latencyMS(d.returned.Sub(d.start)))
	for _, m := range []struct {
		name string
		at   time.Time
	}{
		{"first_meaningful_ms", d.meaningful}, {"first_reasoning_ms", d.reasoning}, {"first_text_ms", d.text}, {"first_tool_ms", d.tool},
	} {
		if !m.at.IsZero() {
			attrs = append(attrs, m.name, latencyMS(m.at.Sub(d.start)))
		}
	}
	snapshot := llm.HTTPTraceSnapshot{}
	if d.http != nil {
		snapshot = d.http.Snapshot()
	}
	attrs = append(attrs, "http_requests", snapshot.TotalRequests, "http_requests_dropped", snapshot.DroppedRequests)
	d.log.DebugContext(ctx, "llm request latency", attrs...)
	for i, r := range snapshot.Requests {
		fields := append(d.attrs(), "request_index", i+1, "start_ms", r.StartMS, "request_bytes", r.RequestBytes, "round_trip_ms", r.RoundTripMS)
		if r.StatusCode > 0 {
			fields = append(fields, "status_code", r.StatusCode)
		}
		// Protocol is a closed transport value, never a server-provided string.
		switch r.Protocol {
		case "HTTP/1.0", "HTTP/1.1", "HTTP/2.0", "HTTP/3.0":
			fields = append(fields, "protocol", r.Protocol)
		}
		for _, m := range []struct {
			name  string
			value *float64
		}{
			{"sdk_prepare_ms", r.SDKPrepareMS}, {"connection_ms", r.ConnectionMS}, {"wrote_request_ms", r.WroteRequestMS}, {"first_byte_ms", r.FirstByteMS},
		} {
			if m.value != nil {
				fields = append(fields, m.name, *m.value)
			}
		}
		if r.Reused != nil {
			fields = append(fields, "reused", *r.Reused)
		}
		d.log.DebugContext(ctx, "llm http request latency", fields...)
	}
}

type requestLatencyDispatchKey struct{}

func (d *requestLatency) dispatchContext(ctx context.Context) context.Context {
	if d == nil {
		return ctx
	}
	return context.WithValue(ctx, requestLatencyDispatchKey{}, d)
}

// noteRequestToolDispatch is called at executeToolCall entry, before restriction,
// hook and permission checks. Only the first non-loop-skipped call is measured:
// the gap includes post-response accounting, persistence and rule discovery,
// not the tool's execution or any wait inside its execution boundary.
func noteRequestToolDispatch(ctx context.Context) {
	d, _ := ctx.Value(requestLatencyDispatchKey{}).(*requestLatency)
	if d == nil || d.dispatched || d.returned.IsZero() {
		return
	}
	d.dispatched = true
	d.log.DebugContext(ctx, "llm tool dispatch latency", append(d.attrs(), "completion_to_dispatch_ms", latencyMS(time.Since(d.returned)))...)
}
