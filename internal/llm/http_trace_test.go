package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type diagnosticRoundTripFunc func(*http.Request) (*http.Response, error)

func (f diagnosticRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHTTPTraceTransparency(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[enabled], func(t *testing.T) {
			ctx := context.Background()
			if enabled {
				ctx, _ = WithHTTPTrace(ctx)
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unused.invalid/private", strings.NewReader("exact body"))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "secret")
			headers := req.Header.Clone()
			response := &http.Response{StatusCode: 201, Body: io.NopCloser(strings.NewReader("response"))}
			sentinel := errors.New("transport error")
			rt := &httpTraceTransport{inner: diagnosticRoundTripFunc(func(got *http.Request) (*http.Response, error) {
				if !enabled && got != req {
					t.Error("off trace cloned request")
				}
				if got.Body != req.Body || !reflect.DeepEqual(got.Header, headers) || got.URL != req.URL {
					t.Error("request changed")
				}
				return response, sentinel
			})}
			got, err := rt.RoundTrip(req)
			if got != response || err != sentinel || !reflect.DeepEqual(req.Header, headers) {
				t.Fatal("response, error or headers changed")
			}
		})
	}
}

func TestHTTPTraceLocalRequestAndReuse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if string(body) != "payload" || r.Header.Get("X-Test") != "unchanged" {
			t.Error("wire content changed")
		}
		w.Header().Set("X-Reply", "unchanged")
		w.WriteHeader(202)
		if _, err := io.WriteString(w, "answer"); err != nil {
			t.Error(err)
		}
	}))
	defer srv.Close()
	for _, makeClient := range []func() (*http.Client, error){func() (*http.Client, error) { return HTTPClientForProviderProxy("none") }, func() (*http.Client, error) { return providerHTTPClient("none", 0, 0) }} {
		client, err := makeClient()
		if err != nil {
			t.Fatal(err)
		}
		ctx, trace := WithHTTPTrace(context.Background())
		var original atomic.Int32
		ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { original.Add(1) }})
		for i := 0; i < 2; i++ {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, strings.NewReader("payload"))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("X-Test", "unchanged")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if err := resp.Body.Close(); err != nil {
				t.Fatal(err)
			}
			if string(body) != "answer" || resp.Header.Get("X-Reply") != "unchanged" {
				t.Fatal("response changed")
			}
		}
		snap := trace.Snapshot()
		if snap.TotalRequests != 2 || len(snap.Requests) != 2 || original.Load() != 2 {
			t.Fatalf("trace/callbacks: %+v, %d", snap, original.Load())
		}
		for _, r := range snap.Requests {
			if r.RequestBytes != 7 || r.StatusCode != 202 || r.Protocol != "HTTP/1.1" || r.ConnectionMS == nil || r.FirstByteMS == nil || r.WroteRequestMS == nil || r.Reused == nil || r.SDKPrepareMS != nil {
				t.Fatalf("missing/wrong metadata: %+v", r)
			}
			if r.StartMS < 0 || r.RoundTripMS < *r.FirstByteMS {
				t.Fatalf("invalid timings: %+v", r)
			}
		}
		if !*snap.Requests[1].Reused {
			t.Fatal("second connection was not reused")
		}
	}
}

func TestHTTPTraceProxy(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "provider.invalid" {
			t.Errorf("proxy target %s", r.URL.Host)
		}
		w.WriteHeader(204)
	}))
	defer proxy.Close()
	client, err := HTTPClientForProviderProxy(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, trace := WithHTTPTrace(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://provider.invalid/secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if snap := trace.Snapshot(); snap.TotalRequests != 1 || snap.Requests[0].StatusCode != 204 {
		t.Fatalf("proxy trace: %+v", snap)
	}
}

