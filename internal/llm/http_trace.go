package llm

import (
	"context"
	"net/http"
	"net/http/httptrace"
	"sync"
	"sync/atomic"
	"time"
)

const maxHTTPTraceRequests = 8

type httpTraceKey struct{}
type sdkPreparationKey struct{}

// HTTPRequestTiming contains transport stages only, without request or response content.
// Stage durations are milliseconds from RoundTrip entry, except ConnectionMS,
// which measures connection acquisition, and SDKPrepareMS, which precedes entry.
// Transport retries within one RoundTrip share one record and one SDK preparation
// interval. Connection fields describe the latest acquisition; WroteRequestMS
// describes the latest successful write and stays nil if every write failed.
type HTTPRequestTiming struct {
	StartMS        float64  `json:"start_ms"`
	RequestBytes   int64    `json:"request_bytes"`
	SDKPrepareMS   *float64 `json:"sdk_prepare_ms,omitempty"`
	ConnectionMS   *float64 `json:"connection_ms,omitempty"`
	WroteRequestMS *float64 `json:"wrote_request_ms,omitempty"`
	FirstByteMS    *float64 `json:"first_byte_ms,omitempty"`
	RoundTripMS    float64  `json:"round_trip_ms"`
	StatusCode     int      `json:"status_code"`
	Protocol       string   `json:"protocol"`
	Reused         *bool    `json:"reused,omitempty"`
}

// HTTPTraceSnapshot is an independent copy of a collector's bounded records.
type HTTPTraceSnapshot struct {
	Requests        []HTTPRequestTiming `json:"requests"`
	TotalRequests   int                 `json:"total_requests"`
	DroppedRequests int                 `json:"dropped_requests"`
}

// HTTPTrace collects HTTP timing metadata for requests carrying its context.
// It is safe for concurrent requests, trace callbacks and snapshots.
type HTTPTrace struct {
	mu       sync.Mutex
	begin    time.Time
	requests []HTTPRequestTiming
	total    int
}

// WithHTTPTrace starts a collector scoped to the returned context.
func WithHTTPTrace(ctx context.Context) (context.Context, *HTTPTrace) {
	trace := &HTTPTrace{begin: time.Now(), requests: make([]HTTPRequestTiming, 0, maxHTTPTraceRequests)}
	return context.WithValue(ctx, httpTraceKey{}, trace), trace
}

// Snapshot returns owned records and optional values that later callbacks cannot change.
func (t *HTTPTrace) Snapshot() HTTPTraceSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	records := make([]HTTPRequestTiming, len(t.requests))
	for i, r := range t.requests {
		r.SDKPrepareMS = copyTimingValue(r.SDKPrepareMS)
		r.ConnectionMS = copyTimingValue(r.ConnectionMS)
		r.WroteRequestMS = copyTimingValue(r.WroteRequestMS)
		r.FirstByteMS = copyTimingValue(r.FirstByteMS)
		r.Reused = copyTimingValue(r.Reused)
		records[i] = r
	}
	return HTTPTraceSnapshot{Requests: records, TotalRequests: t.total, DroppedRequests: t.total - len(records)}
}

func copyTimingValue[T any](v *T) *T {
	if v == nil {
		return nil
	}
	copy := *v
	return &copy
}

func timingMS(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

type sdkPreparation struct {
	begin   time.Time
	claimed atomic.Bool
}

// withSDKPreparation measures SDK/client preparation, including encoding, not
// pure marshaling. A redirect shares the marker but cannot claim it again.
func withSDKPreparation(ctx context.Context) context.Context {
	if _, ok := ctx.Value(httpTraceKey{}).(*HTTPTrace); !ok {
		return ctx
	}
	return context.WithValue(ctx, sdkPreparationKey{}, &sdkPreparation{begin: time.Now()})
}

type httpTraceTransport struct{ inner http.RoundTripper }

func (t *httpTraceTransport) CloseIdleConnections() {
	if inner, ok := t.inner.(interface{ CloseIdleConnections() }); ok {
		inner.CloseIdleConnections()
	}
}

func (t *httpTraceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	collector, ok := req.Context().Value(httpTraceKey{}).(*HTTPTrace)
	if !ok {
		return t.inner.RoundTrip(req)
	}
	start := time.Now()
	collector.mu.Lock()
	collector.total++
	if len(collector.requests) >= maxHTTPTraceRequests {
		collector.mu.Unlock()
		return t.inner.RoundTrip(req)
	}
	index := len(collector.requests)
	record := HTTPRequestTiming{StartMS: timingMS(start.Sub(collector.begin)), RequestBytes: req.ContentLength}
	if prep, ok := req.Context().Value(sdkPreparationKey{}).(*sdkPreparation); ok && prep.claimed.CompareAndSwap(false, true) {
		ms := timingMS(start.Sub(prep.begin))
		record.SDKPrepareMS = &ms
	}
	collector.requests = append(collector.requests, record)
	collector.mu.Unlock()
	var connectionStart time.Time
	callbacks := &httptrace.ClientTrace{
		GetConn: func(string) {
			collector.mu.Lock()
			connectionStart = time.Now()
			collector.mu.Unlock()
		},
		GotConn: func(info httptrace.GotConnInfo) {
			collector.mu.Lock()
			defer collector.mu.Unlock()
			r := &collector.requests[index]
			if !connectionStart.IsZero() {
				ms := timingMS(time.Since(connectionStart))
				r.ConnectionMS = &ms
			}
			reused := info.Reused
			r.Reused = &reused
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err != nil {
				return
			}
			collector.mu.Lock()
			defer collector.mu.Unlock()
			ms := timingMS(time.Since(start))
			collector.requests[index].WroteRequestMS = &ms
		},
		GotFirstResponseByte: func() {
			collector.mu.Lock()
			defer collector.mu.Unlock()
			r := &collector.requests[index]
			if r.FirstByteMS == nil {
				ms := timingMS(time.Since(start))
				r.FirstByteMS = &ms
			}
		},
	}
	ctx := httptrace.WithClientTrace(req.Context(), callbacks)
	resp, err := t.inner.RoundTrip(req.WithContext(ctx))
	elapsed := timingMS(time.Since(start))
	collector.mu.Lock()
	r := &collector.requests[index]
	r.RoundTripMS = elapsed
	if resp != nil {
		r.StatusCode = resp.StatusCode
		r.Protocol = resp.Proto
	}
	collector.mu.Unlock()
	return resp, err
}
