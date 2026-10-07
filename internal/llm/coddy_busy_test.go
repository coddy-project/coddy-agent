package llm

import (
	"context"
	"errors"
	"math"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"
)

// fakeBusyClock stands in for time: requests take no time, a sleep moves the
// clock by exactly what was asked, and every sleep is recorded.
type fakeBusyClock struct {
	mu     sync.Mutex
	t0     time.Time
	now    time.Time
	reads  int
	sleeps []time.Duration
	// block makes a sleep wait for its context instead of advancing the clock.
	block bool
}

func newFakeBusyClock() *fakeBusyClock {
	t0 := time.Unix(1_800_000_000, 0)
	return &fakeBusyClock{t0: t0, now: t0}
}

func (c *fakeBusyClock) clock() coddyClock {
	return coddyClock{
		now: func() time.Time {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.reads++
			return c.now
		},
		sleep: func(ctx context.Context, d time.Duration) error {
			c.mu.Lock()
			c.sleeps = append(c.sleeps, d)
			block := c.block
			c.mu.Unlock()
			if block {
				<-ctx.Done()
				return ctx.Err()
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			c.mu.Lock()
			c.now = c.now.Add(d)
			c.mu.Unlock()
			return nil
		},
	}
}

// advance moves the clock without a sleep: time a stream takes.
func (c *fakeBusyClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// clockReads is how many times the provider has read the clock.
func (c *fakeBusyClock) clockReads() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reads
}

func (c *fakeBusyClock) offset() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now.Sub(c.t0)
}

func (c *fakeBusyClock) recorded() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.sleeps...)
}

// busyUntil answers busy while the fake clock is before freeAt, recording the
// offset of each request, and a short answer after.
func busyUntil(c *fakeBusyClock, freeAt time.Duration, offsets *[]time.Duration, extra ...string) fakeRemoteHandler {
	var mu sync.Mutex
	return func(w http.ResponseWriter, r *http.Request, n int, req WireRequest) {
		mu.Lock()
		*offsets = append(*offsets, c.offset())
		mu.Unlock()
		if c.offset() < freeAt {
			answerWire(w, http.StatusTooManyRequests, WireError{Status: 429, Kind: WireKindBusy, RetryAfterS: 1}, append([]string{"Retry-After", "1"}, extra...)...)
			return
		}
		simpleAnswer("done after the wait")(w, r, n, req)
	}
}

