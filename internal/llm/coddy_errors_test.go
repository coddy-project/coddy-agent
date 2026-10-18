package llm

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Every typed error of the coddy provider is read by the five predicates the
// agent and the resilient wrapper decide on, and by the two retry
// classifiers; this is the table of what each says (model m3-stream, the
// error table of 4.3).
func TestCoddyErrorsAreReadByEveryPredicate(t *testing.T) {
	up := func(cause string, emitted bool) error {
		return &coddyAPIError{kind: WireKindUpstream, cause: cause, emitted: emitted}
	}
	cases := []struct {
		name       string
		err        error
		retryable  bool
		transient  bool
		truncated  bool
		stalled    bool
		status     int
		retryAfter time.Duration
	}{
		{"upstream status before output", up(WireCauseStatus, false), false, true, false, false, 502, 0},
		{"upstream stall before output", up(WireCauseStall, false), false, true, false, true, 502, 0},
		{"upstream stall after output", up(WireCauseStall, true), false, true, true, true, 502, 0},
		{"upstream truncated before output", up(WireCauseTruncated, false), false, true, true, false, 502, 0},
		{"upstream status after output", up(WireCauseStatus, true), false, true, true, false, 502, 0},
		// The remote puts the status of its upstream into upstream{cause: status}:
		// a deterministic refusal is not worth running the step again, the same
		// statuses a local provider treats as transient are.
		{"upstream status 500", &coddyAPIError{kind: WireKindUpstream, cause: WireCauseStatus, status: 500}, false, true, false, false, 500, 0},
		{"upstream status 502", &coddyAPIError{kind: WireKindUpstream, cause: WireCauseStatus, status: 502}, false, true, false, false, 502, 0},
		{"upstream status 503", &coddyAPIError{kind: WireKindUpstream, cause: WireCauseStatus, status: 503}, false, true, false, false, 503, 0},
		{"upstream status 408", &coddyAPIError{kind: WireKindUpstream, cause: WireCauseStatus, status: 408}, false, true, false, false, 408, 0},
		{"upstream status 529", &coddyAPIError{kind: WireKindUpstream, cause: WireCauseStatus, status: 529}, false, true, false, false, 529, 0},
		{"upstream status 400 is deterministic", &coddyAPIError{kind: WireKindUpstream, cause: WireCauseStatus, status: 400}, false, false, false, false, 400, 0},
		{"upstream status 401 is deterministic", &coddyAPIError{kind: WireKindUpstream, cause: WireCauseStatus, status: 401}, false, false, false, false, 401, 0},
		{"upstream status 402 is deterministic", &coddyAPIError{kind: WireKindUpstream, cause: WireCauseStatus, status: 402}, false, false, false, false, 402, 0},
		{"upstream status 403 is deterministic", &coddyAPIError{kind: WireKindUpstream, cause: WireCauseStatus, status: 403}, false, false, false, false, 403, 0},
		{"upstream status 404 is deterministic", &coddyAPIError{kind: WireKindUpstream, cause: WireCauseStatus, status: 404}, false, false, false, false, 404, 0},
		{"upstream status 409 is deterministic", &coddyAPIError{kind: WireKindUpstream, cause: WireCauseStatus, status: 409}, false, false, false, false, 409, 0},
		{"upstream status 451 is deterministic", &coddyAPIError{kind: WireKindUpstream, cause: WireCauseStatus, status: 451}, false, false, false, false, 451, 0},
		{"upstream stall carries a status of its own", &coddyAPIError{kind: WireKindUpstream, cause: WireCauseStall, status: 502}, false, true, false, true, 502, 0},
		{"upstream timeout before output", up(WireCauseTimeout, false), false, false, false, false, 504, 0},
		{"upstream timeout after output keeps the partial", up(WireCauseTimeout, true), false, false, true, false, 504, 0},
		{"rate", &coddyAPIError{kind: WireKindRate, retryAfter: 7 * time.Second}, false, false, false, false, 429, 7 * time.Second},
		{"busy frame", &coddyAPIError{kind: WireKindBusy}, false, false, false, false, 429, 0},
		{"invalid", &coddyAPIError{kind: WireKindInvalid, status: 400}, false, false, false, false, 400, 0},
		{"invalid stale revision", &coddyAPIError{kind: WireKindInvalid, code: WireCodeStaleRevision, status: 400}, false, false, false, false, 400, 0},
		{"auth", &coddyAPIError{kind: WireKindAuth, status: 401}, false, false, false, false, 401, 0},
		{"auth forbidden", &coddyAPIError{kind: WireKindAuth, status: 403}, false, false, false, false, 403, 0},
		{"remote without the routes", &coddyAPIError{kind: CoddyKindUnsupported, status: 200}, false, false, false, false, 0, 0},
		{"broken stream", &coddyAPIError{kind: CoddyKindProtocol}, false, false, false, false, 0, 0},
		{"a plain 500 of a proxy", &coddyAPIError{kind: CoddyKindOther, status: 500}, false, false, false, false, 500, 0},
		{"a plain 429 of a proxy", &coddyAPIError{kind: CoddyKindOther, status: 429}, false, false, false, false, 429, 0},
		{"a plain 503 of a proxy named by status", &coddyAPIError{kind: CoddyKindOther, status: 503}, false, false, false, false, 503, 0},
		{"the spent busy wait", &coddyBusyError{waited: 30 * time.Second, budget: 30 * time.Second, requests: 9}, false, false, false, false, 0, 0},
		{"quota with a reset", &QuotaResetError{ResetAt: time.Now().Add(time.Hour), Delay: time.Hour, Cause: &coddyAPIError{kind: WireKindQuota}}, false, false, false, false, 429, time.Hour},
	}
	for _, tc := range cases {
		for _, wrap := range []string{"bare", "wrapped"} {
			t.Run(tc.name+"/"+wrap, func(t *testing.T) {
				err := tc.err
				if wrap == "wrapped" {
					err = fmt.Errorf("provider %q (https://remote.example): %w", "remote", fmt.Errorf("coddy stream: %w", tc.err))
				}
				if got := isRetryableLLMError(err); got != tc.retryable {
					t.Errorf("isRetryableLLMError = %v, want %v", got, tc.retryable)
				}
				if got := httpStatusFromError(err); got != 0 {
					t.Errorf("httpStatusFromError = %d, want 0: the wrapper must never read a status from a coddy error", got)
				}
				if got := IsTransientProviderError(err); got != tc.transient {
					t.Errorf("IsTransientProviderError = %v, want %v", got, tc.transient)
				}
				if got := IsStreamTruncated(err); got != tc.truncated {
					t.Errorf("IsStreamTruncated = %v, want %v", got, tc.truncated)
				}
				if got := IsStreamStalled(err); got != tc.stalled {
					t.Errorf("IsStreamStalled = %v, want %v", got, tc.stalled)
				}
				if got := UpstreamStatus(err); got != tc.status {
					t.Errorf("UpstreamStatus = %d, want %d", got, tc.status)
				}
				d, ok := UpstreamRetryAfter(err)
				if (tc.retryAfter > 0) != ok || d != tc.retryAfter {
					t.Errorf("UpstreamRetryAfter = %v, %v; want %v", d, ok, tc.retryAfter)
				}
			})
		}
	}
}

