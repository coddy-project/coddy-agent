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
// cache, so the numbers come from GET /coddy/providers/{name}/usage and from
// the provider_usage SSE frames of a turn. The client pulls when nothing
// streams: at session ready, after a turn's stream closed, after a model
// switch, and once more when the server said a refresh was deferred.

// usageFollowUpGrace is added to the server's refreshInSec before the single
// follow-up pull, so the deferred fetch has landed. A variable so tests can
// shorten the wait.
var usageFollowUpGrace = 2 * time.Second

// providerUsageAnswer is the REST envelope of the usage route.
type providerUsageAnswer struct {
	OK          bool                     `json:"ok"`
	Unsupported bool                     `json:"unsupported"`
	Error       string                   `json:"error"`
	Usage       *acp.ProviderUsageUpdate `json:"usage"`
}

// usageProviderOf names the provider row behind a model selector: the part
// before the first slash (config.SplitModelRef), which is all a remote
// client has.
func usageProviderOf(modelID string) string {
	provider, _, _ := config.SplitModelRef(strings.TrimSpace(modelID))
	return provider
}

// ProviderUsageForSession is the console's read; the server answers a
// deferred refresh with RefreshPending and the console's own timer reads
// the cache again when it says so, so the session id is not needed here.
func (h *Handler) ProviderUsageForSession(ctx context.Context, _ string, name string, refresh bool) (*acp.ProviderUsageUpdate, error) {
	return h.ProviderUsage(ctx, name, refresh)
}

// ProviderUsage reads the account usage behind a provider row from the
// remote server. A provider the server reported as unsupported is cached so
// non-metered models cost no round trips.
func (h *Handler) ProviderUsage(ctx context.Context, name string, refresh bool) (*acp.ProviderUsageUpdate, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("remote: provider usage needs a provider name")
	}
	// A manual refresh asks the server again even for a provider it once
	// reported as unsupported: the row's type may have changed since.
	h.usageMu.Lock()
	if !refresh && h.usageUnsupported != nil && h.usageUnsupported[name] {
		h.usageMu.Unlock()
		return &acp.ProviderUsageUpdate{SessionUpdate: acp.UpdateTypeProviderUsage, Provider: name, Unsupported: true}, nil
	}
	h.usageMu.Unlock()

	path := "/coddy/providers/" + url.PathEscape(name) + "/usage"
	if refresh {
		path += "?refresh=1"
	}
	var answer providerUsageAnswer
	if err := h.getJSON(ctx, path, &answer); err != nil {
		return nil, err
	}
	if answer.Unsupported {
		h.usageMu.Lock()
		if h.usageUnsupported == nil {
			h.usageUnsupported = make(map[string]bool)
		}
		h.usageUnsupported[name] = true
		h.usageMu.Unlock()
		return &acp.ProviderUsageUpdate{SessionUpdate: acp.UpdateTypeProviderUsage, Provider: name, Unsupported: true}, nil
	}
	if answer.Usage == nil {
		if answer.Error != "" {
			return nil, fmt.Errorf("remote: provider usage: %s", answer.Error)
		}
		return nil, fmt.Errorf("remote: provider usage: empty answer")
	}
	// A supported answer clears an older unsupported mark for the row.
	h.usageMu.Lock()
	delete(h.usageUnsupported, name)
	h.usageMu.Unlock()
	if answer.Usage.SessionUpdate == "" {
		answer.Usage.SessionUpdate = acp.UpdateTypeProviderUsage
	}
	if answer.Usage.Provider == "" {
		answer.Usage.Provider = name
	}
	return answer.Usage, nil
}

// pullProviderUsageAsync runs pullProviderUsage on its own goroutine with a
// REST timeout: neither a turn's result nor a session load waits for the
// hub round trip. A closed handler pulls nothing.
func (h *Handler) pullProviderUsageAsync(sessionID string, refresh bool) {
	if h.usageIsClosed() {
		return
	}
	h.usageWG.Add(1)
	go func() {
		defer h.usageWG.Done()
		ctx, cancel := context.WithTimeout(context.Background(), restTimeout)
		defer cancel()
		h.pullProviderUsage(ctx, sessionID, refresh)
	}()
}