// busyChain is the chain NewProvider builds for a coddy row - the outer scope
// of one call, the resilient wrapper, the provider - with a fake clock.
func busyChain(t *testing.T, remote *fakeRemote, fc *fakeBusyClock, budget time.Duration, jitter float64, retryMax int) Provider {
	t.Helper()
	in := coddyInput(remote)
	in.BusyWait = budget
	cp, err := newCoddyProvider(in, remote.srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	cp.clock = fc.clock()
	cp.jitter = func() float64 { return jitter }
	return newCoddyScope(wrapResilient(cp, ResilientOptions{RetryMax: retryMax, RetryBase: time.Millisecond}))
}

func secs(n ...int) []time.Duration {
	out := make([]time.Duration, 0, len(n))
	for _, v := range n {
		out = append(out, time.Duration(v)*time.Second)
	}
	return out
}

func TestCoddyBusyWaitFollowsTheModelledSchedule(t *testing.T) {
	fc := newFakeBusyClock()
	var offsets []time.Duration
	remote := newFakeRemote(t, busyUntil(fc, time.Hour, &offsets))
	p := busyChain(t, remote, fc, 30*time.Second, 0, 3)

	got := streamOnce(p, userMsg("Hi"), nil)

	// One wait is at most nine requests at 30 s: ticks 0, 1, 3, 7, 12, 17, 22, 27
	// and 30, the last sleep cut to what is left (model m2).
	if want := secs(0, 1, 3, 7, 12, 17, 22, 27, 30); !reflect.DeepEqual(offsets, want) {
		t.Fatalf("request offsets %v, want %v", offsets, want)
	}
	if want := secs(1, 2, 4, 5, 5, 5, 5, 3); !reflect.DeepEqual(fc.recorded(), want) {
		t.Fatalf("sleeps %v, want %v", fc.recorded(), want)
	}
	var busy *coddyBusyError
	if !errors.As(got.err, &busy) {
		t.Fatalf("err %T %v", got.err, got.err)
	}
	if busy.requests != 9 || busy.waited != 30*time.Second {
		t.Fatalf("busy error %+v", busy)
	}
	// Terminal on every layer: the wrapper does not restart it and the five
	// predicates say status zero, not transient.
	if remote.count() != 9 {
		t.Fatalf("the wrapper restarted the spent wait: %d requests", remote.count())
	}
	if httpStatusFromError(got.err) != 0 || isRetryableLLMError(got.err) || IsTransientProviderError(got.err) ||
		UpstreamStatus(got.err) != 0 || IsStreamTruncated(got.err) || IsStreamStalled(got.err) {
		t.Fatalf("the spent wait is read as retryable or as a status: %v", got.err)
	}
}

func TestCoddyBusyWaitTakesASlotThatFreesInsideTheBudget(t *testing.T) {
	for name, freeAt := range map[string]time.Duration{
		"after a few polls":    12 * time.Second,
		"just before deadline": 29 * time.Second,
		"at the deadline":      30 * time.Second,
	} {
		t.Run(name, func(t *testing.T) {
			fc := newFakeBusyClock()
			var offsets []time.Duration
			remote := newFakeRemote(t, busyUntil(fc, freeAt, &offsets))
			p := busyChain(t, remote, fc, 30*time.Second, 0, 3)

			allowance := NewRetryAllowance(3)
			ctx := WithRetryAllowance(context.Background(), allowance)
			got := streamCtx(ctx, p, userMsg("Hi"), nil)
			if got.err != nil || got.resp.Content != "done after the wait" {
				t.Fatalf("resp %+v err %v", got.resp, got.err)
			}
			// The wait spends none of agent.llm_retry_max and touches no retry
			// allowance: it is one attempt of the wrapper.
			snap := allowance.Snapshot()
			if snap.Remaining != 3 || snap.Attempts != 1 || snap.TransportRetries != 0 {
				t.Fatalf("the wait touched the retry allowance: %+v", snap)
			}
			if len(offsets) < 2 {
				t.Fatalf("the remote was asked %d times", len(offsets))
			}
			if last := offsets[len(offsets)-1]; last < freeAt || last > 30*time.Second {
				t.Fatalf("the slot that freed at %v was taken at %v", freeAt, last)
			}
		})
	}
}

func TestCoddyBusyWaitOfZeroFailsAtTheFirstBusyAfterOneRequest(t *testing.T) {
	fc := newFakeBusyClock()
	var offsets []time.Duration
	remote := newFakeRemote(t, busyUntil(fc, time.Hour, &offsets))
	p := busyChain(t, remote, fc, 0, 0, 3)
	got := streamOnce(p, userMsg("Hi"), nil)
	var busy *coddyBusyError
	if !errors.As(got.err, &busy) || remote.count() != 1 || len(fc.recorded()) != 0 {
		t.Fatalf("err %v, %d requests, sleeps %v", got.err, remote.count(), fc.recorded())
	}
	if busy.budget != 0 {
		t.Fatalf("budget %v", busy.budget)
	}
}

func TestCoddyBusyWaitSleepIsTheLargerOfRetryAfterAndTheBackoffWithJitterUpwardOnly(t *testing.T) {
	cases := []struct {
		name       string
		retryAfter string
		first      time.Duration // the base of the first sleep
	}{
		{"Retry-After above the backoff", "10", 10 * time.Second},
		{"Retry-After below the floor", "0", time.Second},
		{"no Retry-After", "", time.Second},
		{"Retry-After of one second", "1", time.Second},
	}
	for _, tc := range cases {
		for _, jitter := range []float64{0, 0.5, 0.999} {
			t.Run(tc.name, func(t *testing.T) {
				fc := newFakeBusyClock()
				remote := newFakeRemote(t, func(w http.ResponseWriter, r *http.Request, n int, req WireRequest) {
					if n == 1 {
						var extra []string
						if tc.retryAfter != "" {
							extra = []string{"Retry-After", tc.retryAfter}
						}
						answerWire(w, http.StatusTooManyRequests, WireError{Status: 429, Kind: WireKindBusy}, extra...)
						return
					}
					simpleAnswer("ok")(w, r, n, req)
				})
				p := busyChain(t, remote, fc, time.Minute, jitter, 3)
				if got := streamOnce(p, userMsg("Hi"), nil); got.err != nil {
					t.Fatal(got.err)
				}
				sl := fc.recorded()
				if len(sl) != 1 {
					t.Fatalf("sleeps %v", sl)
				}
				if sl[0] < tc.first {
					t.Fatalf("jitter pulled the sleep to %v, below the %v the remote asked for", sl[0], tc.first)
				}
				if max := tc.first + tc.first/5 + time.Millisecond; sl[0] > max {
					t.Fatalf("jitter above 20%%: %v for a base of %v", sl[0], tc.first)
				}
			})
		}
	}
}

func TestCoddyBusyWaitBackoffDoublesToACeilingOfFiveSeconds(t *testing.T) {
	fc := newFakeBusyClock()
	var offsets []time.Duration
	remote := newFakeRemote(t, busyUntil(fc, time.Hour, &offsets))
	p := busyChain(t, remote, fc, 20*time.Second, 0, 3)
	_ = streamOnce(p, userMsg("Hi"), nil)
	for i, d := range fc.recorded() {
		want := time.Second << min(i, 3)
		if want > 5*time.Second {
			want = 5 * time.Second
		}
		if left := 20*time.Second - sum(fc.recorded()[:i]); want > left {
			want = left
		}
		if d != want {
			t.Fatalf("sleep %d is %v, want %v (all: %v)", i, d, want, fc.recorded())
		}
	}
}

func sum(ds []time.Duration) time.Duration {
	var t time.Duration
	for _, d := range ds {
		t += d
	}
	return t
}

func TestCoddyBusyWaitCutsTheLastSleepWithJitterToo(t *testing.T) {
	fc := newFakeBusyClock()
	var offsets []time.Duration
	remote := newFakeRemote(t, busyUntil(fc, time.Hour, &offsets))
	p := busyChain(t, remote, fc, 9*time.Second, 0.999, 3)
	got := streamOnce(p, userMsg("Hi"), nil)
	var busy *coddyBusyError
	if !errors.As(got.err, &busy) {
		t.Fatalf("err %v", got.err)
	}
	if fc.offset() != 9*time.Second {
		t.Fatalf("the wait ran to %v: the deadline is monotonic over requests, sleeps and jitter", fc.offset())
	}
}

func TestCoddyStopCancelsASleepingWaitAtOnce(t *testing.T) {
	t.Run("fake clock", func(t *testing.T) {
		fc := newFakeBusyClock()
		fc.block = true
		var offsets []time.Duration
		remote := newFakeRemote(t, busyUntil(fc, time.Hour, &offsets))
		p := busyChain(t, remote, fc, 30*time.Second, 0, 3)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan collected, 1)
		go func() { done <- streamCtx(ctx, p, userMsg("Hi"), nil) }()
		waitFor(t, 5*time.Second, "the wait to start sleeping", func() bool { return len(fc.recorded()) == 1 })
		cancel()
		select {
		case got := <-done:
			if !errors.Is(got.err, context.Canceled) {
				t.Fatalf("err %v", got.err)
			}
			if remote.count() != 1 {
				t.Fatalf("%d requests: the cancelled wait went on", remote.count())
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Stop did not end the wait")
		}
	})
	t.Run("real clock", func(t *testing.T) {
		remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
			answerWire(w, http.StatusTooManyRequests, WireError{Status: 429, Kind: WireKindBusy}, "Retry-After", "30")
		})
		in := coddyInput(remote)
		in.BusyWait = time.Minute
		p := newTestCoddy(t, in)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan collected, 1)
		start := time.Now()
		go func() { done <- streamCtx(ctx, p, userMsg("Hi"), nil) }()
		waitFor(t, 5*time.Second, "the first busy answer", func() bool { return remote.count() == 1 })
		time.Sleep(50 * time.Millisecond)
		cancel()
		select {
		case got := <-done:
			if !errors.Is(got.err, context.Canceled) || time.Since(start) > 3*time.Second {
				t.Fatalf("err %v after %v", got.err, time.Since(start))
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Stop did not end the wait")
		}
	})
}

