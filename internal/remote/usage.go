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
// streams: at session ready and after a turn's stream closed, on a
// goroutine so neither waits for the hub. A refresh the server deferred by
// its pacing floor answers refreshPending, and the console's own timer reads
// the cache again when it says so, the same way it does with a local
// backend, so this client arms no timer of its own.

// usageUnsupportedTTL is how long a provider the server reported as having
// no usage source is taken at its word before it is asked again.
const usageUnsupportedTTL = 5 * time.Minute

// providerUsageAnswer is the REST envelope of the usage route.
type providerUsageAnswer struct {
	OK          bool                     `json:"ok"`
	Unsupported bool                     `json:"unsupported"`
	Error       string                   `json:"error"`
	Usage       *acp.ProviderUsageUpdate `json:"usage"`
}

// usageState is the per-handler cache of unsupported providers, the closed
// flag that keeps a shut-down console from pulling, and the group of pulls
// in flight.
type usageState struct {
	usageMu          sync.Mutex
	usageUnsupported map[string]time.Time
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

// ProviderUsageForSession is the console's read; the server answers a
// deferred refresh with RefreshPending and the console's own timer reads
// the cache again when it says so, so the session id is not needed here.
func (h *Handler) ProviderUsageForSession(ctx context.Context, _ string, name string, refresh bool) (*acp.ProviderUsageUpdate, error) {
	return h.ProviderUsage(ctx, name, refresh)
}

// ProviderUsage reads the account usage behind a provider row from the
// remote server. A provider the server reported as unsupported is remembered
// for a while so non-metered models cost no round trips; a manual refresh
// asks again regardless.
func (h *Handler) ProviderUsage(ctx context.Context, name string, refresh bool) (*acp.ProviderUsageUpdate, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("remote: provider usage needs a provider name")
	}
	h.usageMu.Lock()
	if until, ok := h.usageUnsupported[name]; ok && !refresh && time.Now().Before(until) {
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
	h.usageMu.Lock()
	if answer.Unsupported {
		if h.usageUnsupported == nil {
			h.usageUnsupported = make(map[string]time.Time)
		}
		h.usageUnsupported[name] = time.Now().Add(usageUnsupportedTTL)
	} else {
		delete(h.usageUnsupported, name)
	}
	h.usageMu.Unlock()
	if answer.Unsupported {
		return &acp.ProviderUsageUpdate{SessionUpdate: acp.UpdateTypeProviderUsage, Provider: name, Unsupported: true}, nil
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
		answer.Usage.Provider = name
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
	provider := usageProviderOf(model)
	if provider == "" {
		return
	}
	u, err := h.ProviderUsage(ctx, provider, refresh)
	if err != nil {
		h.log.Debug("remote provider usage", "session", sessionID, "error", err)
		return
	}
	if u == nil || u.Unsupported || h.usageIsClosed() {
		return
	}
	if sender := h.currentSender(); sender != nil {
		_ = sender.SendSessionUpdate(sessionID, *u)
	}
}

// Close refuses new pulls and marks the ones in flight as not to be
// delivered: the console is quitting, nothing may fire into a dead sender.
func (h *Handler) Close() {
	h.usageMu.Lock()
	h.usageClosed = true
	h.usageMu.Unlock()
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
