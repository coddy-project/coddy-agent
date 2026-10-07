package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// coddyListingLimit bounds a listing a remote sends: a hundred rows are a few
// kilobytes, and the remote is untrusted as far as memory goes.
const coddyListingLimit = 4 << 20

// WireCodeProtocolMismatch is the code of the invalid error a remote answers a
// request of another protocol version with; the message names both versions.
const WireCodeProtocolMismatch = "protocol_mismatch"

// listCoddyModels reads the models a remote Coddy shares, at
// <api_base>/coddy/llm/models. Each row carries the alias as its id and the
// capabilities the client takes from the remote instead of guessing them from
// the id: the context window, the revision of the row, multimodal, the
// reasoning levels, the default level and whether off is allowed. The protocol
// is checked strictly, and an answer that is not the listing (a remote built
// without the routes answers its web page) is the clear "does not offer shared
// models" error.
func listCoddyModels(ctx context.Context, in ProviderInput) ([]ModelEntry, error) {
	endpoint, err := coddyEndpoint(in.BaseURL, CoddyModelsPath)
	if err != nil {
		return nil, err
	}
	hc, err := HTTPClientForProviderProxy(in.ProxyURL)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, modelListTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if in.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+in.APIKey)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, redactURLError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, coddyListingLimit+1))
	if err != nil {
		return nil, fmt.Errorf("list models: read: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, classifyCoddyAnswer(resp.StatusCode, resp.Header, body)
	}
	if len(body) > coddyListingLimit {
		return nil, &coddyAPIError{kind: CoddyKindProtocol, message: "the remote's model listing is larger than 4 MiB"}
	}
	var listing WireListing
	if json.Unmarshal(body, &listing) != nil || listing.Protocol == 0 {
		return nil, &coddyAPIError{kind: CoddyKindUnsupported, status: resp.StatusCode,
			message: "the remote does not offer shared models: its answer at " + CoddyModelsPath + " is not a model listing (is api_base a remote coddy serve with shared models, or a swarm relay mount?)"}
	}
	if listing.Protocol != CoddyProtocol {
		return nil, &coddyAPIError{kind: WireKindInvalid, code: WireCodeProtocolMismatch, status: http.StatusBadRequest,
			message: fmt.Sprintf("the remote speaks shared-model protocol %d and this Coddy speaks %d: update the older one", listing.Protocol, CoddyProtocol)}
	}

	seen := make(map[string]struct{}, len(listing.Data))
	out := make([]ModelEntry, 0, len(listing.Data))
	for _, row := range listing.Data {
		id := strings.TrimSpace(row.ID)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, ModelEntry{
			ID:                id,
			ContextWindow:     max(row.MaxContextTokens, 0),
			Revision:          row.Revision,
			Multimodal:        row.Multimodal,
			ReasoningLevels:   append([]string(nil), row.ReasoningLevels...),
			ReasoningDefault:  row.ReasoningDefault,
			AllowReasoningOff: row.AllowReasoningOff,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// redactURLError strips a credential in the URL (api_base with userinfo) from
// the text of a transport error.
func redactURLError(err error) error {
	if ue, ok := err.(*url.Error); ok {
		if u, perr := url.Parse(ue.URL); perr == nil && u.User != nil {
			ue.URL = u.Redacted()
		}
	}
	return err
}
