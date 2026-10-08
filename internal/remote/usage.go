package remote

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// Provider usage on the remote console: the server owns the key and the
// cache, so the numbers come from GET /coddy/providers/{name}/usage (with
// ?model=<alias> for a row of type coddy, whose usage is read per alias) and
// from the provider_usage SSE frames of a turn. The client pulls when nothing
// streams: at session ready and after a turn's stream closed, on a
// goroutine so neither waits for the hub. A refresh the server deferred by
// its pacing floor answers refreshPending, and the console's own timer reads
// the cache again when it says so, the same way it does with a local
// backend, so this client arms no timer of its own.

// usageUnsupportedTTL is how long a provider the server reported as having
// no usage source is taken at its word before it is asked again. A row of type
// coddy is never marked: the server remembers an alias that has no usage for
// its own, shorter, time, and a second memory here would add to it.
const usageUnsupportedTTL = 5 * time.Minute

// coddyProviderType is the provider type of a row that shares a remote Coddy's
// models; its usage belongs to the alias, not to the row.
const coddyProviderType = "coddy"

// providerUsageAnswer is the REST envelope of the usage route. Provider and
// ProviderType name the row on the unsupported answer, which carries no
// snapshot.
type providerUsageAnswer struct {
	OK           bool                     `json:"ok"`
	Unsupported  bool                     `json:"unsupported"`
	Disabled     bool                     `json:"disabled"`
	Provider     string                   `json:"provider"`
	ProviderType string                   `json:"providerType"`
	Model        string                   `json:"model"`
	Error        string                   `json:"error"`
	Usage        *acp.ProviderUsageUpdate `json:"usage"`
}

// usageUnsupportedMark remembers an unsupported row until it expires;
// disabled says the row's panel is switched off on the server rather than
// the type having no source, so /usage can name the switch.
type usageUnsupportedMark struct {
	until        time.Time
	providerType string
	disabled     bool
}

// usageState is the per-handler cache of unsupported providers, the closed
// flag that keeps a shut-down console from pulling, and the group of pulls
// in flight.
type usageState struct {
	usageMu          sync.Mutex
	usageUnsupported map[string]usageUnsupportedMark
	usageClosed      bool
	usageWG          sync.WaitGroup
}

// usageProviderOf names the provider row behind a model selector: the part
// before the first slash (config.SplitModelRef), which is all a remote
// client has.
func usageProviderOf(modelID string) string {
	provider, _, _ := config.SplitModelRef(strings.TrimSpace(modelID))
	return provider
}

// splitUsageSubject reads what the console passes to a usage read: a provider
// row's name, or a model selector "provider/alias" that also names the alias a
// row of type coddy is read for. The alias is everything after the first slash,
// the way a selector is split everywhere; it is empty for a bare row name.
func splitUsageSubject(name string) (provider, alias string) {
	name = strings.TrimSpace(name)
	if i := strings.IndexByte(name, '/'); i >= 0 {
		return strings.TrimSpace(name[:i]), strings.TrimSpace(name[i+1:])
	}
	return name, ""
}

// ProviderUsageForSession is the console's read; the server answers a
// deferred refresh with RefreshPending and the console's own timer reads
// the cache again when it says so, so the session id is not needed here.
func (h *Handler) ProviderUsageForSession(ctx context.Context, _ string, name string, refresh bool) (*acp.ProviderUsageUpdate, error) {
	return h.ProviderUsage(ctx, name, refresh)
}

