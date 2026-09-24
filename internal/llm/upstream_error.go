package llm

import (
	"context"
	"errors"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go"
)

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
// lane rather than of the request - a 5xx, a connection that died, a stream
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
		return isDialFailure(transport.cause) || isTransientTransportError(transport.cause)
	}
	if status := UpstreamStatus(err); status != 0 {
		return transientStatus(status)
	}
	return httpStatusFromError(err) != 429 && isRetryableLLMError(err)
}

// transientStatus reports the statuses a lane answers with when it is down
// rather than when the request is wrong or a limit was reached.
func transientStatus(status int) bool {
	switch status {
	case 408, 500, 502, 503, 504, 529:
		return true
	}
	return false
}