func TestCoddyCompleteWaitsToo(t *testing.T) {
	fc := newFakeBusyClock()
	var offsets []time.Duration
	remote := newFakeRemote(t, busyUntil(fc, 4*time.Second, &offsets))
	p := busyChain(t, remote, fc, 30*time.Second, 0, 3)
	resp, err := p.Complete(context.Background(), userMsg("Hi"), nil)
	if err != nil || resp.Content != "done after the wait" {
		t.Fatalf("resp %+v err %v", resp, err)
	}
	if len(offsets) < 3 {
		t.Fatalf("Complete did not wait: %v", offsets)
	}
}

// The deadline belongs to one Stream call of the outer wrapper: a transport
// failure after a successful wait must not start a new wait, or a call could
// wait (llm_retry_max + 1) times busy_wait_ms.
func TestCoddyOneWaitPerOuterCallAcrossTransportRetries(t *testing.T) {
	fc := newFakeBusyClock()
	var offsets []time.Duration
	var mu sync.Mutex
	remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, n int, _ WireRequest) {
		mu.Lock()
		offsets = append(offsets, fc.offset())
		mu.Unlock()
		switch {
		case n <= 2: // busy for the first two requests
			answerWire(w, http.StatusTooManyRequests, WireError{Status: 429, Kind: WireKindBusy}, "Retry-After", "1")
		case n == 3: // admitted, then the stream is cut before any output
			startStream(w).heartbeat()
		default: // the slot is gone again
			answerWire(w, http.StatusTooManyRequests, WireError{Status: 429, Kind: WireKindBusy}, "Retry-After", "1")
		}
	})
	p := busyChain(t, remote, fc, 10*time.Second, 0, 3)
	got := streamOnce(p, userMsg("Hi"), nil)
	var busy *coddyBusyError
	if !errors.As(got.err, &busy) {
		t.Fatalf("err %T %v", got.err, got.err)
	}
	if fc.offset() != 10*time.Second {
		t.Fatalf("the whole call waited %v in all: the deadline must not restart after the transport failure (10s)", fc.offset())
	}
}