func TestHTTPTraceMissingStagesCapAndCopies(t *testing.T) {
	ctx, trace := WithHTTPTrace(context.Background())
	ctx = withSDKPreparation(ctx)
	sentinel := errors.New("failure")
	var callbacks *httptrace.ClientTrace
	rt := &httpTraceTransport{inner: diagnosticRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		callbacks = httptrace.ContextClientTrace(r.Context())
		return nil, sentinel
	})}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unused.invalid", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = -1
	if _, err := rt.RoundTrip(req); err != sentinel {
		t.Fatal(err)
	}
	first := trace.Snapshot()
	r := first.Requests[0]
	if r.SDKPrepareMS == nil || r.RequestBytes != -1 || r.FirstByteMS != nil || r.WroteRequestMS != nil || r.ConnectionMS != nil || r.Reused != nil || r.StatusCode != 0 || r.Protocol != "" {
		t.Fatalf("fabricated stages: %+v", r)
	}
	callbacks.GotFirstResponseByte()
	if first.Requests[0].FirstByteMS != nil {
		t.Fatal("old snapshot mutated")
	}
	*first.Requests[0].SDKPrepareMS = -10
	for i := 0; i < 10; i++ {
		if _, err := rt.RoundTrip(req); err != sentinel {
			t.Fatal(err)
		}
	}
	snap := trace.Snapshot()
	if snap.TotalRequests != 11 || snap.DroppedRequests != 3 || len(snap.Requests) != 8 {
		t.Fatalf("cap: %+v", snap)
	}
	if *snap.Requests[0].SDKPrepareMS < 0 || snap.Requests[1].SDKPrepareMS != nil {
		t.Fatal("snapshot alias or repeated preparation")
	}
	snap.Requests[0].StatusCode = 999
	if trace.Snapshot().Requests[0].StatusCode != 0 {
		t.Fatal("slice aliased")
	}
	plain := context.Background()
	if withSDKPreparation(plain) != plain {
		t.Fatal("off preparation changed context")
	}
}

func TestHTTPTraceConcurrentCollectors(t *testing.T) {
	var wg sync.WaitGroup
	for n := 0; n < 10; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			ctx, trace := WithHTTPTrace(context.Background())
			rt := &httpTraceTransport{inner: diagnosticRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				ct := httptrace.ContextClientTrace(r.Context())
				var stages sync.WaitGroup
				for i := 0; i < 4; i++ {
					stages.Add(1)
					go func() {
						defer stages.Done()
						ct.GetConn("not retained")
						ct.GotConn(httptrace.GotConnInfo{Reused: true})
						ct.WroteRequest(httptrace.WroteRequestInfo{})
						ct.GotFirstResponseByte()
						_ = trace.Snapshot()
					}()
				}
				stages.Wait()
				return &http.Response{StatusCode: 200 + n, Proto: "HTTP/2.0"}, nil
			})}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unused.invalid", nil)
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := rt.RoundTrip(req); err != nil {
				t.Error(err)
			}
			snap := trace.Snapshot()
			if snap.TotalRequests != 1 || snap.Requests[0].StatusCode != 200+n {
				t.Errorf("collector crossed: %+v", snap)
			}
		}(n)
	}
	wg.Wait()
}

func TestHTTPTraceCanceled(t *testing.T) {
	ctx, trace := WithHTTPTrace(context.Background())
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	rt := &httpTraceTransport{inner: diagnosticRoundTripFunc(func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() })}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unused.invalid", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RoundTrip(req); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if r := trace.Snapshot().Requests[0]; r.FirstByteMS != nil || r.StatusCode != 0 {
		t.Fatalf("fabricated canceled metadata: %+v", r)
	}
}

func TestHTTPTraceCodexSDKPreparation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sseCompleted(w)
	}))
	defer srv.Close()
	p := newCodexTestProvider(t, srv.URL)
	client, err := providerHTTPClient("none", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	p.httpClient = client
	ctx, trace := WithHTTPTrace(context.Background())
	if _, err := p.Complete(ctx, []Message{{Role: RoleUser, Content: "hello"}}, nil); err != nil {
		t.Fatal(err)
	}
	snap := trace.Snapshot()
	if snap.TotalRequests != 1 || snap.Requests[0].SDKPrepareMS == nil || *snap.Requests[0].SDKPrepareMS < 0 {
		t.Fatalf("missing SDK preparation: %+v", snap)
	}
}