func TestCoddyErrorTextNeverPassesForAStatusOrAPause(t *testing.T) {
	// The text of a remote can say anything. The typed branches decide, never
	// the words: a status number, a reset time or a transport phrase inside a
	// remote's message changes nothing.
	hostile := "upstream answered 429 Too Many Requests, Limit resets at: 2099-01-01 00:00:00 UTC, retry in 5s, unexpected EOF, connection reset by peer, 502 Bad Gateway"
	for _, err := range []error{
		&coddyAPIError{kind: WireKindInvalid, message: hostile},
		&coddyAPIError{kind: WireKindUpstream, cause: WireCauseTimeout, message: hostile},
		&coddyAPIError{kind: WireKindRate, message: hostile},
		&coddyAPIError{kind: CoddyKindOther, status: 500, message: hostile},
		&coddyBusyError{waited: time.Second},
	} {
		if isRetryableLLMError(err) {
			t.Errorf("%v: a remote's words made the call retryable", err)
		}
		if httpStatusFromError(err) != 0 {
			t.Errorf("%v: a remote's words passed for a status", err)
		}
		if d, ok := UpstreamRetryAfter(err); ok && d > 0 {
			t.Errorf("%v: a remote's words passed for a pause of %v", err, d)
		}
	}
}

func TestCoddyBusyErrorDoesNotLookLikeAContextError(t *testing.T) {
	var err error = &coddyBusyError{waited: time.Second, budget: time.Second, requests: 2}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("a spent wait is not a cancellation or a deadline")
	}
	if !strings.Contains(err.Error(), "slot") {
		t.Fatalf("the error should name the slot: %q", err.Error())
	}
}

