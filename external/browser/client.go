//go:build browser

package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// listTimeout bounds asking a server for its browsers: a listing is quick,
// and a server that does not answer is reported rather than waited for.
const listTimeout = 15 * time.Second

// Remote is another Coddy server whose browsers this process's chats use,
// reached through its HTTP API: the server of this machine for a console or
// an editor, or a node of the swarm through a relay mount.
type Remote struct {
	// Label names the server in browser names (<browser id>@<label>); ""
	// for the server of this machine, whose browsers keep their bare ids.
	Label string
	// Base is the server's address, a relay mount included.
	Base string
	// Token is the bearer credential of that server's API ("" sends none).
	Token string
	// HTTP is the client; nil uses one without a timeout of its own, since a
	// call lasts as long as its action and its context bounds it.
	HTTP *http.Client
}

func (r *Remote) client() *http.Client {
	if r.HTTP != nil {
		return r.HTTP
	}
	return &http.Client{}
}

func (r *Remote) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(r.Base, "/")+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if r.Token != "" {
		req.Header.Set("Authorization", "Bearer "+r.Token)
	}
	res, err := r.client().Do(req)
	if err != nil {
		return fmt.Errorf("%s could not be reached: %w", r.name(), err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxCallBody+1))
	if err != nil {
		return fmt.Errorf("%s did not answer in full: %w", r.name(), err)
	}
	switch {
	case res.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%s does not serve its browsers: it needs browser.serve_remote and a binary with the browser module", r.name())
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%s refused the credential (HTTP %d); check the token of that remote", r.name(), res.StatusCode)
	case res.StatusCode/100 != 2:
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("%s answered HTTP %d", r.name(), res.StatusCode)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s sent an answer that is not JSON", r.name())
	}
	return nil
}

func (r *Remote) name() string {
	if r.Label == "" {
		return "the Coddy server of this machine (" + r.Base + ")"
	}
	return "the Coddy server " + r.Label
}

// Browsers lists the server's browsers as the caller's chat sees them.
func (r *Remote) Browsers(ctx context.Context, c Caller) ([]Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	q := url.Values{}
	q.Set("node", c.Node)
	q.Set("session_id", c.SessionID)
	var out BrowsersResponse
	if err := r.do(ctx, http.MethodGet, "/coddy/browser/v1/browsers?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	if out.Browsers == nil {
		out.Browsers = []Snapshot{}
	}
	return out.Browsers, nil
}

// Call asks the server's browser for one action. Like a local call it never
// retries: a lost answer is an unknown outcome.
func (r *Remote) Call(ctx context.Context, req CallRequest) (*CallResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, actionTimeout(req.Action)+30*time.Second)
	defer cancel()
	var out CallResponse
	if err := r.do(ctx, http.MethodPost, "/coddy/browser/v1/call", req, &out); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("the browser action was cancelled or timed out; inspect the page before trying again: %w", ctx.Err())
		}
		return nil, err
	}
	return &out, nil
}