func TestHTTPTraceRedirectPreparation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/next", http.StatusTemporaryRedirect)
			return
		}
		w.WriteHeader(204)
	}))
	defer srv.Close()
	client, err := HTTPClientForProviderProxy("none")
	if err != nil {
		t.Fatal(err)
	}
	ctx, trace := WithHTTPTrace(context.Background())
	ctx = withSDKPreparation(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	snap := trace.Snapshot()
	if snap.TotalRequests != 2 || snap.Requests[0].SDKPrepareMS == nil || snap.Requests[1].SDKPrepareMS != nil {
		t.Fatalf("redirect preparation: %+v", snap)
	}
}

type diagnosticIdleTransport struct {
	diagnosticRoundTripFunc
	closed bool
}

func (t *diagnosticIdleTransport) CloseIdleConnections() { t.closed = true }

func TestHTTPTraceCloseIdleConnections(t *testing.T) {
	inner := &diagnosticIdleTransport{}
	client := &http.Client{Transport: &httpTraceTransport{inner: inner}}
	client.CloseIdleConnections()
	if !inner.closed {
		t.Fatal("wrapper hid idle connection cleanup")
	}
}

func TestHTTPTraceJSON(t *testing.T) {
	value := 1.0
	reused := false
	snapshot := HTTPTraceSnapshot{Requests: []HTTPRequestTiming{{StartMS: 1, RequestBytes: -1, SDKPrepareMS: &value, ConnectionMS: &value, WroteRequestMS: &value, FirstByteMS: &value, RoundTripMS: 2, StatusCode: 200, Protocol: "HTTP/2.0", Reused: &reused}}, TotalRequests: 1}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"requests":[{"start_ms":1,"request_bytes":-1,"sdk_prepare_ms":1,"connection_ms":1,"wrote_request_ms":1,"first_byte_ms":1,"round_trip_ms":2,"status_code":200,"protocol":"HTTP/2.0","reused":false}],"total_requests":1,"dropped_requests":0}`
	if string(raw) != want {
		t.Fatalf("JSON = %s, want %s", raw, want)
	}
}

func TestHTTPTraceWriteOutcomes(t *testing.T) {
	writeErr := errors.New("write failed")
	for _, tc := range []struct {
		name   string
		writes []error
	}{
		{name: "failed write", writes: []error{writeErr}},
		{name: "retry succeeds", writes: []error{writeErr, nil}},
		{name: "later failure preserves successful write", writes: []error{nil, writeErr}},
		{name: "multiple successful writes", writes: []error{nil, nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, trace := WithHTTPTrace(context.Background())
			ctx = withSDKPreparation(ctx)
			var observed []error
			ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
				WroteRequest: func(info httptrace.WroteRequestInfo) { observed = append(observed, info.Err) },
			})
			rt := &httpTraceTransport{inner: diagnosticRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				callbacks := httptrace.ContextClientTrace(r.Context())
				initial := trace.Snapshot()
				if initial.TotalRequests != 1 || initial.Requests[0].SDKPrepareMS == nil {
					t.Fatalf("missing initial preparation: %+v", initial)
				}
				var successfulWrite *float64
				for i, err := range tc.writes {
					callbacks.GetConn("not retained")
					callbacks.GotConn(httptrace.GotConnInfo{Reused: i == 0})
					callbacks.WroteRequest(httptrace.WroteRequestInfo{Err: err})
					snap := trace.Snapshot()
					if snap.TotalRequests != 1 || len(snap.Requests) != 1 || *snap.Requests[0].SDKPrepareMS != *initial.Requests[0].SDKPrepareMS {
						t.Fatalf("wire retry changed request or SDK preparation: %+v", snap)
					}
					timing := snap.Requests[0]
					if timing.ConnectionMS == nil || timing.Reused == nil || *timing.Reused != (i == 0) {
						t.Fatalf("connection acquisition not updated: %+v", timing)
					}
					if err == nil {
						if timing.WroteRequestMS == nil {
							t.Fatal("successful write missing")
						}
						successfulWrite = timing.WroteRequestMS
					} else if !reflect.DeepEqual(timing.WroteRequestMS, successfulWrite) {
						t.Fatalf("failed write recorded completion: got %v, want %v", timing.WroteRequestMS, successfulWrite)
					}
				}
				return nil, writeErr
			})}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unused.invalid", nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := rt.RoundTrip(req); err != writeErr {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(observed, tc.writes) {
				t.Fatalf("original callbacks = %v, want %v", observed, tc.writes)
			}
		})
	}
}
