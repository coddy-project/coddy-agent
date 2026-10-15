package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The errors of the coddy provider.
//
// The agent and the resilient wrapper decide by the TYPES of llm errors:
// IsStreamTruncated and IsStreamStalled keep a partial answer after a cut
// stream, IsTransientProviderError starts the recovery that re-issues a step,
// and the wrapper retries by isRetryableLLMError and httpStatusFromError.
// None of them may be fed an error built from a remote's answer in a shape
// they already know (a stall, a truncation, a status error): the remote has
// retried its own upstream already, so a local retry multiplies the requests
// (up to sixteen for one plain upstream error) and a retry after output shows
// the same text twice. Hence ONE type, coddyAPIError, registered in all five
// predicates and read by the two classifiers as status zero and never
// retryable; and a second one, coddyBusyError, for the spent wait for a slot.
//
// The only failures retried locally are built as the existing transport
// errors: a 408, 502, 503 or 504 answered before any output (a relay that
// cannot reach its node, a gateway) as a streamServerError, and a stream that
// ends without a terminal event as a streamTruncatedError, both only while no
// chunk was emitted.

// The kinds a coddy provider failure carries beyond the wire's own six
// (CoddyErrorKind).
const (
	// CoddyKindUnsupported is a remote that does not offer shared models: a
	// 200 that is not the listing or an event stream, or a plain 404.
	CoddyKindUnsupported = "unsupported"
	// CoddyKindProtocol is a stream that broke the rules of the wire.
	CoddyKindProtocol = "protocol"
	// CoddyKindOther is an answer of a gateway or a proxy that no rule
	// classifies: not retried.
	CoddyKindOther = "other"
)

// coddyRetryAfterCap bounds every pause a remote names (Retry-After, retry_after_s,
// reset_at), as seconds and before the value is turned into a Duration.
const coddyRetryAfterCap = 24 * time.Hour

// remoteTextLimit bounds a remote's message in an error: the remote is an
// untrusted host as far as the client's memory and terminal go.
const remoteTextLimit = 512

// coddyAPIError is a failure a remote Coddy (or a hop on the way) reported.
type coddyAPIError struct {
	kind    string
	cause   string
	code    string
	message string
	// status is what the remote or the hop answered, informational: the
	// client decides by kind and cause.
	status int
	// emitted is the client's own count of chunks it passed on, never the
	// frame's field.
	emitted    bool
	retryAfter time.Duration
	resetAt    time.Time
	revision   string
}

func (e *coddyAPIError) Error() string {
	msg := e.message
	if msg == "" {
		msg = defaultCoddyMessage(e.kind, e.cause)
	}
	label := e.kind
	if e.kind == WireKindUpstream && e.cause != "" && e.cause != WireCauseStatus {
		label += ", " + e.cause
	}
	switch e.kind {
	case CoddyKindUnsupported, CoddyKindProtocol, CoddyKindOther:
		return msg
	}
	return msg + " (" + label + ")"
}

func defaultCoddyMessage(kind, cause string) string {
	switch kind {
	case WireKindBusy:
		return "the remote has no free stream slot for this credential"
	case WireKindRate:
		return "the remote's provider is rate limiting this model"
	case WireKindQuota:
		return "the remote's usage limit is reached"
	case WireKindUpstream:
		switch cause {
		case WireCauseStall:
			return "the remote's provider went silent"
		case WireCauseTruncated:
			return "the remote's provider cut its answer short"
		case WireCauseTimeout:
			return "the remote's provider did not answer in time"
		}
		return "the remote's provider failed"
	case WireKindInvalid:
		return "the remote refused the request"
	case WireKindAuth:
		return "the remote refused the credential"
	case CoddyKindUnsupported:
		return "the remote does not offer shared models"
	}
	return "the remote answered with an error"
}

// transient reports a failure of the remote's lane rather than of the request,
// so the agent may run the step again after a pause. A timeout is not: the
// agent's recovery would repeat a call that already ran as long as the
// remote allows. Neither is an upstream status that a local provider would not
// repeat either (a 401, 402, 403, 409, 451 ...): the remote reports the status
// of its upstream in upstream{cause: status}, and the same call gets the same
// answer. A status the remote did not report (zero) is given the benefit of
// the doubt.
func (e *coddyAPIError) transient() bool {
	if e.kind != WireKindUpstream || e.cause == WireCauseTimeout {
		return false
	}
	return e.cause != WireCauseStatus || e.status == 0 || transientStatus(e.status)
}

