package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go"
)

// httpStatusError is a non-2xx answer a provider read off the wire itself,
// with no SDK in between (the native Google API of the OpenCode gateway). It
// keeps the status and the headers, so the classifiers read it as they read
// the SDK errors of the other providers: a 429 or a 5xx is retried, its
// Retry-After is the pause, and UpstreamStatus names the status.
type httpStatusError struct {
	label  string
	status int
	header http.Header
	body   string
}

func (e *httpStatusError) Error() string {
	if body := strings.TrimSpace(e.body); body != "" {
		return fmt.Sprintf("%s: HTTP %d: %s", e.label, e.status, body)
	}
	return fmt.Sprintf("%s: HTTP %d", e.label, e.status)
}

// UpstreamStatus reports the HTTP status the provider answered a failed call
// with, or 0 when the error carries none (a transport failure, an error of
// Coddy's own). Only typed errors are read: unlike httpStatusFromError, which
// decides retries and may guess from a message, this is what an HTTP client
// of Coddy is told, so a digit inside a message never passes for a status. A
// usage limit Coddy chose not to wait for is the 429 behind it.
func UpstreamStatus(err error) int {
	if err == nil {
		return 0
	}
	var reset *QuotaResetError
	if errors.As(err, &reset) {
		return 429
	}
	var oai *openai.Error
	if errors.As(err, &oai) && oai.StatusCode > 0 {
		return oai.StatusCode
	}
	var ant *anthropic.Error
	if errors.As(err, &ant) && ant.StatusCode > 0 {
		return ant.StatusCode
	}
	var dev *devinAPIError
	if errors.As(err, &dev) {
		return dev.status
	}
	var raw *httpStatusError
	if errors.As(err, &raw) {
		return raw.status
	}
	var sse *streamServerError
	if errors.As(err, &sse) {
		return sse.code
	}
	return 0
}

// UpstreamRetryAfter reports the pause the provider asked for before the
// next attempt: a Retry-After header, a reset time or a "retry in Ns" phrase
// of a limit answer, or the reset of a usage limit Coddy chose not to wait for.
func UpstreamRetryAfter(err error) (time.Duration, bool) {
	var reset *QuotaResetError
	if errors.As(err, &reset) && reset.Delay > 0 {
		return reset.Delay, true
	}
	var dev *devinAPIError
	if errors.As(err, &dev) && dev.retryAfter > 0 {
		return dev.retryAfter, true
	}
	return serverRetryDelay(err)
}

// IsTransientProviderError reports whether err is a failure of the provider's
// lane rather than of the request - a 5xx, an overload (529, or an error
// event the stream carried that says so), a connection that died, a stream
// cut or gone silent - so the same step may run again after a pause.
// Unlike the retry classification of the resilient wrapper it holds after
// text has streamed: the wrapper must not replay a call whose deltas the
// caller already showed, but the agent can keep that text and ask the model
// to go on. A request the provider refused, a cancel, a deadline and a limit
// (429, with or without a reset time: the wrapper's backoff and the opt-in
// limit wait own those, and a pause of seconds does not lift a quota) are
// not transient.
func IsTransientProviderError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	// An ACP agent decides by kind, ahead of any text: a step it may have
	// acted on is never run again.
	var acpErr *ACPError
	if errors.As(err, &acpErr) {
		return acpErr.retryable()
	}
	// The same request asks the same thing again, whatever status carried the
	// refusal.
	if IsContextOverflow(err) {
		return false
	}
	var reset *QuotaResetError
	if errors.As(err, &reset) {
		return false
	}
	var sse *streamServerError
	if errors.As(err, &sse) {
		// A failure the server reported without a status (a Codex
		// response.failed) may be a refusal as well as an outage.
		return transientStatus(sse.code)
	}
	if UpstreamStatus(err) == 429 {
		return false
	}
	var trunc *streamTruncatedError
	if errors.As(err, &trunc) {
		return true
	}
	var stalled *streamStalledError
	if errors.As(err, &stalled) {
		return true
	}
	var transport *streamTransportError
	if errors.As(err, &transport) {
		// An error the stream reported in-band is judged by its decoded
		// fields: an overload is the lane's, before text and after it alike
		// (the agent keeps what was shown and goes on), and any other is
		// final whatever its message says. A status the SDK's HTTP error
		// behind the wrapper carries (the Anthropic and Codex streams wrap it
		// as it came) is the lane's when it marks the lane as down; a limit
		// was ruled out above.
		if overload, decoded := inBandOverload(transport.cause); decoded {
			return overload
		}
		return transientStatus(UpstreamStatus(transport.cause)) || isDialFailure(transport.cause) ||
			isTransientTransportError(transport.cause)
	}
	if status := UpstreamStatus(err); status != 0 {
		return transientStatus(status)
	}
	return httpStatusFromError(err) != 429 && isRetryableLLMError(err)
}

// laneDownStatuses are the statuses a lane answers with when it is down
// rather than when the request is wrong or a limit was reached: a timeout, a
// server failure, and 529, Anthropic's "overloaded". It is the one list the
// resilient wrapper's retry classification (with the limit, 429, on top),
// its scan of an untyped message for a status and IsTransientProviderError
// all read.
var laneDownStatuses = []int{408, 500, 502, 503, 504, 529}

// retryableStatuses is what the resilient wrapper repeats: the lane failures
// and the limit, whose backoff and reset handling the wrapper owns.
var retryableStatuses = append([]int{429}, laneDownStatuses...)

// transientStatus reports whether status marks the lane as down.
func transientStatus(status int) bool {
	return slices.Contains(laneDownStatuses, status)
}
