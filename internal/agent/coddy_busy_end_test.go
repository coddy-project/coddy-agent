package agent

// The countdown of a call that waits for a free stream slot of a remote Coddy,
// and its end (docs/plans/remote-model-provider-phase2.md, 4.6; decided by
// docs/plans/remote-model-provider-models/p2-d4-busy-notice.md): the agent
// sends the countdown and, when the wait is over, one explicit end update on the
// same fields - the blocker remote_busy with Resuming absent and Blocked false,
// stamped by the clock at the moment of sending - and nothing else. It names
// the alias the row shares the model under, not the provider/alias selector, so
// a surface keeps one alias's countdown apart from another's. It never reads a
// usage cache and never sends an Unsupported answer.

import (
	"context"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// coddyTransport is the transport getProvider builds for the coddy row of the
// tests: models[].model is the selector provider/alias.
func coddyTransport() llmTransport {
	return llmTransport{providerName: "remote", providerType: "coddy", model: coddyRow}
}

// carriesBusyBlocker reports whether an update belongs to the countdown family.
func carriesBusyBlocker(u acp.ProviderUsageUpdate) bool {
	for _, b := range u.Blockers {
		if b == "remote_busy" {
			return true
		}
	}
	return false
}

// isBusyEnd reports whether an update is the end of a countdown: the family's
// blocker without Resuming, Blocked, retry fields or Unsupported.
func isBusyEnd(u acp.ProviderUsageUpdate) bool {
	return carriesBusyBlocker(u) && !u.Resuming && !u.Blocked && !u.Unsupported && u.RetryAt == "" && u.RetryInSec == 0
}

// requireBusyEnd fails unless u is the end update of the countdown of the alias
// on the provider row: the same blocker, no Resuming, not Blocked, no retry
// fields, no Unsupported, no usage data of any kind, and the clock's stamp.
func requireBusyEnd(t *testing.T, u acp.ProviderUsageUpdate, provider, alias string) {
	t.Helper()
	if u.Unsupported {
		t.Fatalf("end update = %+v: Unsupported tells a surface to drop the row's usage", u)
	}
	if !isBusyEnd(u) {
		t.Fatalf("update = %+v, want the end of the countdown: blocker remote_busy, no Resuming, not Blocked, no retry fields", u)
	}
	if u.Provider != provider || u.ProviderType != "coddy" || u.Model != alias {
		t.Fatalf("end names %s/%s model %q, want %s/coddy model %q", u.Provider, u.ProviderType, u.Model, provider, alias)
	}
	if len(u.Blockers) != 1 {
		t.Fatalf("end blockers = %v, want exactly [remote_busy]", u.Blockers)
	}
	stamp, err := time.Parse(time.RFC3339, u.FetchedAt)
	if err != nil || stamp.Location() != time.UTC || u.FetchedAt[len(u.FetchedAt)-1] != 'Z' {
		t.Fatalf("end fetchedAt = %q (%v), want an RFC 3339 UTC stamp", u.FetchedAt, err)
	}
	// Nothing else: no plan, window, wallet, key or model list rides on an end.
	rest := u
	rest.SessionUpdate, rest.Provider, rest.ProviderType, rest.Model = "", "", "", ""
	rest.ObservedAt, rest.FetchedAt, rest.Blockers = "", "", nil
	if !reflect.DeepEqual(rest, acp.ProviderUsageUpdate{}) {
		t.Fatalf("end update carries more than the family's fields: %+v", rest)
	}
}

// busyKinds splits the provider_usage updates of a run into the countdown
// notices, the ends and everything else.
func busyKinds(all []acp.ProviderUsageUpdate) (countdowns, ends, others []acp.ProviderUsageUpdate) {
	for _, u := range all {
		switch {
		case carriesBusyBlocker(u) && (u.Resuming || u.Blocked):
			countdowns = append(countdowns, u)
		case isBusyEnd(u):
			ends = append(ends, u)
		default:
			others = append(others, u)
		}
	}
	return countdowns, ends, others
}

// The alias is rm.Model of the resolved row: the part of models[].model after
// the provider name, slashes of the alias included. The selector never leaves.
func TestBusyNoticeNamesTheAliasAndNotTheSelector(t *testing.T) {
	for _, tc := range []struct{ selector, alias string }{
		{"remote/coder", "coder"},
		{"remote/org/coder", "org/coder"},
		{"remote/gpt-5.5", "gpt-5.5"},
	} {
		t.Run(tc.selector, func(t *testing.T) {
			sender := &limitWaitCapture{}
			ag := NewAgent(coddyConfig("https://remote.example", nil), &session.State{ID: "sess_alias"}, sender, nil)
			ag.limitWaitHeartbeat = time.Hour
			tr := coddyTransport()
			tr.model = tc.selector
			notice := ag.newBusyWaitNotice("sess_alias", tr)

			before := time.Now().UTC().Truncate(time.Second)
			notice.report(llm.BusyWaitStatus{Budget: 30 * time.Second, Remaining: 30 * time.Second, RetryIn: time.Second})
			notice.clear()

			all := sender.snapshot()
			if len(all) != 2 {
				t.Fatalf("updates = %+v, want the countdown and its end", all)
			}
			count, end := all[0], all[1]
			if !count.Resuming || !count.Blocked || count.Model != tc.alias || count.Provider != "remote" || count.ProviderType != "coddy" {
				t.Fatalf("countdown = %+v, want a resuming, blocked update of remote/coddy naming the alias %q", count, tc.alias)
			}
			requireBusyEnd(t, end, "remote", tc.alias)
			// The stamp is the clock at the moment of sending, never anything a
			// cache says (a cache's fetchedAt lies in the past), and an end is
			// strictly newer than the countdown it ends. The generator may run
			// ahead of the clock to keep stamps strictly increasing, so only the
			// lower bound is the clock's.
			stamp, _ := time.Parse(time.RFC3339, end.FetchedAt)
			if stamp.Before(before) {
				t.Fatalf("end stamped %s, before the sending window began at %s", end.FetchedAt, before.Format(time.RFC3339))
			}
			if end.FetchedAt <= count.FetchedAt {
				t.Fatalf("end %s is not newer than its countdown %s", end.FetchedAt, count.FetchedAt)
			}
		})
	}
}

// A transport the loop built has the selector in model; a notice made for one
// that names none (a hand-built transport of a test) names no alias rather than
// guessing one, and every other provider type shows nothing at all.
func TestBusyNoticeWithoutASelectorNamesNoAlias(t *testing.T) {
	sender := &limitWaitCapture{}
	ag := NewAgent(coddyConfig("https://remote.example", nil), &session.State{ID: "sess_noalias"}, sender, nil)
	notice := ag.newBusyWaitNotice("sess_noalias", llmTransport{providerName: "remote", providerType: "coddy"})
	notice.report(llm.BusyWaitStatus{Remaining: time.Second})
	notice.clear()
	all := sender.snapshot()
	if len(all) != 2 || all[0].Model != "" || all[1].Model != "" {
		t.Fatalf("updates = %+v, want a countdown and an end that name no alias", all)
	}
}

// The end exists on every exit of a call that showed a countdown: the remote
// admits it, the call fails, the turn is cancelled. Whatever the exit, the last
// provider_usage update of the turn is one end, no Unsupported answer was ever
// sent, and every update names the alias.
func TestBusyEndExistsOnEveryExit(t *testing.T) {
	busy := func(w http.ResponseWriter) {
		refuse(w, http.StatusTooManyRequests, llm.WireError{Kind: llm.WireKindBusy}, "Retry-After", "1")
	}
	check := func(t *testing.T, r *coddyRun) {
		t.Helper()
		all := r.sender.snapshot()
		count, ends, others := busyKinds(all)
		if len(count) == 0 {
			t.Fatal("the wait showed no countdown, so the test proves nothing about its end")
		}
		for _, u := range count {
			if u.Model != "coder" || u.Provider != "remote" {
				t.Fatalf("countdown = %+v, want it to name remote and the alias coder", u)
			}
		}
		if len(ends) != 1 {
			t.Fatalf("%d end updates, want exactly one (the three sites share one idempotent clear): %+v", len(ends), all)
		}
		for _, u := range others {
			if u.Unsupported {
				t.Fatalf("an Unsupported answer was sent (%+v): a surface would drop the row's usage", u)
			}
		}
		last := all[len(all)-1]
		requireBusyEnd(t, last, "remote", "coder")
		if last.FetchedAt <= count[len(count)-1].FetchedAt {
			t.Fatalf("the end (%s) is not newer than the last countdown (%s)", last.FetchedAt, count[len(count)-1].FetchedAt)
		}
	}
	tune := func(c *config.Config) { c.Providers[0].BusyWaitMS = 10000 }

	t.Run("admission", func(t *testing.T) {
		rm := newRemote(t, func(w http.ResponseWriter, _ *http.Request, n int, _ llm.WireRequest) {
			if n == 1 {
				busy(w)
				return
			}
			f := startFrames(w)
			f.text("ok")
			f.final("ok")
		})
		r := newCoddyRun(t, rm, tune)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		r.run(ctx)
		if r.err != nil {
			t.Fatalf("Run: %v", r.err)
		}
		check(t, r)
	})

	t.Run("error", func(t *testing.T) {
		rm := newRemote(t, func(w http.ResponseWriter, _ *http.Request, n int, _ llm.WireRequest) {
			if n == 1 {
				busy(w)
				return
			}
			refuse(w, http.StatusBadRequest, llm.WireError{Kind: llm.WireKindInvalid})
		})
		r := newCoddyRun(t, rm, tune)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		r.run(ctx)
		if r.err == nil {
			t.Fatalf("Run succeeded (stop %q), want the remote's refusal", r.stop)
		}
		check(t, r)
	})

	t.Run("cancel", func(t *testing.T) {
		rm := newRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ llm.WireRequest) { busy(w) })
		r := newCoddyRun(t, rm, func(c *config.Config) { c.Providers[0].BusyWaitMS = 60000 })
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan struct{})
		go func() { r.run(ctx); close(done) }()

		// Stop only once the countdown is on screen, or there is nothing to end.
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if c, _, _ := busyKinds(r.sender.snapshot()); len(c) > 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		r.st.SetUserCancelledTurn()
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Stop did not end the wait for a free slot")
		}
		if r.stop != string(acp.StopReasonCancelled) {
			t.Fatalf("stop = %q (err %v), want cancelled", r.stop, r.err)
		}
		check(t, r)
	})
}