func TestCoddyBoundErrorIsAStallToTheRetryLogic(t *testing.T) {
	var err error = &coddyBoundError{after: 45 * time.Second}
	if !isRetryableLLMError(err) {
		t.Error("a remote that never answered the request is retried like a silent stream")
	}
	if !IsTransientProviderError(err) || !IsStreamStalled(err) {
		t.Error("the agent's recovery treats an unanswered request as a stall")
	}
	if !strings.Contains(err.Error(), "45s") || strings.Contains(err.Error(), "model") {
		t.Errorf("message: %q", err.Error())
	}
}

func TestCoddyErrorKindAndCodeAreReadableByDiagnostics(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &coddyAPIError{kind: WireKindInvalid, code: WireCodeStaleRevision})
	if CoddyErrorKind(err) != WireKindInvalid || CoddyErrorCode(err) != WireCodeStaleRevision {
		t.Fatalf("kind %q code %q", CoddyErrorKind(err), CoddyErrorCode(err))
	}
	if CoddyErrorKind(&coddyBusyError{}) != WireKindBusy {
		t.Fatal("a spent wait is kind busy")
	}
	if CoddyErrorKind(errors.New("other")) != "" || CoddyErrorCode(errors.New("other")) != "" {
		t.Fatal("a foreign error has no coddy kind")
	}
}