func TestCoddyEachOuterCallHasItsOwnWait(t *testing.T) {
	fc := newFakeBusyClock()
	var offsets []time.Duration
	remote := newFakeRemote(t, busyUntil(fc, time.Hour, &offsets))
	p := busyChain(t, remote, fc, 5*time.Second, 0, 0)
	for i := 0; i < 2; i++ {
		before := fc.offset()
		got := streamOnce(p, userMsg("Hi"), nil)
		if CoddyErrorKind(got.err) != WireKindBusy || fc.offset()-before != 5*time.Second {
			t.Fatalf("call %d: err %v waited %v", i, got.err, fc.offset()-before)
		}
	}
}

func TestCoddyBusyWaitReportsItsCountdown(t *testing.T) {
	fc := newFakeBusyClock()
	var offsets []time.Duration
	remote := newFakeRemote(t, busyUntil(fc, time.Hour, &offsets))
	p := busyChain(t, remote, fc, 10*time.Second, 0, 3)

	var seen []BusyWaitStatus
	ctx := WithBusyWaitNotify(context.Background(), func(s BusyWaitStatus) { seen = append(seen, s) })
	_ = streamCtx(ctx, p, userMsg("Hi"), nil)

	if len(seen) == 0 {
		t.Fatal("the wait reported nothing")
	}
	first := seen[0]
	if first.Budget != 10*time.Second || first.Requests != 1 || first.Waited != 0 || first.RetryIn != time.Second || first.Remaining != 10*time.Second {
		t.Fatalf("first report %+v", first)
	}
	for i := 1; i < len(seen); i++ {
		if seen[i].Remaining >= seen[i-1].Remaining || seen[i].Requests != seen[i-1].Requests+1 {
			t.Fatalf("the countdown does not count down: %+v then %+v", seen[i-1], seen[i])
		}
	}
	if last := seen[len(seen)-1]; last.Remaining < last.RetryIn {
		t.Fatalf("the last sleep is cut to what is left: %+v", last)
	}
}