// ProviderUsage reads the account usage behind a provider row from the remote
// server. name is the row's name or a model selector "provider/alias": the
// alias is sent as ?model= whenever there is one (the server needs it for a row
// of type coddy and ignores it for every other type, so a caller can always pass
// the selector). A provider the server reported as unsupported is remembered
// for a while so non-metered models cost no round trips; a manual refresh asks
// again regardless. A row of type coddy is never remembered here: the server's
// own memory per alias is the only one.
func (h *Handler) ProviderUsage(ctx context.Context, name string, refresh bool) (*acp.ProviderUsageUpdate, error) {
	provider, alias := splitUsageSubject(name)
	if provider == "" {
		return nil, fmt.Errorf("remote: provider usage needs a provider name")
	}
	h.usageMu.Lock()
	if mark, ok := h.usageUnsupported[provider]; ok && !refresh && time.Now().Before(mark.until) {
		h.usageMu.Unlock()
		return &acp.ProviderUsageUpdate{SessionUpdate: acp.UpdateTypeProviderUsage, Provider: provider, ProviderType: mark.providerType, Unsupported: true, Disabled: mark.disabled}, nil
	}
	h.usageMu.Unlock()

	path := "/coddy/providers/" + url.PathEscape(provider) + "/usage"
	query := url.Values{}
	if alias != "" {
		query.Set("model", alias)
	}
	if refresh {
		query.Set("refresh", "1")
	}
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	var answer providerUsageAnswer
	if err := h.getJSON(ctx, path, &answer); err != nil {
		return nil, err
	}
	coddy := answer.ProviderType == coddyProviderType
	h.usageMu.Lock()
	if answer.Unsupported && !coddy {
		if h.usageUnsupported == nil {
			h.usageUnsupported = make(map[string]usageUnsupportedMark)
		}
		h.usageUnsupported[provider] = usageUnsupportedMark{until: time.Now().Add(usageUnsupportedTTL), providerType: answer.ProviderType, disabled: answer.Disabled}
	} else {
		delete(h.usageUnsupported, provider)
	}
	h.usageMu.Unlock()
	if answer.Unsupported {
		u := &acp.ProviderUsageUpdate{SessionUpdate: acp.UpdateTypeProviderUsage, Provider: provider, ProviderType: answer.ProviderType, Unsupported: true, Disabled: answer.Disabled}
		if coddy {
			u.Model = answer.Model
			if u.Model == "" {
				u.Model = alias
			}
		}
		return u, nil
	}
	if answer.Usage == nil {
		if answer.Error != "" {
			return nil, fmt.Errorf("remote: provider usage: %s", answer.Error)
		}
		return nil, fmt.Errorf("remote: provider usage: empty answer")
	}
	if answer.Usage.SessionUpdate == "" {
		answer.Usage.SessionUpdate = acp.UpdateTypeProviderUsage
	}
	if answer.Usage.Provider == "" {
		answer.Usage.Provider = provider
	}
	// A server that predates the alias on the wire still answers for the alias
	// the read was about, so the console keeps it under its own subject.
	if answer.Usage.Model == "" && answer.Usage.ProviderType == coddyProviderType {
		answer.Usage.Model = alias
	}
	return answer.Usage, nil
}

// pullProviderUsageAsync runs pullProviderUsage on its own goroutine with a
// REST timeout: neither a turn's result nor a session load waits for the
// hub round trip. The registration and the closed check happen under one
// lock, so Close cannot slip between them; a closed handler pulls nothing.
func (h *Handler) pullProviderUsageAsync(sessionID string, refresh bool) {
	h.usageMu.Lock()
	if h.usageClosed {
		h.usageMu.Unlock()
		return
	}
	h.usageWG.Add(1)
	h.usageMu.Unlock()
	go func() {
		defer h.usageWG.Done()
		ctx, cancel := context.WithTimeout(context.Background(), restTimeout)
		defer cancel()
		h.pullProviderUsage(ctx, sessionID, refresh)
	}()
}

// pullProviderUsage reads the usage behind a session's model and delivers it
// to the current sender as if it had streamed. Nothing is sent once the
// handler is closed, even when the read was already in flight.
func (h *Handler) pullProviderUsage(ctx context.Context, sessionID string, refresh bool) {
	if h.usageIsClosed() {
		return
	}
	// A session forgotten meanwhile (a quick /new after the ready pull) is
	// not recreated by its own usage read.
	h.mu.Lock()
	st, live := h.sessions[sessionID]
	model := ""
	if live {
		model = st.modelID
	}
	if model == "" {
		model = h.defModel
	}
	h.mu.Unlock()
	if !live {
		return
	}
	if usageProviderOf(model) == "" {
		return
	}
	// The selector, not the row: a row of type coddy is read for the alias.
	u, err := h.ProviderUsage(ctx, model, refresh)
	if err != nil {
		h.log.Debug("remote provider usage", "session", sessionID, "error", err)
		return
	}
	// A type without a source stays silent, as before; a row whose panel the
	// server switched off is forwarded, so the console takes a stale line
	// down (its own reads do the same for the local backend).
	if u == nil || (u.Unsupported && !u.Disabled) || h.usageIsClosed() {
		return
	}
	if sender := h.currentSender(); sender != nil {
		_ = sender.SendSessionUpdate(sessionID, *u)
	}
}

// Close refuses new pulls and marks the ones in flight as not to be
// delivered: the console is quitting, nothing may fire into a dead sender.
func (h *Handler) Close() {
	h.controlStop()
	h.usageMu.Lock()
	h.usageClosed = true
	h.usageMu.Unlock()
	h.StopEvents()
	// The subagent prompts this client offered run on the control context,
	// which is cancelled above, so none can post an answer after Close; the
	// woken turns it follows read on the same context.
	h.detachedWG.Wait()
	h.stopFollowing()
}

// usageIsClosed reports whether Close ran.
func (h *Handler) usageIsClosed() bool {
	h.usageMu.Lock()
	defer h.usageMu.Unlock()
	return h.usageClosed
}

// WaitUsage joins the pulls in flight, up to d (tests and a clean exit).
func (h *Handler) WaitUsage(d time.Duration) {
	done := make(chan struct{})
	go func() {
		h.usageWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
	}
}