func TestClassifyCoddyAnswer(t *testing.T) {
	header := func(kv ...string) http.Header {
		h := http.Header{}
		for i := 0; i+1 < len(kv); i += 2 {
			h.Set(kv[i], kv[i+1])
		}
		return h
	}
	cases := []struct {
		name   string
		status int
		header http.Header
		body   string
		kind   string
		code   string
		// serverError is set for the statuses a relay or a gateway answers with
		// before output: they are retried by the existing machinery.
		serverError int
		retryAfter  time.Duration
		textHas     string
	}{
		{"coddy busy", 429, header("Retry-After", "1"), `{"status":429,"kind":"busy","message":"slots taken","retry_after_s":1}`, WireKindBusy, "", 0, time.Second, ""},
		{"coddy busy with only the header", 429, header("Retry-After", "3"), `{"kind":"busy"}`, WireKindBusy, "", 0, 3 * time.Second, ""},
		{"coddy rate", 429, nil, `{"kind":"rate","retry_after_s":2.5}`, WireKindRate, "", 0, 2500 * time.Millisecond, ""},
		{"coddy invalid", 400, nil, `{"kind":"invalid","message":"max_tokens must be a positive integer"}`, WireKindInvalid, "", 0, 0, "max_tokens must be a positive integer"},
		{"coddy stale revision", 400, nil, `{"kind":"invalid","code":"stale_revision","revision":"r2"}`, WireKindInvalid, WireCodeStaleRevision, 0, 0, ""},
		{"coddy 404 unknown alias", 404, nil, `{"kind":"invalid","message":"model not shared"}`, WireKindInvalid, "", 0, 0, ""},
		{"coddy 413", 413, nil, `{"kind":"invalid","message":"request body too large"}`, WireKindInvalid, "", 0, 0, ""},
		{"coddy auth", 403, nil, `{"kind":"auth","message":"shared models need authentication"}`, WireKindAuth, "", 0, 0, ""},
		{"coddy kind wins over a 502 status", 502, nil, `{"kind":"upstream","cause":"status"}`, WireKindUpstream, "", 0, 0, ""},
		{"plain-text 401 of the gate", 401, header("Content-Type", "text/plain"), "Unauthorized\n", WireKindAuth, "", 0, 0, ""},
		{"plain 403", 403, nil, "forbidden", WireKindAuth, "", 0, 0, ""},
		{"relay 502 before output", 502, nil, `{"error":{"message":"swarm node \"n1\": node is registered but not reachable","node":"n1","reason":"node is registered but not reachable"}}`, "", "", 502, 0, "n1"},
		{"gateway 503 html", 503, header("Content-Type", "text/html"), "<html><body>503 Service Unavailable</body></html>", "", "", 503, 0, ""},
		{"gateway 504", 504, nil, "", "", "", 504, 0, ""},
		{"gateway 408", 408, nil, "", "", "", 408, 0, ""},
		{"relay 404 hop error", 404, nil, `{"error":{"message":"swarm node \"zz\": no such node in this relay","node":"zz","reason":"no such node in this relay"}}`, WireKindInvalid, "", 0, 0, "no such node"},
		{"plain 404 means the routes are missing", 404, header("Content-Type", "text/plain"), "404 page not found\n", CoddyKindUnsupported, "", 0, 0, "does not offer shared models"},
		{"plain 500", 500, nil, "internal error", CoddyKindOther, "", 0, 0, ""},
		{"plain 429 of a proxy", 429, header("Retry-After", "9"), "slow down", CoddyKindOther, "", 0, 9 * time.Second, ""},
		{"plain 413 of a proxy", 413, nil, "<html>413 Request Entity Too Large</html>", CoddyKindOther, "", 0, 0, ""},
		{"plain 400", 400, nil, "bad request", CoddyKindOther, "", 0, 0, ""},
		{"an unknown kind is not trusted", 500, nil, `{"kind":"wat"}`, CoddyKindOther, "", 0, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := classifyCoddyAnswer(tc.status, tc.header, []byte(tc.body))
			if err == nil {
				t.Fatal("no error")
			}
			if tc.serverError != 0 {
				var sse *streamServerError
				if !errors.As(err, &sse) || sse.code != tc.serverError || sse.emitted {
					t.Fatalf("want a retryable server error %d before output, got %T %v", tc.serverError, err, err)
				}
				if !isRetryableLLMError(err) {
					t.Errorf("a %d answered before output must be retried by the wrapper", tc.serverError)
				}
				if tc.textHas != "" && !strings.Contains(err.Error(), tc.textHas) {
					t.Errorf("text %q lacks %q", err.Error(), tc.textHas)
				}
				return
			}
			var api *coddyAPIError
			if !errors.As(err, &api) {
				t.Fatalf("want a coddyAPIError, got %T %v", err, err)
			}
			if api.kind != tc.kind || api.code != tc.code {
				t.Fatalf("kind %q code %q, want %q %q", api.kind, api.code, tc.kind, tc.code)
			}
			if api.retryAfter != tc.retryAfter {
				t.Errorf("retryAfter = %v, want %v", api.retryAfter, tc.retryAfter)
			}
			if api.emitted {
				t.Error("an answer before the stream is before any output")
			}
			if tc.textHas != "" && !strings.Contains(err.Error(), tc.textHas) {
				t.Errorf("text %q lacks %q", err.Error(), tc.textHas)
			}
			if isRetryableLLMError(err) {
				t.Error("a coddy error is never retried by the wrapper")
			}
		})
	}
}

func TestClassifyCoddyAnswerReadsTheRevisionOfAStaleAnswer(t *testing.T) {
	err := classifyCoddyAnswer(400, nil, []byte(`{"kind":"invalid","code":"stale_revision","revision":"r2"}`))
	var api *coddyAPIError
	if !errors.As(err, &api) || api.revision != "r2" {
		t.Fatalf("revision not read: %v", err)
	}
}

func TestRemoteTextIsBoundedAndCleaned(t *testing.T) {
	long := strings.Repeat("x", 5000)
	err := classifyCoddyAnswer(400, nil, []byte(`{"kind":"invalid","message":"`+long+`\u001b[31mred\nline"}`))
	if len(err.Error()) > 1200 {
		t.Fatalf("a remote's message is not bounded: %d bytes", len(err.Error()))
	}
	if strings.ContainsAny(err.Error(), "\x1b\n") {
		t.Fatalf("control characters of a remote's message reach the terminal: %q", err.Error())
	}
}

