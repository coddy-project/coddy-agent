package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/version"
	"testing"
	"time"
)

func TestKimiOAuthEndpoint(t *testing.T) {
	for _, tc := range []struct {
		region, endpoint string
		valid            bool
	}{
		{"", "https://auth.kimi.ai", true},
		{"global", "https://auth.kimi.ai", true},
		{"china", "https://auth.kimi.com", true},
		{"mainland-cn", "", false},
		{"eu", "", false},
	} {
		endpoint, err := kimiOAuthEndpoint(tc.region)
		if (err == nil) != tc.valid {
			t.Errorf("region %q: valid=%v err=%v", tc.region, tc.valid, err)
			continue
		}
		if err == nil && endpoint != tc.endpoint {
			t.Errorf("region %q: endpoint=%q want %q", tc.region, endpoint, tc.endpoint)
		}
	}
}

func TestKimiVerificationURL(t *testing.T) {
	for _, tc := range []struct {
		region, raw string
		valid       bool
	}{
		{"china", "https://www.kimi.com/code/authorize_device", true},
		{"china", "https://www.kimi.com/code/authorize_device?user_code=ABCD", true},
		{"china", "https://www.kimi.ai/code/authorize_device", false},
		{"", "https://www.kimi.ai/code/authorize_device", true},
		{"global", "https://www.kimi.ai/code/authorize_device", true},
		{"global", "https://www.kimi.ai/code/authorize_device?user_code=ABCD", true},
		{"global", "https://www.kimi.com/code/authorize_device", false},
		{"china", "https://www.kimi.com/code/authorize_device?user_code=a&user_code=b", false},
		{"china", "https://www.kimi.com/code/authorize_device?next=https://evil.test", false},
		{"global", "https://www.kimi.ai/code/authorize_device?user_code=", false},
		{"china", "https://www.kimi.com.evil.test/code/authorize_device", false},
		{"china", "https://x@www.kimi.com/code/authorize_device", false},
		{"china", "http://www.kimi.com/code/authorize_device", false},
		{"china", "https://www.kimi.com/code/authorize_device#frag", false},
	} {
		if err := ValidateKimiVerificationURL(tc.region, tc.raw); (err == nil) != tc.valid {
			t.Errorf("%s %s: %v", tc.region, tc.raw, err)
		}
	}
}

// kimiFlowStub serves the two device-flow endpoints and records when each
// token poll arrived, relative to the moment the flow started.
type kimiFlowStub struct {
	server  *httptest.Server
	mu      sync.Mutex
	pollAt  []time.Duration
	started time.Time
}

func newKimiFlowStub(t *testing.T, deviceBody string, tokenReply func(poll int) (int, string)) *kimiFlowStub {
	stub := &kimiFlowStub{}
	var deviceID string
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Msh-Device-Id")
		if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(id) {
			t.Errorf("invalid device ID %q", id)
		}
		stub.mu.Lock()
		if deviceID == "" {
			deviceID = id
		} else if id != deviceID {
			t.Error("device ID changed between requests")
		}
		stub.mu.Unlock()
		if got := r.Header.Get("User-Agent"); got != "coddy/"+version.Get() {
			t.Errorf("user agent %q", got)
		}
		if got := r.Header.Get("X-Msh-Version"); got != version.Get() {
			t.Errorf("version %q", got)
		}
		if got := r.Header.Get("X-Msh-Platform"); got != "kimi_code_cli" {
			t.Errorf("platform %q", got)
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("unexpected credentials")
		}
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Error("not form POST")
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("client_id") != kimiClientID {
			t.Error("missing client id")
		}
		if strings.HasSuffix(r.URL.Path, "device_authorization") {
			if _, err := fmt.Fprint(w, deviceBody); err != nil {
				t.Errorf("write response: %v", err)
			}
			return
		}
		if r.Form.Get("device_code") != "device-secret" || r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" {
			t.Error("bad token request")
		}
		stub.mu.Lock()
		stub.pollAt = append(stub.pollAt, time.Since(stub.started))
		n := len(stub.pollAt)
		stub.mu.Unlock()
		status, body := tokenReply(n)
		if status != 0 {
			w.WriteHeader(status)
		}
		if _, err := fmt.Fprint(w, body); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

func kimiDeviceBody(interval, expires int) string {
	return fmt.Sprintf(`{"device_code":"device-secret","user_code":"ABCD","verification_uri_complete":"https://www.kimi.com/code/authorize_device?user_code=ABCD","expires_in":%d,"interval":%d}`, expires, interval)
}