// truncated reports a cut answer whose partial text the agent keeps: a stream
// cut by the remote, or any upstream failure after output (a timeout after
// output included).
func (e *coddyAPIError) truncated() bool {
	return e.cause == WireCauseTruncated || (e.kind == WireKindUpstream && e.emitted)
}

func (e *coddyAPIError) stalled() bool { return e.cause == WireCauseStall }

// upstreamStatus is what an HTTP client of this Coddy is told: the status the
// remote reported when it is an error status, else the status the kind stands
// for. Failures that carry none (a remote without the routes, a broken
// stream) have none.
func (e *coddyAPIError) upstreamStatus() int {
	if e.status >= 400 && e.status <= 599 {
		return e.status
	}
	switch e.kind {
	case WireKindBusy, WireKindRate, WireKindQuota:
		return http.StatusTooManyRequests
	case WireKindUpstream:
		if e.cause == WireCauseTimeout {
			return http.StatusGatewayTimeout
		}
		return http.StatusBadGateway
	case WireKindInvalid:
		return http.StatusBadRequest
	case WireKindAuth:
		return http.StatusUnauthorized
	}
	return 0
}

// coddyBusyError is the spent wait for a stream slot of the remote. It has
// status zero on every layer and is terminal on every one: a restart by the
// wrapper or by the agent's recovery would multiply the wait.
type coddyBusyError struct {
	waited   time.Duration
	budget   time.Duration
	requests int
	// code is the code of the last busy answer: empty for a full slot, one of
	// the window codes when a limit on calls per minute, or on a client's
	// concurrent calls, refused it. It only changes the words of the message.
	code string
}

// what names the limit a busy answer came from.
func (e *coddyBusyError) what() string {
	switch e.code {
	case WireCodeRateWindow:
		return "the remote's limit of calls per minute for this credential is spent"
	case WireCodeClientRate:
		return "the relay's limit of calls per minute for this client is spent"
	case WireCodeClientStreams:
		return "the relay's limit of concurrent calls for this client is reached"
	}
	return "the remote has no free stream slot for this credential"
}

func (e *coddyBusyError) Error() string {
	if e.budget <= 0 {
		return e.what() + ", and waiting for it is turned off (busy_wait_ms)"
	}
	until := "for one"
	if e.code == WireCodeRateWindow || e.code == WireCodeClientRate {
		until = "for a free window"
	}
	return fmt.Sprintf("%s: waited %s %s, in %d requests", e.what(), e.waited.Round(100*time.Millisecond), until, e.requests)
}

// coddyBoundError is a remote that did not answer the request within the
// request bound R: the upload stalled or the remote never produced its
// headers. To the retry logic it is a stream that went silent (the server took
// the request and never answered it), but its message does not talk of a model.
type coddyBoundError struct {
	after time.Duration
}

func (e *coddyBoundError) Error() string {
	return "the remote did not answer the request within " + e.after.String() + " (request bound)"
}

func (e *coddyBoundError) Unwrap() error { return &streamStalledError{idle: e.after} }

// coddyNeverRetried reports a coddy error the resilient wrapper must not
// retry and must read no status from.
func coddyNeverRetried(err error) bool {
	var api *coddyAPIError
	var busy *coddyBusyError
	return errors.As(err, &api) || errors.As(err, &busy)
}

func coddyStreamTruncated(err error) bool {
	var api *coddyAPIError
	return errors.As(err, &api) && api.truncated()
}

func coddyStreamStalled(err error) bool {
	var api *coddyAPIError
	return errors.As(err, &api) && api.stalled()
}

// coddyTransient answers IsTransientProviderError for a coddy error; known is
// false for any other error.
func coddyTransient(err error) (transient, known bool) {
	var busy *coddyBusyError
	if errors.As(err, &busy) {
		return false, true
	}
	var api *coddyAPIError
	if errors.As(err, &api) {
		return api.transient(), true
	}
	return false, false
}

// coddyUpstreamStatus answers UpstreamStatus for a coddy error.
func coddyUpstreamStatus(err error) (status int, known bool) {
	var busy *coddyBusyError
	if errors.As(err, &busy) {
		return 0, true
	}
	var api *coddyAPIError
	if errors.As(err, &api) {
		return api.upstreamStatus(), true
	}
	return 0, false
}