func TestCoddyBusyAfterTheStreamStartedIsNotWaitedFor(t *testing.T) {
	// The deadline covers admission only: a busy frame after the 200 is an
	// error like any other, with no second request.
	fc := newFakeBusyClock()
	remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
		fw := startStream(w)
		fw.text("x")
		fw.fail(WireError{Kind: WireKindBusy, Emitted: true})
	})
	p := busyChain(t, remote, fc, 30*time.Second, 0, 3)
	got := streamOnce(p, userMsg("Hi"), nil)
	if got.err == nil || remote.count() != 1 || len(fc.recorded()) != 0 {
		t.Fatalf("err %v, %d requests, sleeps %v", got.err, remote.count(), fc.recorded())
	}
}

func TestCoddyBusyWaitWithoutAScopeStillWaitsOncePerStreamCall(t *testing.T) {
	// A provider used directly (no outer scope in the context) gets a fresh
	// deadline per call rather than none.
	fc := newFakeBusyClock()
	var offsets []time.Duration
	remote := newFakeRemote(t, busyUntil(fc, 3*time.Second, &offsets))
	in := coddyInput(remote)
	in.BusyWait = 30 * time.Second
	cp, err := newCoddyProvider(in, remote.srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	cp.clock = fc.clock()
	cp.jitter = func() float64 { return 0 }
	resp, err := cp.Stream(context.Background(), userMsg("Hi"), nil, nil)
	if err != nil || resp.Content != "done after the wait" {
		t.Fatalf("resp %+v err %v", resp, err)
	}
}

// A host that drives time itself (a test, a simulation) replaces how a sleep of
// the wait is spent through the context: the provider still decides how long
// each sleep lasts and counts the budget on its own clock, so the schedule the
// remote is told about does not change, only how long it takes.
func TestCoddyBusyWaitSleepCanBeReplacedThroughTheContext(t *testing.T) {
	remote := newFakeRemote(t, func(w http.ResponseWriter, r *http.Request, n int, req WireRequest) {
		if n <= 2 {
			answerWire(w, http.StatusTooManyRequests, WireError{Status: 429, Kind: WireKindBusy, RetryAfterS: 1}, "Retry-After", "1")
			return
		}
		simpleAnswer("done after the wait")(w, r, n, req)
	})
	in := coddyInput(remote)
	in.BusyWait = 30 * time.Second
	cp, err := newCoddyProvider(in, remote.srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	cp.jitter = func() float64 { return 0 }

	var slept []time.Duration
	ctx := WithBusyWaitSleep(context.Background(), func(ctx context.Context, d time.Duration) error {
		slept = append(slept, d)
		return ctx.Err()
	})
	started := time.Now()
	resp, err := cp.Stream(ctx, userMsg("Hi"), nil, nil)
	if err != nil || resp.Content != "done after the wait" {
		t.Fatalf("resp %+v err %v", resp, err)
	}
	if want := secs(1, 2); !reflect.DeepEqual(slept, want) {
		t.Fatalf("sleeps %v, want %v", slept, want)
	}
	if took := time.Since(started); took > 2*time.Second {
		t.Fatalf("the replaced sleep still took %v", took)
	}

	// A replacement that fails ends the wait with its error: Stop works through it.
	remote2 := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
		answerWire(w, http.StatusTooManyRequests, WireError{Status: 429, Kind: WireKindBusy, RetryAfterS: 1}, "Retry-After", "1")
	})
	in = coddyInput(remote2)
	in.BusyWait = 30 * time.Second
	cp2, err := newCoddyProvider(in, remote2.srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	boom := errors.New("stopped by the host")
	ctx = WithBusyWaitSleep(context.Background(), func(context.Context, time.Duration) error { return boom })
	if _, err := cp2.Stream(ctx, userMsg("Hi"), nil, nil); !errors.Is(err, boom) || remote2.count() != 1 {
		t.Fatalf("err %v after %d requests", err, remote2.count())
	}
}

// A Retry-After the remote names is bounded before it becomes a Duration: a
// value near the top of int64 nanoseconds used to wrap the jittered sleep
// negative, and the wait then sent requests without a pause until its budget
// ran out.
func TestCoddyBusyWaitSleepStaysPositiveAndBoundedForAHostileRetryAfter(t *testing.T) {
	huge := []time.Duration{
		time.Duration(math.MaxInt64),
		9223372035 * time.Second,
		8_000_000_000 * time.Second,
		24 * time.Hour,
		1000 * time.Hour,
	}
	for _, ra := range huge {
		for _, jitter := range []float64{0, 0.5, 0.999} {
			for index := 0; index < 6; index++ {
				d := busySleep(index, ra, jitter)
				if d <= 0 {
					t.Fatalf("busySleep(%d, %v, %v) = %v: not a pause", index, ra, jitter, d)
				}
				if limit := time.Hour + time.Hour/5; d > limit {
					t.Fatalf("busySleep(%d, %v, %v) = %v: a Retry-After is honoured up to an hour (%v)", index, ra, jitter, d, limit)
				}
			}
		}
	}
}

func TestCoddyBusyWaitWithAHostileRetryAfterNeverSpinsOnTheRemote(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header string
		retry  float64
	}{
		{"header just under the int64 nanoseconds", "9223372035", 0},
		{"header of 8e9 seconds", "8e9", 0},
		{"header of 1e30 seconds", "1e30", 0},
		{"header Inf", "Inf", 0},
		{"header NaN", "NaN", 0},
		{"frame retry_after_s of 9223372035", "", 9223372035},
		{"frame retry_after_s of 1e30", "", 1e30},
	} {
		for _, jitter := range []float64{0, 0.5, 0.999} {
			t.Run(tc.name, func(t *testing.T) {
				fc := newFakeBusyClock()
				remote := newFakeRemote(t, func(w http.ResponseWriter, r *http.Request, n int, req WireRequest) {
					if n > 40 {
						// A loop that does not pause would end here; the test then
						// counts the requests it took.
						simpleAnswer("the loop ran away")(w, r, n, req)
						return
					}
					var extra []string
					if tc.header != "" {
						extra = []string{"Retry-After", tc.header}
					}
					answerWire(w, http.StatusTooManyRequests, WireError{Status: 429, Kind: WireKindBusy, RetryAfterS: tc.retry}, extra...)
				})
				p := busyChain(t, remote, fc, 5*time.Second, jitter, 3)

				got := streamOnce(p, userMsg("Hi"), nil)

				var busy *coddyBusyError
				if !errors.As(got.err, &busy) {
					t.Fatalf("err %T %v after %d requests", got.err, got.err, remote.count())
				}
				// The floor of a sleep is a second, so a 5 s wait is at most five requests.
				if n := remote.count(); n > 5 {
					t.Fatalf("%d requests in a 5 s wait: the sleeps did not pause (%v)", n, fc.recorded())
				}
				sleeps := fc.recorded()
				for i, d := range sleeps {
					if d <= 0 || d > 5*time.Second || (d < time.Second && i != len(sleeps)-1) {
						t.Fatalf("sleeps %v: each is a pause of a second or more, the last cut to what is left of the budget", sleeps)
					}
				}
				if fc.offset() != 5*time.Second {
					t.Fatalf("the wait ended at %v, want exactly the 5 s budget", fc.offset())
				}
			})
		}
	}
}