// A call that was never told to wait shows nothing and ends nothing, whatever
// its exit: no provider_usage update at all.
func TestACallThatNeverWaitedSendsNoCountdownAndNoEnd(t *testing.T) {
	rm := newRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ llm.WireRequest) {
		f := startFrames(w)
		f.text("ok")
		f.final("ok")
	})
	r := newCoddyRun(t, rm, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	r.run(ctx)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if all := r.sender.snapshot(); len(all) != 0 {
		t.Fatalf("provider_usage updates = %+v, want none", all)
	}
}

// The surfaces compare the stamps of a countdown with the tombstone an end left
// at one second of resolution, so two updates of one process must never share a
// stamp: a quick answer, an instant tool or a busy remote would otherwise put a
// new countdown in the same second as the previous call's end, and the surface
// would drop it as old.
func TestBusyStampsAreStrictlyIncreasingWithinOneSecond(t *testing.T) {
	var g stampGenerator
	now := time.Date(2026, 10, 8, 12, 0, 0, 250_000_000, time.UTC)
	var prev time.Time
	for i := 0; i < 5; i++ {
		// The clock does not move: five stamps in one wall second.
		s := g.next(now)
		stamp, err := time.Parse(time.RFC3339, s)
		if err != nil || stamp.Location() != time.UTC || s[len(s)-1] != 'Z' {
			t.Fatalf("stamp %q (%v) is not an RFC 3339 UTC stamp", s, err)
		}
		if i > 0 && !stamp.After(prev) {
			t.Fatalf("stamp %d = %s is not after the previous %s", i, s, prev.Format(time.RFC3339))
		}
		if i == 0 && !stamp.Equal(now.Truncate(time.Second)) {
			t.Fatalf("the first stamp %s is not the clock's second %s", s, now.Truncate(time.Second).Format(time.RFC3339))
		}
		prev = stamp
	}
	// A clock that has moved past the generator is believed again.
	later := now.Add(time.Minute)
	if s := g.next(later); s != later.Truncate(time.Second).Format(time.RFC3339) {
		t.Fatalf("after the clock moved on the stamp is %s, want the clock's second", s)
	}
	// A clock that steps back never produces an older stamp.
	back := g.next(now)
	stampBack, _ := time.Parse(time.RFC3339, back)
	if !stampBack.After(later.Truncate(time.Second)) {
		t.Fatalf("a clock stepped back produced %s, not after %s", back, later.Truncate(time.Second).Format(time.RFC3339))
	}
}