func (stub *kimiFlowStub) start(t *testing.T, ctx context.Context) *KimiDeviceLogin {
	t.Helper()
	stub.started = time.Now()
	login, err := startKimiDeviceLogin(ctx, "china", stub.server.Client(), stub.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return login
}

func (stub *kimiFlowStub) polls() []time.Duration {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return append([]time.Duration(nil), stub.pollAt...)
}

// The first poll must go out immediately, as upstream oauth-manager does:
// it calls pollImpl before awaiting the interval.
func TestKimiWaitFirstPollIsImmediate(t *testing.T) {
	stub := newKimiFlowStub(t, kimiDeviceBody(3, 60), func(int) (int, string) {
		return 0, `{"access_token":"a","refresh_token":"r","expires_in":3600}`
	})
	login := stub.start(t, context.Background())
	start := time.Now()
	token, err := login.Wait(context.Background())
	elapsed := time.Since(start)
	if err != nil || token.AccessToken != "a" {
		t.Fatalf("token=%+v err=%v", token, err)
	}
	if got := stub.polls(); len(got) != 1 {
		t.Fatalf("polls=%d want 1", len(got))
	}
	if elapsed >= time.Second {
		t.Errorf("first poll waited %v with interval 3s; want an immediate first poll", elapsed)
	}
}

// After a pending poll the client waits the interval before the next one.
func TestKimiWaitPendingThenSuccess(t *testing.T) {
	stub := newKimiFlowStub(t, kimiDeviceBody(1, 60), func(poll int) (int, string) {
		if poll == 1 {
			return http.StatusBadRequest, `{"error":"authorization_pending"}`
		}
		return 0, `{"access_token":"a","refresh_token":"r","expires_in":3600}`
	})
	login := stub.start(t, context.Background())
	token, err := login.Wait(context.Background())
	if err != nil || token.AccessToken != "a" {
		t.Fatalf("token=%+v err=%v", token, err)
	}
	got := stub.polls()
	if len(got) != 2 {
		t.Fatalf("polls=%d want 2", len(got))
	}
	if got[0] >= 500*time.Millisecond {
		t.Errorf("first poll at %v; want immediate", got[0])
	}
	if got[1] < 900*time.Millisecond || got[1] > 3*time.Second {
		t.Errorf("second poll at %v; want about one interval (1s) after the first", got[1])
	}
}

// slow_down permanently raises the interval by 5s (RFC 8628, upstream
// oauth-manager): with interval 1 the next poll must not arrive at ~1s.
func TestKimiWaitSlowDownBumpsInterval(t *testing.T) {
	stub := newKimiFlowStub(t, kimiDeviceBody(1, 60), func(poll int) (int, string) {
		if poll == 1 {
			return http.StatusBadRequest, `{"error":"slow_down"}`
		}
		return http.StatusBadRequest, `{"error":"authorization_pending"}`
	})
	login := stub.start(t, context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := login.Wait(ctx)
		errCh <- err
	}()
	time.Sleep(2200 * time.Millisecond)
	if got := len(stub.polls()); got != 1 {
		cancel()
		err := <-errCh
		t.Fatalf("polls=%d after 2.2s with interval 1s plus slow_down bump; want the next poll only after ~6s (Wait: %v)", got, err)
	}
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err=%v want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return after cancellation")
	}
}

func TestKimiWaitCancellation(t *testing.T) {
	stub := newKimiFlowStub(t, kimiDeviceBody(5, 60), func(int) (int, string) {
		return http.StatusBadRequest, `{"error":"authorization_pending"}`
	})
	login := stub.start(t, context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	start := time.Now()
	_, err := login.Wait(ctx)
	elapsed := time.Since(start)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v want context.Canceled", err)
	}
	if elapsed >= 2*time.Second {
		t.Errorf("Wait returned after %v; want promptly on cancel", elapsed)
	}
	if got := len(stub.polls()); got != 1 {
		t.Errorf("polls=%d want 1 (the immediate first poll)", got)
	}
}

// The server-side expiry bounds the flow even when the poll interval is
// longer than the remaining lifetime, and the immediate first poll still
// happens before the deadline.
func TestKimiWaitExpiry(t *testing.T) {
	stub := newKimiFlowStub(t, kimiDeviceBody(5, 1), func(int) (int, string) {
		return http.StatusBadRequest, `{"error":"authorization_pending"}`
	})
	login := stub.start(t, context.Background())
	start := time.Now()
	_, err := login.Wait(context.Background())
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v want context.DeadlineExceeded", err)
	}
	if elapsed >= 3*time.Second {
		t.Errorf("Wait returned after %v; want the 1s expiry to bound it", elapsed)
	}
	if got := len(stub.polls()); got < 1 {
		t.Errorf("polls=%d want at least the immediate first poll before the deadline", got)
	}
}

// A terminal error (access_denied, expired_token, an unknown error) ends the
// flow at once without further polling.
func TestKimiWaitTerminalError(t *testing.T) {
	for _, code := range []string{"access_denied", "expired_token", "invalid_grant"} {
		stub := newKimiFlowStub(t, kimiDeviceBody(1, 60), func(int) (int, string) {
			return http.StatusBadRequest, `{"error":"` + code + `"}`
		})
		login := stub.start(t, context.Background())
		start := time.Now()
		_, err := login.Wait(context.Background())
		if err == nil {
			t.Fatalf("error=%v want a failure for %s", err, code)
		}
		if elapsed := time.Since(start); elapsed >= time.Second {
			t.Errorf("%s: Wait returned after %v; want no retry sleep", code, elapsed)
		}
		if got := len(stub.polls()); got != 1 {
			t.Errorf("%s: polls=%d want 1", code, got)
		}
	}
}