// The budget of the wait is admission only: the time a stream ran before it was
// cut is not charged to the wait of the retry that follows.
func TestCoddyBusyWaitDoesNotChargeTheTimeOfAStreamThatWasCut(t *testing.T) {
	for _, tc := range []struct {
		name   string
		budget time.Duration
	}{
		{"a budget shorter than the stream", time.Second},
		{"a budget longer than the stream", 5 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fc := newFakeBusyClock()
			remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, n int, _ WireRequest) {
				if n == 1 {
					// Admitted at once, then 1.5 s of a stream that ends with no
					// terminal event and no chunk: the wrapper sends the call again.
					// The 1.5 s start once the provider has read the clock for the
					// request and for its admission (two reads).
					startStream(w).heartbeat()
					waitFor(t, 5*time.Second, "the admission", func() bool { return fc.clockReads() >= 2 })
					fc.advance(1500 * time.Millisecond)
					return
				}
				answerWire(w, http.StatusTooManyRequests, WireError{Status: 429, Kind: WireKindBusy, RetryAfterS: 1}, "Retry-After", "1")
			})
			p := busyChain(t, remote, fc, tc.budget, 0, 3)

			got := streamOnce(p, userMsg("Hi"), nil)

			var busy *coddyBusyError
			if !errors.As(got.err, &busy) {
				t.Fatalf("err %T %v", got.err, got.err)
			}
			if busy.waited != tc.budget {
				t.Fatalf("the wait reports %v waited, want the budget %v: the stream is not waiting", busy.waited, tc.budget)
			}
			if total := sum(fc.recorded()); total != tc.budget {
				t.Fatalf("the retry slept %v (%v), want the whole budget %v", total, fc.recorded(), tc.budget)
			}
		})
	}
}