// coddyRetryAfter answers UpstreamRetryAfter for a coddy error; known is
// false for any other error, and the text of a coddy error is never scanned
// for a pause.
func coddyRetryAfter(err error) (d time.Duration, known bool) {
	var busy *coddyBusyError
	if errors.As(err, &busy) {
		return 0, true
	}
	var api *coddyAPIError
	if errors.As(err, &api) {
		return api.retryAfter, true
	}
	return 0, false
}

// CoddyErrorKind names the kind of a coddy provider failure: one of the wire's
// (busy, rate, quota, upstream, invalid, auth) or unsupported, protocol and
// other for a remote that does not offer shared models, a stream that broke
// the wire and an answer no rule classifies. A spent wait for a slot is busy.
// It is empty for any other error. Diagnostics read it to tell a refused
// credential from an unreachable remote.
func CoddyErrorKind(err error) string {
	var busy *coddyBusyError
	if errors.As(err, &busy) {
		return WireKindBusy
	}
	var api *coddyAPIError
	if errors.As(err, &api) {
		return api.kind
	}
	return ""
}

// CoddyErrorCode is the code of a coddy failure (stale_revision), or empty.
func CoddyErrorCode(err error) string {
	var api *coddyAPIError
	if errors.As(err, &api) {
		return api.code
	}
	return ""
}

// wireErrorKinds are the kinds a remote may name.
var wireErrorKinds = map[string]bool{
	WireKindBusy: true, WireKindRate: true, WireKindQuota: true,
	WireKindUpstream: true, WireKindInvalid: true, WireKindAuth: true,
}

// retryAfterSeconds turns a number of seconds a remote named into a pause. A
// value that is not a positive finite number names none, and a longer one is
// cut to coddyRetryAfterCap while it is still a float: converted first, a value
// near 9.2e9 seconds wraps negative and a larger one saturates or wraps, and
// the wait that reads it would stop pausing.
func retryAfterSeconds(sec float64) (time.Duration, bool) {
	if math.IsNaN(sec) || math.IsInf(sec, 0) || sec <= 0 {
		return 0, false
	}
	if sec >= coddyRetryAfterCap.Seconds() {
		return coddyRetryAfterCap, true
	}
	if d := time.Duration(sec * float64(time.Second)); d > 0 {
		return d, true
	}
	return 0, false
}

// coddyAPIErrorFromWire builds the error a remote's error object describes,
// checked as the HTTP path checks the body of a refused request: a kind outside
// the wire's six is an answer no rule classifies, a cause outside the four is
// status, and the texts are cleaned of what a terminal would act on. emitted is
// the client's own count, not the object's field.
func coddyAPIErrorFromWire(w WireError, header http.Header, emitted bool) *coddyAPIError {
	e := &coddyAPIError{
		kind:     w.Kind,
		code:     cleanRemoteText(w.Code),
		message:  cleanRemoteText(w.Message),
		status:   w.Status,
		emitted:  emitted,
		revision: cleanRemoteText(w.Revision),
	}
	if !wireErrorKinds[e.kind] {
		e.kind = CoddyKindOther
	}
	if e.kind == WireKindUpstream {
		switch w.Cause {
		case WireCauseStall, WireCauseTruncated, WireCauseTimeout:
			e.cause = w.Cause
		default:
			e.cause = WireCauseStatus
		}
	}
	if d, ok := retryAfterSeconds(w.RetryAfterS); ok {
		e.retryAfter = d
	}
	if d, ok := parseRetryAfterHeader(header); ok {
		e.retryAfter = d
	}
	if w.ResetAt != "" {
		if t, err := time.Parse(time.RFC3339, w.ResetAt); err == nil {
			e.resetAt = t
		}
	}
	return e
}