// pullProviderUsage reads the usage behind a session's model and delivers it
// to the current sender as if it had streamed. When the server deferred the
// refresh it schedules one follow-up pull, replacing any pending one; a
// session that is forgotten cancels it.
func (h *Handler) pullProviderUsage(ctx context.Context, sessionID string, refresh bool) {
	if h.usageIsClosed() {
		return
	}
	st := h.session(sessionID)
	h.mu.Lock()
	model := st.modelID
	if model == "" {
		model = h.defModel
	}
	h.mu.Unlock()
	provider := usageProviderOf(model)
	if provider == "" {
		return
	}
	u, err := h.ProviderUsage(ctx, provider, refresh)
	if err != nil {
		h.log.Debug("remote provider usage", "session", sessionID, "error", err)
		return
	}
	if u == nil || u.Unsupported {
		return
	}
	if h.usageIsClosed() {
		return
	}
	if sender := h.currentSender(); sender != nil {
		_ = sender.SendSessionUpdate(sessionID, *u)
	}
	h.scheduleUsageFollowUp(st, sessionID, u)
}

// scheduleUsageFollowUp arms the single follow-up pull a deferred refresh
// asks for, cancelling a previous one for the session.
func (h *Handler) scheduleUsageFollowUp(st *sessionState, sessionID string, u *acp.ProviderUsageUpdate) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if st.usageFollowUp != nil {
		st.usageFollowUp.Stop()
		st.usageFollowUp = nil
	}
	if u == nil || !u.RefreshPending || h.usageIsClosed() {
		return
	}
	delay := time.Duration(u.RefreshInSec)*time.Second + usageFollowUpGrace
	st.usageFollowUp = time.AfterFunc(delay, func() {
		h.mu.Lock()
		live := h.sessions[sessionID] == st
		h.mu.Unlock()
		if !live {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), restTimeout)
		defer cancel()
		h.pullProviderUsageOnce(ctx, sessionID)
	})
}

// pullProviderUsageOnce is the follow-up pull: it delivers the answer and
// never schedules another one.
func (h *Handler) pullProviderUsageOnce(ctx context.Context, sessionID string) {
	st := h.session(sessionID)
	h.mu.Lock()
	model := st.modelID
	if model == "" {
		model = h.defModel
	}
	h.mu.Unlock()
	provider := usageProviderOf(model)
	if provider == "" {
		return
	}
	u, err := h.ProviderUsage(ctx, provider, false)
	if err != nil || u == nil || u.Unsupported {
		return
	}
	if sender := h.currentSender(); sender != nil {
		_ = sender.SendSessionUpdate(sessionID, *u)
	}
}

// stopUsageFollowUp cancels a pending follow-up pull (session forgotten).
func stopUsageFollowUp(st *sessionState) {
	if st != nil && st.usageFollowUp != nil {
		st.usageFollowUp.Stop()
		st.usageFollowUp = nil
	}
}

// usageState is the per-handler cache of unsupported providers, the closed
// flag that keeps a shut-down console from arming new timers, and the
// group of pulls in flight.
type usageState struct {
	usageMu          sync.Mutex
	usageUnsupported map[string]bool
	usageClosed      bool
	usageWG          sync.WaitGroup
}

// Close stops every pending follow-up pull and refuses new ones: the console
// is quitting, nothing may fire into a dead sender. Pulls already in flight
// finish on their own (bounded by the REST timeout) and find the handler
// closed.
func (h *Handler) Close() {
	h.usageMu.Lock()
	h.usageClosed = true
	h.usageMu.Unlock()
	h.mu.Lock()
	for _, st := range h.sessions {
		stopUsageFollowUp(st)
	}
	h.mu.Unlock()
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

// usageIsClosed reports whether Close ran.
func (h *Handler) usageIsClosed() bool {
	h.usageMu.Lock()
	defer h.usageMu.Unlock()
	return h.usageClosed
}
