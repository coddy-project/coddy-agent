package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type kimiRoundTripFunc func(*http.Request) (*http.Response, error)

func (f kimiRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestKimiInferenceBoundary(t *testing.T) {
	cases := []struct{ mode, region, protocol, want string }{
		{"", "", "kimi", "https://api.kimi.ai/coding/v1/chat/completions"},
		{"subscription", "china", "kimi", "https://api.kimi.com/coding/v1/chat/completions"},
		{"subscription", "china", "anthropic", "https://api.kimi.com/coding/v1/messages?beta=true"},
		{"subscription", "china", "openai_responses", "https://api.kimi.com/coding/v1/responses"},
		{"platform", "china", "kimi", "https://api.moonshot.cn/v1/chat/completions"},
		{"platform", "global", "kimi", "https://api.moonshot.ai/v1/chat/completions"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: kimiRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != tc.want || r.Header.Get("Authorization") != "Bearer token" {
					t.Errorf("unexpected request %s", r.URL)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: r}, nil
			})}
			sourceCalls := 0
			tr, err := newKimiInferenceTransport(tc.mode, tc.region, tc.protocol, func(context.Context) (string, error) { sourceCalls++; return "token", nil }, client)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				resp, err := tr.Do(context.Background(), strings.NewReader("{}"))
				if err != nil {
					t.Fatal(err)
				}
				if err := resp.Body.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if calls != 2 || sourceCalls != 2 {
				t.Fatalf("requests=%d token calls=%d", calls, sourceCalls)
			}
		})
	}
}

func TestKimiInferenceRejectsUnknownAndRedirect(t *testing.T) {
	client := &http.Client{Transport: kimiRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 307, Header: http.Header{"Location": []string{"https://evil.example/"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})}
	source := func(context.Context) (string, error) { return "secret", nil }
	for _, p := range []string{"", "openai", "google", "../messages"} {
		if _, err := newKimiInferenceTransport("", "", p, source, client); err == nil {
			t.Fatalf("accepted %q", p)
		}
	}
	tr, err := newKimiInferenceTransport("", "", "kimi", source, client)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := tr.Do(context.Background(), strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	if resp.StatusCode != 307 {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestKimiInferenceRejectsUnverifiedRoutes(t *testing.T) {
	client := &http.Client{}
	source := func(context.Context) (string, error) { return "token", nil }
	for _, protocol := range []string{"anthropic", "openai_responses"} {
		for _, region := range []string{"global", "china"} {
			if _, err := newKimiInferenceTransport("platform", region, protocol, source, client); err == nil {
				t.Errorf("accepted unverified platform %s in %s", protocol, region)
			}
		}
	}
	if _, err := newKimiInferenceTransport("subscription", "mainland-cn", "kimi", source, client); err == nil {
		t.Error("accepted non-config region mainland-cn")
	}
}

func TestKimiInferenceRealProviderProxy(t *testing.T) {
	requests := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Method + " " + r.Host
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()
	tr, err := NewKimiInferenceTransport("platform", "china", "kimi", proxy.URL, func(context.Context) (string, error) { return "synthetic-token", nil })
	if err != nil {
		t.Fatal(err)
	}
	_, err = tr.Do(context.Background(), strings.NewReader("{}"))
	if err == nil {
		t.Fatal("expected proxy rejection")
	}
	select {
	case got := <-requests:
		if got != "CONNECT api.moonshot.cn:443" {
			t.Fatalf("proxy request = %q", got)
		}
	default:
		t.Fatal("provider proxy received no request")
	}
}

func TestKimiInferenceRedactsCredentialSourceError(t *testing.T) {
	client := &http.Client{Transport: kimiRoundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("sent request"); return nil, nil })}
	tr, err := newKimiInferenceTransport("", "", "kimi", func(context.Context) (string, error) { return "", errors.New("secret") }, client)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tr.Do(context.Background(), strings.NewReader("{}"))
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error %v", err)
	}
}

func TestKimiInferenceAnthropicHeaders(t *testing.T) {
	client := &http.Client{Transport: kimiRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.URL.String(); got != "https://api.kimi.ai/coding/v1/messages?beta=true" {
			t.Errorf("route %s", got)
		}
		if r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("anthropic-version") != "2023-06-01" || r.Header.Get("anthropic-beta") != "interleaved-thinking-2025-05-14" {
			t.Errorf("unexpected headers %#v", r.Header)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})}
	tr, err := newKimiInferenceTransport("subscription", "global", "anthropic", func(context.Context) (string, error) { return "secret", nil }, client)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := tr.Do(context.Background(), strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
}