// quotaResetFromWire turns a quota error into the typed reset the agent's
// wait_for_limit_reset reads. A reset time still ahead of the clock wins over a
// bare pause; one that is not (the remote's clock runs behind this one, or the
// limit has just lifted) gives way to the relative pause the remote sends
// with it, which does not depend on the two clocks agreeing. A quota that
// names no moment ahead stays a coddyAPIError, since the agent would otherwise
// wait for nothing and run the same call again at once. A reset more than
// coddyRetryAfterCap away is cut to it, so that no arithmetic on the delay can
// wrap.
func quotaResetFromWire(api *coddyAPIError, now time.Time) error {
	if !api.resetAt.IsZero() {
		if d := api.resetAt.Sub(now); d > 0 {
			at := api.resetAt
			if d > coddyRetryAfterCap {
				d, at = coddyRetryAfterCap, now.Add(coddyRetryAfterCap)
			}
			return &QuotaResetError{ResetAt: at, Delay: d, Cause: api}
		}
	}
	if api.retryAfter > 0 {
		return &QuotaResetError{ResetAt: now.Add(api.retryAfter), Delay: api.retryAfter, Cause: api}
	}
	return api
}

// classifyCoddyAnswer turns a response that is not a stream - a refused
// request, an answer of a relay or a gateway - into the error the rules of 4.3
// give it. A coddy kind wins over any status. Anything else is read by status,
// in this order: 401 and 403 are auth; 408, 502, 503 and 504, answered before
// any output, are a transient transport failure the wrapper retries (a relay
// that cannot reach its node answers 502); a 404 with a relay's hop-error body
// is invalid, and a plain 404 means the remote has no such routes; everything
// else is not retried.
func classifyCoddyAnswer(status int, header http.Header, body []byte) error {
	var w WireError
	if json.Unmarshal(body, &w) == nil && wireErrorKinds[w.Kind] {
		if w.Status == 0 {
			w.Status = status
		}
		return coddyAPIErrorFromWire(w, header, false)
	}
	retryAfter, _ := parseRetryAfterHeader(header)
	hop := hopErrorMessage(body)
	snippet := hop
	if snippet == "" {
		snippet = cleanRemoteText(string(body))
	}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &coddyAPIError{kind: WireKindAuth, status: status}
	case http.StatusRequestTimeout, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		if looksLikeHTML(body) || snippet == "" {
			snippet = http.StatusText(status)
		}
		return &streamServerError{code: status, msg: snippet}
	case http.StatusNotFound:
		if hop != "" {
			return &coddyAPIError{kind: WireKindInvalid, status: status, message: hop}
		}
		return &coddyAPIError{kind: CoddyKindUnsupported, status: status, message: "the remote does not offer shared models (it answered not found for the shared-model routes)"}
	}
	msg := "the remote or a proxy in front of it answered HTTP " + strconv.Itoa(status)
	if snippet != "" && !looksLikeHTML(body) {
		msg += ": " + snippet
	}
	return &coddyAPIError{kind: CoddyKindOther, status: status, retryAfter: retryAfter, message: msg}
}

// hopErrorMessage reads the message of a relay's hop error ({"error":{"node":
// ...,"message":...}}), or returns "" for any other body.
func hopErrorMessage(body []byte) string {
	var h struct {
		Error struct {
			Message string `json:"message"`
			Node    string `json:"node"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &h) != nil || h.Error.Node == "" {
		return ""
	}
	return cleanRemoteText(h.Error.Message)
}

func looksLikeHTML(body []byte) bool {
	head := strings.ToLower(strings.TrimSpace(string(body[:min(len(body), 256)])))
	return strings.HasPrefix(head, "<!doctype") || strings.HasPrefix(head, "<html") || strings.Contains(head, "<body")
}

// parseRetryAfterHeader reads a Retry-After header: seconds or an HTTP date,
// bounded by coddyRetryAfterCap. A value that is not a finite positive number
// names no pause.
func parseRetryAfterHeader(h http.Header) (time.Duration, bool) {
	ra := strings.TrimSpace(h.Get("Retry-After"))
	if ra == "" {
		return 0, false
	}
	if v, err := strconv.ParseFloat(ra, 64); err == nil {
		return retryAfterSeconds(v)
	}
	if t, err := http.ParseTime(ra); err == nil {
		if d := time.Until(t); d > 0 {
			return min(d, coddyRetryAfterCap), true
		}
	}
	return 0, false
}

// cleanRemoteText bounds a text a remote sent and strips the characters a
// terminal would act on.
func cleanRemoteText(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > remoteTextLimit {
		cut := remoteTextLimit
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "..."
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return ' '
		}
		return r
	}, s)
}
