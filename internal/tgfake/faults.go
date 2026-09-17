package tgfake

import (
	"net/url"
	"strings"
)

// Fault makes the fake answer a method with an error, the way Telegram does
// on a bad request, a flood or an outage.
type Fault struct {
	// Method is the Bot API method name, or "*" for every method.
	Method string `json:"method"`
	// Code is the error_code and the HTTP status.
	Code int `json:"code"`
	// Description is the error text; a default is derived from Code when empty.
	Description string `json:"description,omitempty"`
	// RetryAfter fills parameters.retry_after, as a 429 carries.
	RetryAfter int `json:"retry_after,omitempty"`
	// Times is how many calls the fault answers before it clears itself;
	// zero or less keeps it until ClearFaults.
	Times int `json:"times,omitempty"`
	// Contains narrows the fault to calls one of whose parameters carries
	// this text: Telegram refusing an entity it cannot parse, not the method
	// as a whole. A call that does not match passes and is not counted.
	Contains string `json:"contains,omitempty"`
}

// SetFault schedules a fault; one per method, the newest wins.
func (s *Server) SetFault(f Fault) {
	f.Method = strings.ToLower(strings.TrimSpace(f.Method))
	if f.Method == "" {
		f.Method = "*"
	}
	if f.Code == 0 {
		f.Code = 500
	}
	if f.Description == "" {
		f.Description = defaultFaultDescription(f.Code, f.RetryAfter)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faults[f.Method] = &f
}

// ClearFault removes the fault scheduled for method; "" or "*" removes the
// catch-all.
func (s *Server) ClearFault(method string) {
	method = strings.ToLower(strings.TrimSpace(method))
	if method == "" {
		method = "*"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.faults, method)
}

// ClearFaults removes every scheduled fault.
func (s *Server) ClearFaults() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faults = map[string]*Fault{}
}

// Faults lists the faults still scheduled.
func (s *Server) Faults() []Fault {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Fault, 0, len(s.faults))
	for _, f := range s.faults {
		out = append(out, *f)
	}
	return out
}

// takeFaultLocked returns the fault that applies to a call, counting it down.
// Caller holds s.mu.
func (s *Server) takeFaultLocked(method string, params url.Values) *Fault {
	for _, key := range []string{strings.ToLower(method), "*"} {
		f, ok := s.faults[key]
		if !ok || !f.matches(params) {
			continue
		}
		hit := *f
		if f.Times > 0 {
			f.Times--
			if f.Times == 0 {
				delete(s.faults, key)
			}
		}
		return &hit
	}
	return nil
}

// matches reports whether the fault's Contains filter lets it fire on a call.
func (f *Fault) matches(params url.Values) bool {
	if f.Contains == "" {
		return true
	}
	for _, values := range params {
		for _, v := range values {
			if strings.Contains(v, f.Contains) {
				return true
			}
		}
	}
	return false
}

func defaultFaultDescription(code, retryAfter int) string {
	switch code {
	case 400:
		return "Bad Request"
	case 401:
		return "Unauthorized"
	case 403:
		return "Forbidden: bot was blocked by the user"
	case 404:
		return "Not Found"
	case 429:
		if retryAfter > 0 {
			return "Too Many Requests: retry after " + itoa(retryAfter)
		}
		return "Too Many Requests"
	case 502:
		return "Bad Gateway"
	default:
		return "Internal Server Error"
	}
}