// Once the remote admits a call that waited, the notify callback hears of it
// once, so a surface can take the countdown down while the call streams.
func TestCoddyBusyWaitReportsTheAdmission(t *testing.T) {
	t.Run("a call that waited", func(t *testing.T) {
		fc := newFakeBusyClock()
		var offsets []time.Duration
		remote := newFakeRemote(t, busyUntil(fc, 4*time.Second, &offsets))
		p := busyChain(t, remote, fc, 30*time.Second, 0, 3)

		var seen []BusyWaitStatus
		ctx := WithBusyWaitNotify(context.Background(), func(s BusyWaitStatus) { seen = append(seen, s) })
		got := streamCtx(ctx, p, userMsg("Hi"), nil)
		if got.err != nil {
			t.Fatal(got.err)
		}
		if len(seen) < 2 {
			t.Fatalf("reports %+v", seen)
		}
		admitted := 0
		for i, s := range seen {
			if s.Admitted {
				admitted++
				if i != len(seen)-1 {
					t.Errorf("the admission is report %d of %d: it comes last", i+1, len(seen))
				}
			}
		}
		last := seen[len(seen)-1]
		if admitted != 1 || !last.Admitted {
			t.Fatalf("reports %+v: want exactly one, the last, to say Admitted", seen)
		}
		if last.Budget != 30*time.Second || last.Requests != remote.count() || last.Waited != fc.offset() || last.RetryIn != 0 {
			t.Fatalf("admission report %+v after %d requests and %v", last, remote.count(), fc.offset())
		}
	})
	t.Run("a call that did not wait says nothing", func(t *testing.T) {
		fc := newFakeBusyClock()
		remote := newFakeRemote(t, simpleAnswer("ok"))
		p := busyChain(t, remote, fc, 30*time.Second, 0, 3)
		var seen []BusyWaitStatus
		ctx := WithBusyWaitNotify(context.Background(), func(s BusyWaitStatus) { seen = append(seen, s) })
		if got := streamCtx(ctx, p, userMsg("Hi"), nil); got.err != nil {
			t.Fatal(got.err)
		}
		if len(seen) != 0 {
			t.Fatalf("reports %+v for a call admitted at once", seen)
		}
	})
	t.Run("a spent wait is not an admission", func(t *testing.T) {
		fc := newFakeBusyClock()
		var offsets []time.Duration
		remote := newFakeRemote(t, busyUntil(fc, time.Hour, &offsets))
		p := busyChain(t, remote, fc, 5*time.Second, 0, 3)
		var seen []BusyWaitStatus
		ctx := WithBusyWaitNotify(context.Background(), func(s BusyWaitStatus) { seen = append(seen, s) })
		_ = streamCtx(ctx, p, userMsg("Hi"), nil)
		for _, s := range seen {
			if s.Admitted {
				t.Fatalf("reports %+v: a call that was never admitted reported an admission", seen)
			}
		}
	})
}