// A countdown that starts in the wall second the previous call ended in is
// stamped strictly after that call's end, and every stamp of a call is strictly
// after the one before it.
func TestBusyCountdownAfterAnEndInTheSameSecondIsStrictlyNewer(t *testing.T) {
	sender := &limitWaitCapture{}
	ag := NewAgent(coddyConfig("https://remote.example", nil), &session.State{ID: "sess_tie"}, sender, nil)
	ag.limitWaitHeartbeat = time.Hour
	wait := llm.BusyWaitStatus{Budget: 30 * time.Second, Remaining: 30 * time.Second, RetryIn: time.Second}

	// Two calls of one session back to back: the second one's countdown comes
	// at once after the first one's end.
	first := ag.newBusyWaitNotice("sess_tie", coddyTransport())
	first.report(wait)
	first.clear()
	second := ag.newBusyWaitNotice("sess_tie", coddyTransport())
	second.report(wait)
	second.clear()

	all := sender.snapshot()
	if len(all) != 4 {
		t.Fatalf("updates = %+v, want two countdowns and two ends", all)
	}
	var prev time.Time
	for i, u := range all {
		observed, err1 := time.Parse(time.RFC3339, u.ObservedAt)
		fetched, err2 := time.Parse(time.RFC3339, u.FetchedAt)
		if err1 != nil || err2 != nil || !observed.Equal(fetched) {
			t.Fatalf("update %d stamps observed %q (%v) and fetched %q (%v): want one RFC 3339 stamp in both", i, u.ObservedAt, err1, u.FetchedAt, err2)
		}
		if i > 0 && !fetched.After(prev) {
			t.Fatalf("update %d (%+v) is stamped %s, not strictly after the update before it (%s)", i, u, u.FetchedAt, prev.Format(time.RFC3339))
		}
		prev = fetched
	}
	if !all[2].Resuming || !isBusyEnd(all[1]) {
		t.Fatalf("updates = %+v, want end then countdown at positions 1 and 2", all)
	}
	// RetryAt is the real time the wait ends at, not a generator stamp.
	retry, err := time.Parse(time.RFC3339, all[0].RetryAt)
	if err != nil || retry.After(time.Now().Add(31*time.Second)) || retry.Before(time.Now().Add(28*time.Second)) {
		t.Fatalf("retryAt %q (%v), want about 30 s from now on the real clock", all[0].RetryAt, err)
	}
}

// Concurrent notices of one process share the generator: no two stamps are
// equal however the calls interleave.
func TestBusyStampsAreStrictlyIncreasingUnderConcurrency(t *testing.T) {
	var g stampGenerator
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	const workers, each = 8, 50
	stamps := make(chan string, workers*each)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				stamps <- g.next(now)
			}
		}()
	}
	wg.Wait()
	close(stamps)
	seen := make(map[string]struct{}, workers*each)
	for s := range stamps {
		if _, dup := seen[s]; dup {
			t.Fatalf("stamp %s was handed out twice", s)
		}
		seen[s] = struct{}{}
	}
	if len(seen) != workers*each {
		t.Fatalf("%d distinct stamps, want %d", len(seen), workers*each)
	}
	// The process-wide entry point is the same generator behind a function.
	a, b := nextBusyStamp(now), nextBusyStamp(now)
	if a >= b {
		t.Fatalf("nextBusyStamp gave %s then %s, want strictly increasing", a, b)
	}
}
