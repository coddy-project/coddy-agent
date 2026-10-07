package pachcafake

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
)

// Fault makes the fake answer a route with an error, the way Pachca does on
// a rate limit, a validation it disagrees with or an outage. A faulted
// request does not take effect: no message is created or edited, no event
// is deleted.
type Fault struct {
	// Route is the method plus the collection path, ids stripped, as
	// Call.Route spells it: "POST /messages", "PUT /messages",
	// "GET /webhooks/events", "DELETE /webhooks/events"; "*" matches every
	// route.
	Route string
	// Status is the HTTP status; 500 when zero.
	Status int
	// RetryAfter sets the Retry-After header, in seconds, when positive.
	RetryAfter int
	// Body is the raw response body. Empty means "Too Many Requests" as
	// text/plain for a 429, an OAuthError for a 401 or 403, and an ApiError
	// otherwise.
	Body string
	// ContentType of Body; derived from the body when empty.
	ContentType string
	// Times is how many requests the fault answers before it clears itself;
	// zero means one.
	Times int
	// Contains narrows the fault to requests whose body carries this text. A
	// request that does not match passes and is not counted.
	Contains string
}

// AddFault schedules a fault. Several can be scheduled; the oldest one that
// matches a request answers it.
func (s *Server) AddFault(f Fault) {
	f.Route = strings.TrimSpace(f.Route)
	if f.Route == "" {
		f.Route = "*"
	}
	if f.Status == 0 {
		f.Status = http.StatusInternalServerError
	}
	if f.Times <= 0 {
		f.Times = 1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faults = append(s.faults, &f)
}

// takeFaultLocked returns the fault that answers a request, counting it
// down. Caller holds s.mu.
func (s *Server) takeFaultLocked(route string, body []byte) *Fault {
	for i, f := range s.faults {
		if f.Route != "*" && !strings.EqualFold(f.Route, route) {
			continue
		}
		if f.Contains != "" && !bytes.Contains(body, []byte(f.Contains)) {
			continue
		}
		hit := *f
		f.Times--
		if f.Times <= 0 {
			s.faults = append(s.faults[:i:i], s.faults[i+1:]...)
		}
		return &hit
	}
	return nil
}

// reply renders the fault's response.
func (f *Fault) reply() reply {
	out := reply{status: f.Status, retryAfter: f.RetryAfter, body: []byte(f.Body), contentType: f.ContentType}
	if f.Body == "" {
		switch f.Status {
		case http.StatusTooManyRequests:
			out.body = []byte("Too Many Requests")
			out.contentType = "text/plain; charset=utf-8"
		case http.StatusUnauthorized, http.StatusForbidden:
			code := "invalid_token"
			if f.Status == http.StatusForbidden {
				code = "insufficient_scope"
			}
			rendered := oauthReply(f.Status, code, http.StatusText(f.Status))
			out.body, out.contentType = rendered.body, rendered.contentType
		default:
			rendered := apiErrorReply(f.Status, "fault", "", "unhandled", http.StatusText(f.Status))
			out.body, out.contentType = rendered.body, rendered.contentType
		}
		if f.ContentType != "" {
			out.contentType = f.ContentType
		}
	}
	if out.contentType == "" {
		out.contentType = "text/plain; charset=utf-8"
		if json.Valid(out.body) {
			out.contentType = "application/json; charset=utf-8"
		}
	}
	return out
}