// A Retry-After the remote names, in a header or in a frame, is clamped as a
// number of seconds before it is turned into a Duration: converted first, a
// value near 9.2e9 seconds wraps negative and a larger one saturates, and the
// wait that reads it would stop pausing.
func TestRetryAfterIsBoundedBeforeItBecomesADuration(t *testing.T) {
	header := func(v string) http.Header {
		h := http.Header{}
		h.Set("Retry-After", v)
		return h
	}
	for _, v := range []string{"9223372035", "9223372036", "9223372037", "8e9", "1e30", "1e308", "100000000", "Fri, 31 Dec 9999 23:59:59 GMT"} {
		d, ok := parseRetryAfterHeader(header(v))
		if !ok || d <= 0 || d > coddyRetryAfterCap {
			t.Errorf("Retry-After %q = %v, %v: want a pause of at most %v", v, d, ok, coddyRetryAfterCap)
		}
	}
	for _, v := range []string{"Inf", "+Inf", "-Inf", "inf", "infinity", "NaN", "nan", "-5", "0", "0.0", "", "soon"} {
		if d, ok := parseRetryAfterHeader(header(v)); ok || d != 0 {
			t.Errorf("Retry-After %q = %v, %v: want none", v, d, ok)
		}
	}
	if d, ok := parseRetryAfterHeader(header("2.5")); !ok || d != 2500*time.Millisecond {
		t.Errorf("an ordinary Retry-After changed: %v %v", d, ok)
	}

	for _, secs := range []float64{9223372035, 8e9, 1e30, math.MaxFloat64} {
		e := coddyAPIErrorFromWire(WireError{Kind: WireKindBusy, RetryAfterS: secs}, nil, false)
		if e.retryAfter <= 0 || e.retryAfter > coddyRetryAfterCap {
			t.Errorf("retry_after_s %v became %v: want a pause of at most %v", secs, e.retryAfter, coddyRetryAfterCap)
		}
	}
	for _, secs := range []float64{math.Inf(1), math.Inf(-1), math.NaN(), -1, 0} {
		if e := coddyAPIErrorFromWire(WireError{Kind: WireKindBusy, RetryAfterS: secs}, nil, false); e.retryAfter != 0 {
			t.Errorf("retry_after_s %v became %v: want none", secs, e.retryAfter)
		}
	}
	// A header beats the frame, and a header that says nothing leaves the frame's value.
	e := coddyAPIErrorFromWire(WireError{Kind: WireKindBusy, RetryAfterS: 7}, header("Inf"), false)
	if e.retryAfter != 7*time.Second {
		t.Errorf("an unusable header cancelled the frame's pause: %v", e.retryAfter)
	}
}

// An error frame is checked like the body of a refused request: a kind the
// wire does not name is other, a cause outside the four is status, and no text
// of the remote reaches a terminal or a log with a control character in it.
func TestErrorFrameIsValidatedLikeTheHTTPAnswer(t *testing.T) {
	const hostile = "\x1b]0;pwned\x07"
	noControl := func(t *testing.T, what, s string) {
		t.Helper()
		if strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) }) {
			t.Errorf("%s carries a control character: %q", what, s)
		}
	}

	e := coddyAPIErrorFromWire(WireError{Kind: hostile, Message: "plain"}, nil, false)
	if e.kind != CoddyKindOther {
		t.Errorf("an unknown kind is %q, want %q", e.kind, CoddyKindOther)
	}
	noControl(t, "the error text of an unknown kind", e.Error())
	if CoddyErrorKind(e) != CoddyKindOther {
		t.Errorf("CoddyErrorKind = %q", CoddyErrorKind(e))
	}

	for _, kind := range []string{"", "wat", "BUSY", " busy"} {
		if got := coddyAPIErrorFromWire(WireError{Kind: kind}, nil, false); got.kind != CoddyKindOther {
			t.Errorf("kind %q is %q, want other", kind, got.kind)
		}
	}
	for kind := range wireErrorKinds {
		if got := coddyAPIErrorFromWire(WireError{Kind: kind}, nil, false); got.kind != kind {
			t.Errorf("kind %q became %q", kind, got.kind)
		}
	}

	for cause, want := range map[string]string{
		WireCauseStatus: WireCauseStatus, WireCauseStall: WireCauseStall,
		WireCauseTruncated: WireCauseTruncated, WireCauseTimeout: WireCauseTimeout,
		hostile: WireCauseStatus, "": WireCauseStatus, "wat": WireCauseStatus,
	} {
		got := coddyAPIErrorFromWire(WireError{Kind: WireKindUpstream, Cause: cause}, nil, false)
		if got.cause != want {
			t.Errorf("upstream cause %q became %q, want %q", cause, got.cause, want)
		}
		noControl(t, "the error text of an upstream cause", got.Error())
		if strings.Contains(got.Error(), "pwned") {
			t.Errorf("an unknown cause is printed: %q", got.Error())
		}
	}

	got := coddyAPIErrorFromWire(WireError{Kind: WireKindInvalid, Code: hostile + "\nstale_revision"}, nil, false)
	noControl(t, "the code", got.code)
	noControl(t, "CoddyErrorCode", CoddyErrorCode(got))
	if got := coddyAPIErrorFromWire(WireError{Kind: WireKindInvalid, Code: WireCodeStaleRevision}, nil, false); !isStaleRevision(got) {
		t.Error("a well-formed code no longer reads as stale_revision")
	}
	long := strings.Repeat("c", 5000)
	if got := coddyAPIErrorFromWire(WireError{Kind: WireKindInvalid, Code: long}, nil, false); len(got.code) > 1200 {
		t.Errorf("a code of %d bytes is kept whole", len(got.code))
	}
}

