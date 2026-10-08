package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"
)

// The client side of the application probe of a vanished peer (docs/plans/remote-model-provider-probe.md). Every completions call asks
// for it with X-Coddy-Probe: 1. A remote that supports it confirms in the same response with "id=<32 hex>; every_ms=<n>; grace_ms=<n>",
// and from then on the client posts the call's ping every n milliseconds while the stream runs, so the remote can tell a client that
// is alive from one that vanished behind an intermediary that acknowledges bytes for it. A remote that does not confirm, or confirms in
// a form this client does not read, is never pinged, and a ping never delays, blocks or fails the stream.

// coddyProbeFloor is the shortest interval the client pings at, whatever the remote asks for: the remote's constant is its own, and a
// remote asking for a ping every millisecond would only make this host post to it that often. A test lowers it.
var coddyProbeFloor = time.Second

// probePingBound bounds one ping to DMAX = half the interval (10 s asked, 5 s): a ping slower than that counts as lost, which is what keeps
// the bound of the model p4-probe true (a delivered ping arrives within DMAX), and a hung ping never lasts into the next tick.
func probePingBound(every time.Duration) time.Duration { return every / 2 }

var coddyProbeConfirmRE = regexp.MustCompile(`^id=([0-9a-f]{32}); every_ms=(\d+); grace_ms=(\d+)$`)

// parseProbeConfirmation reads the confirmation header of a response.
func parseProbeConfirmation(v string) (id string, every time.Duration, ok bool) {
	m := coddyProbeConfirmRE.FindStringSubmatch(v)
	if m == nil {
		return "", 0, false
	}
	ms, err := strconv.Atoi(m[2])
	if err != nil || ms <= 0 {
		return "", 0, false
	}
	return m[1], time.Duration(ms) * time.Millisecond, true
}

// probeInterval is the interval the client pings at for the one the remote asked for.
func probeInterval(asked time.Duration) time.Duration {
	if asked < coddyProbeFloor {
		return coddyProbeFloor
	}
	return asked
}

// startProbe starts the pinger of an admitted call when its response confirmed the probe, and returns the function that ends it and
// waits for it. Without a confirmation the function does nothing.
func (p *coddyProvider) startProbe(ctx context.Context, resp *http.Response) (stop func()) {
	id, asked, ok := parseProbeConfirmation(resp.Header.Get(CoddyProbeHeader))
	if !ok {
		return func() {}
	}
	target, err := coddyEndpoint(p.base, CoddyAlivePath)
	if err != nil {
		return func() {}
	}
	every := probeInterval(asked)
	pctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		// The first ping goes out at once, as the model p4-probe has it: a client that vanishes right after the headers is still cut
		// by the grace from the call's start, and one that stays is known to be alive early.
		if !p.ping(pctx, target, id, every) {
			return
		}
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-pctx.Done():
				return
			case <-ticker.C:
				if !p.ping(pctx, target, id, every) {
					return
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// ping posts one ping and reports whether the pinger goes on: only an unknown call (404 with code unknown_call) ends it. A transport
// error, a timeout, a 404 from somewhere else (a proxy that does not carry the route) or any other status is a failed ping, retried at
// the next interval, since the stream itself says whether the call is alive.
func (p *coddyProvider) ping(ctx context.Context, target, id string, every time.Duration) bool {
	bound := probePingBound(every)
	rctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, target, nil)
	if err != nil {
		return false
	}
	req.Header.Set(CoddyProbeIDHeader, id)
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	resp, err := p.hc.Do(req)
	if err != nil {
		return ctx.Err() == nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return true
	}
	var e WireError
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if json.Unmarshal(body, &e) != nil {
		return true
	}
	return e.Kind != WireKindInvalid || e.Code != WireCodeUnknownCall
}
