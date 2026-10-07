//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// The error object of the shared-model routes is llm.WireError: the body of a
// refused request, and with a type the error frame that ends a stream. Its
// message is built here from the kind and the status alone. It is never taken
// from err.Error() and never made by substituting names in an upstream text:
// the provider wraps every error with the provider's name and api_base, and an
// upstream text can carry a dated model id, an organisation id or a quotation
// of the request, all of which would leave the host. The raw text goes to the
// log at debug level only.

// classifySharedFailure maps the failure of a provider call to the error
// object. stalled is the remote's own guard S having fired, timedOut is the
// max_call_ms bound of a blocking row having expired, and emitted is the
// number of chunk frames actually written.
func classifySharedFailure(err error, stalled, timedOut bool, emitted int) llm.WireError {
	out := llm.WireError{Emitted: emitted > 0}
	upstream := func(cause string, status int) llm.WireError {
		out.Kind = llm.WireKindUpstream
		out.Cause = cause
		out.Status = status
		out.Message = sharedUpstreamMessage(cause, status)
		return out
	}
	switch {
	case stalled || llm.IsStreamStalled(err):
		return upstream(llm.WireCauseStall, http.StatusGatewayTimeout)
	case timedOut || errors.Is(err, context.DeadlineExceeded):
		return upstream(llm.WireCauseTimeout, http.StatusGatewayTimeout)
	}

	var reset *llm.QuotaResetError
	if errors.As(err, &reset) {
		out.Kind = llm.WireKindQuota
		out.Status = http.StatusTooManyRequests
		out.Message = "the usage limit of the upstream provider is reached"
		if !reset.ResetAt.IsZero() {
			out.ResetAt = reset.ResetAt.UTC().Format(time.RFC3339)
		}
		if reset.Delay > 0 {
			out.RetryAfterS = reset.Delay.Seconds()
		}
		return out
	}

	status := llm.UpstreamStatus(err)
	switch {
	case status == http.StatusTooManyRequests:
		out.Kind = llm.WireKindRate
		out.Status = status
		out.Message = "the upstream provider is rate limiting this model (returned 429)"
		if d, ok := llm.UpstreamRetryAfter(err); ok && d > 0 {
			out.RetryAfterS = math.Ceil(d.Seconds())
		}
		return out
	case llm.IsStreamTruncated(err):
		return upstream(llm.WireCauseTruncated, status)
	case sharedInvalidStatus(status):
		out.Kind = llm.WireKindInvalid
		out.Status = status
		out.Message = fmt.Sprintf("the upstream provider rejected the request (returned %d)", status)
		return out
	}
	return upstream(llm.WireCauseStatus, status)
}

// sharedInvalidStatus is the upstream statuses that say the request itself is
// wrong for the model rather than the lane being down. A 401 or 403 is not one
// of them: it means the remote's own credential for its provider is refused,
// which the client must not read as its own credential being refused.
func sharedInvalidStatus(status int) bool {
	switch status {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusRequestEntityTooLarge,
		http.StatusRequestURITooLong, http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity:
		return true
	}
	return false
}

// sharedUpstreamMessage is the text of an upstream error, from the cause and
// the status only.
func sharedUpstreamMessage(cause string, status int) string {
	switch cause {
	case llm.WireCauseStall:
		return "the upstream provider went silent (stall)"
	case llm.WireCauseTimeout:
		return "the upstream provider did not answer within the call limit (timeout)"
	case llm.WireCauseTruncated:
		return "the upstream provider cut its answer short"
	}
	if status > 0 {
		return "upstream provider returned " + strconv.Itoa(status)
	}
	return "the upstream provider could not be reached"
}

// writeSharedError answers a request the shared-model routes refuse before a
// stream starts: the flat error object as JSON.
func writeSharedError(w http.ResponseWriter, status int, e llm.WireError) {
	e.Status = status
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	if e.Kind == llm.WireKindBusy {
		h.Set("Retry-After", "1")
		if e.RetryAfterS == 0 {
			e.RetryAfterS = 1
		}
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(e)
}

// sharedInvalid is the error object of a refused request.
func sharedInvalid(code, message string) llm.WireError {
	return llm.WireError{Kind: llm.WireKindInvalid, Code: code, Message: message}
}