// A usage limit whose reset time is already behind the clock must not become a
// zero-length wait: the agent would run the same call again at once, over and
// over. The pause the remote also names (it sends both) takes its place, and a
// quota that names no future moment stays a plain error.
func TestQuotaResetInThePastFallsBackToTheRelativePause(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	quota := func(resetAt time.Time, retryAfter time.Duration) *coddyAPIError {
		return &coddyAPIError{kind: WireKindQuota, resetAt: resetAt, retryAfter: retryAfter}
	}

	for name, resetAt := range map[string]time.Time{
		"a reset an hour behind":      now.Add(-time.Hour),
		"a reset at this instant":     now,
		"a reset a nanosecond behind": now.Add(-time.Nanosecond),
	} {
		t.Run(name+" with a pause", func(t *testing.T) {
			got := quotaResetFromWire(quota(resetAt, 45*time.Second), now)
			var qr *QuotaResetError
			if !errors.As(got, &qr) {
				t.Fatalf("got %T %v, want the typed reset", got, got)
			}
			if qr.Delay != 45*time.Second || !qr.ResetAt.Equal(now.Add(45*time.Second)) {
				t.Fatalf("reset %v delay %v, want the relative pause of 45 s", qr.ResetAt, qr.Delay)
			}
		})
		t.Run(name+" alone", func(t *testing.T) {
			got := quotaResetFromWire(quota(resetAt, 0), now)
			var qr *QuotaResetError
			if errors.As(got, &qr) {
				t.Fatalf("a reset that is not in the future became a wait of %v", qr.Delay)
			}
			var api *coddyAPIError
			if !errors.As(got, &api) || api.kind != WireKindQuota {
				t.Fatalf("got %T %v, want the plain quota error", got, got)
			}
		})
	}

	// A reset in the future still wins over a bare pause.
	got := quotaResetFromWire(quota(now.Add(10*time.Minute), 45*time.Second), now)
	var qr *QuotaResetError
	if !errors.As(got, &qr) || qr.Delay != 10*time.Minute {
		t.Fatalf("got %v, want the 10 minutes of the reset time", got)
	}
	// And a pause alone, or nothing at all, as before.
	if got := quotaResetFromWire(quota(time.Time{}, 3*time.Second), now); !errors.As(got, &qr) || qr.Delay != 3*time.Second {
		t.Fatalf("a pause alone: %v", got)
	}
	if got := quotaResetFromWire(quota(time.Time{}, 0), now); errors.As(got, &qr) {
		t.Fatalf("a quota with no time at all became a wait: %v", got)
	}
	// A reset beyond a day is bounded, so no arithmetic of the caller can wrap.
	got = quotaResetFromWire(quota(now.AddDate(100, 0, 0), 0), now)
	if !errors.As(got, &qr) || qr.Delay != coddyRetryAfterCap || !qr.ResetAt.Equal(now.Add(coddyRetryAfterCap)) {
		t.Fatalf("a reset a century away: %v", got)
	}
}
