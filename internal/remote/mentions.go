package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// SearchMentions asks the server for the "@" candidates of a draft (GET
// /coddy/mentions), so a console in remote mode offers the server's
// workspace - where the session runs and where its mentions resolve - rather
// than the folder the console was started in.
func (h *Handler) SearchMentions(ctx context.Context, req session.MentionSearch) (session.MentionSearchResult, error) {
	q := url.Values{"q": {req.Query}}
	if req.Limit > 0 {
		q.Set("limit", strconv.Itoa(req.Limit))
	}
	if req.Refresh {
		q.Set("refresh", "1")
	}
	ctx, cancel := context.WithTimeout(ctx, restTimeout)
	defer cancel()
	hr, err := h.newRequest(ctx, http.MethodGet, "/coddy/mentions?"+q.Encode(), nil)
	if err != nil {
		return session.MentionSearchResult{}, err
	}
	if req.SessionID != "" {
		hr.Header.Set("X-Coddy-Session-ID", req.SessionID)
	}
	res, err := h.hc.Do(hr)
	if err != nil {
		return session.MentionSearchResult{}, fmt.Errorf("remote coddy %s: %w", h.opts.BaseURL, err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode != http.StatusOK {
		return session.MentionSearchResult{}, h.remoteError(res, body)
	}
	var out session.MentionSearchResult
	if err := json.Unmarshal(body, &out); err != nil {
		return session.MentionSearchResult{}, err
	}
	if out.Items == nil {
		out.Items = []session.MentionCandidate{}
	}
	return out, nil
}
