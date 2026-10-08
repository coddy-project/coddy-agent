package llm

// The client side of the application probe (docs/plans/remote-model-provider-probe.md): the call asks for it, and when the remote
// confirms, a goroutine posts the ping every interval until the stream ends. A remote that does not confirm is never pinged.

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const probeTestID = "0123456789abcdef0123456789abcdef"

// probeRemote answers the completions with a held stream and the pings with a scripted status.
type probeRemote struct {
	*fakeRemote
	release  chan struct{}
	once     sync.Once
	pings    atomic.Int32
	pingAuth atomic.Value
	pingPath atomic.Value
	pingID   atomic.Value
	// hang makes a ping wait until its request is cancelled, as an intermediary that holds it does.
	hang      atomic.Bool
	pingTimes []time.Time
	timesMu   sync.Mutex
	status    atomic.Int32
	// bare404 makes the 404 of a ping a plain one, as a proxy that does not carry the route answers it.
	bare404 atomic.Bool
	asked   atomic.Bool
}

func newProbeRemote(t *testing.T, confirm string) *probeRemote {
	t.Helper()
	pr := &probeRemote{release: make(chan struct{})}
	pr.status.Store(http.StatusNoContent)
	t.Cleanup(pr.finish)
	pr.fakeRemote = newFakeRemote(t, func(w http.ResponseWriter, r *http.Request, _ int, _ WireRequest) {
		if strings.HasSuffix(r.URL.Path, "/alive") {
			pr.pings.Add(1)
			pr.timesMu.Lock()
			pr.pingTimes = append(pr.pingTimes, time.Now())
			pr.timesMu.Unlock()
			if pr.hang.Load() {
				<-r.Context().Done()
				return
			}
			pr.pingAuth.Store(r.Header.Get("Authorization"))
			pr.pingPath.Store(r.URL.Path)
			pr.pingID.Store(r.Header.Get(CoddyProbeIDHeader))
			if st := int(pr.status.Load()); st == http.StatusNotFound && !pr.bare404.Load() {
				answerWire(w, st, WireError{Kind: WireKindInvalid, Code: WireCodeUnknownCall})
			} else {
				w.WriteHeader(st)
			}
			return
		}
		pr.asked.Store(r.Header.Get(CoddyProbeHeader) == "1")
		if confirm != "" {
			w.Header().Set(CoddyProbeHeader, confirm)
		}
		fw := startStream(w)
		fw.heartbeat()
		select {
		case <-pr.release:
		case <-r.Context().Done():
			return
		}
		fw.text("hi")
		fw.final(&Response{Content: "hi", StopReason: "end_turn", InputTokens: 1, OutputTokens: 1})
	})
	return pr
}

// finish lets every held stream end; it is safe to call more than once.
func (pr *probeRemote) finish() { pr.once.Do(func() { close(pr.release) }) }

func withProbeFloor(t *testing.T, d time.Duration) {
	t.Helper()
	old := coddyProbeFloor
	coddyProbeFloor = d
	t.Cleanup(func() { coddyProbeFloor = old })
}

func runProbeCall(t *testing.T, pr *probeRemote, in ProviderInput) (done chan collected, cancel context.CancelFunc) {
	t.Helper()
	p := newTestCoddy(t, in)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done = make(chan collected, 1)
	go func() { done <- streamCtx(ctx, p, userMsg("hello"), nil) }()
	return done, cancel
}

func TestCoddyProbeIsAskedForOnEveryCompletion(t *testing.T) {
	pr := newProbeRemote(t, "")
	done, _ := runProbeCall(t, pr, coddyInput(pr.fakeRemote))
	waitFor(t, 5*time.Second, "the request", func() bool { return pr.count() >= 1 })
	pr.finish()
	<-done
	if !pr.asked.Load() {
		t.Fatal("the call did not send X-Coddy-Probe: 1")
	}
}

func TestCoddyProbePingsWhileTheStreamRunsAndStopsAtItsEnd(t *testing.T) {
	withProbeFloor(t, 0)
	pr := newProbeRemote(t, "id="+probeTestID+"; every_ms=20; grace_ms=100")
	done, _ := runProbeCall(t, pr, coddyInput(pr.fakeRemote))
	waitFor(t, 5*time.Second, "three pings", func() bool { return pr.pings.Load() >= 3 })
	if got := pr.pingAuth.Load(); got != "Bearer shared-token" {
		t.Errorf("the ping carries the call's credential: %v", got)
	}
	if got := pr.pingPath.Load(); got != CoddyAlivePath {
		t.Errorf("ping path %v", got)
	}
	if got := pr.pingID.Load(); got != probeTestID {
		t.Errorf("the id travels in %s, not in the path: %v", CoddyProbeIDHeader, got)
	}
	pr.finish()
	res := <-done
	if res.err != nil || res.resp == nil {
		t.Fatalf("the call: %v", res.err)
	}
	n := pr.pings.Load()
	time.Sleep(120 * time.Millisecond)
	if pr.pings.Load() != n {
		t.Errorf("the client kept pinging after the stream ended: %d then %d", n, pr.pings.Load())
	}
}

func TestCoddyProbeANodeThatDoesNotConfirmIsNeverPinged(t *testing.T) {
	withProbeFloor(t, 0)
	for _, confirm := range []string{"", "garbage", "id=NOTHEX; every_ms=20; grace_ms=100", "id=" + probeTestID + "; every_ms=0; grace_ms=100", "id=" + probeTestID + "; every_ms=abc; grace_ms=1"} {
		pr := newProbeRemote(t, confirm)
		done, _ := runProbeCall(t, pr, coddyInput(pr.fakeRemote))
		waitFor(t, 5*time.Second, "the request", func() bool { return pr.count() >= 1 })
		time.Sleep(80 * time.Millisecond)
		pr.finish()
		<-done
		if n := pr.pings.Load(); n != 0 {
			t.Errorf("confirmation %q: %d pings, want none", confirm, n)
		}
	}
}

func TestCoddyProbeAnUnknownCallStopsThePings(t *testing.T) {
	withProbeFloor(t, 0)
	pr := newProbeRemote(t, "id="+probeTestID+"; every_ms=20; grace_ms=100")
	pr.status.Store(http.StatusNotFound)
	done, _ := runProbeCall(t, pr, coddyInput(pr.fakeRemote))
	waitFor(t, 5*time.Second, "the first ping", func() bool { return pr.pings.Load() >= 1 })
	time.Sleep(150 * time.Millisecond)
	if n := pr.pings.Load(); n != 1 {
		t.Errorf("%d pings after a 404 unknown_call, want exactly the one that was answered", n)
	}
	pr.finish()
	if res := <-done; res.err != nil {
		t.Fatalf("a refused ping must not fail the call: %v", res.err)
	}
}

// A 404 that is not the node's unknown_call (a proxy that does not carry the route) is a failed ping, not the end of the call.
func TestCoddyProbeABare404IsAFailedPingNotTheEnd(t *testing.T) {
	withProbeFloor(t, 0)
	pr := newProbeRemote(t, "id="+probeTestID+"; every_ms=20; grace_ms=100")
	pr.status.Store(http.StatusNotFound)
	pr.bare404.Store(true)
	done, _ := runProbeCall(t, pr, coddyInput(pr.fakeRemote))
	waitFor(t, 5*time.Second, "pings through the bare 404s", func() bool { return pr.pings.Load() >= 3 })
	pr.finish()
	if res := <-done; res.err != nil || res.resp == nil {
		t.Fatalf("the call: %v", res.err)
	}
}

func TestCoddyProbeAFailingPingNeverEndsTheCall(t *testing.T) {
	withProbeFloor(t, 0)
	pr := newProbeRemote(t, "id="+probeTestID+"; every_ms=20; grace_ms=100")
	pr.status.Store(http.StatusInternalServerError)
	done, _ := runProbeCall(t, pr, coddyInput(pr.fakeRemote))
	waitFor(t, 5*time.Second, "pings through the errors", func() bool { return pr.pings.Load() >= 3 })
	pr.finish()
	if res := <-done; res.err != nil || res.resp == nil {
		t.Fatalf("a failing ping ended the call: %v", res.err)
	}
}

func TestCoddyProbeStopsWhenTheCallIsCancelled(t *testing.T) {
	withProbeFloor(t, 0)
	pr := newProbeRemote(t, "id="+probeTestID+"; every_ms=20; grace_ms=100")
	done, cancel := runProbeCall(t, pr, coddyInput(pr.fakeRemote))
	waitFor(t, 5*time.Second, "a ping", func() bool { return pr.pings.Load() >= 1 })
	cancel()
	<-done
	n := pr.pings.Load()
	time.Sleep(120 * time.Millisecond)
	if pr.pings.Load() != n {
		t.Errorf("pings after the cancellation: %d then %d", n, pr.pings.Load())
	}
}

func TestCoddyProbeKeepsTheMountPrefixOfARelay(t *testing.T) {
	withProbeFloor(t, 0)
	pr := newProbeRemote(t, "id="+probeTestID+"; every_ms=20; grace_ms=100")
	in := coddyInput(pr.fakeRemote)
	in.BaseURL += "/swarm/nodes/nas02/"
	done, _ := runProbeCall(t, pr, in)
	waitFor(t, 5*time.Second, "a ping", func() bool { return pr.pings.Load() >= 1 })
	if got := pr.pingPath.Load(); got != "/swarm/nodes/nas02"+CoddyAlivePath {
		t.Errorf("through a relay the ping goes to the mount: %v", got)
	}
	pr.finish()
	<-done
}

// The pings go out start to start, every interval, whatever happens to the one before: a hung ping is cut at DMAX = I / 2 and the next
// tick is on time, so a run of slow pings never stretches the gap the model's grace was sized for.
func TestCoddyProbePingsKeepTheirRateWhenSomeHang(t *testing.T) {
	withProbeFloor(t, 0)
	pr := newProbeRemote(t, "id="+probeTestID+"; every_ms=200; grace_ms=1000")
	pr.hang.Store(true)
	done, _ := runProbeCall(t, pr, coddyInput(pr.fakeRemote))
	waitFor(t, 10*time.Second, "six hung pings", func() bool { return pr.pings.Load() >= 6 })
	pr.finish()
	<-done
	pr.timesMu.Lock()
	times := append([]time.Time(nil), pr.pingTimes...)
	pr.timesMu.Unlock()
	var gaps []time.Duration
	for i := 1; i < len(times) && i < 7; i++ {
		gaps = append(gaps, times[i].Sub(times[i-1]))
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i] < gaps[j] })
	// Start to start the gap is the interval (200 ms); a loop that waited for the ping and then slept would make it 300 ms.
	if median := gaps[len(gaps)/2]; median > 260*time.Millisecond {
		t.Errorf("the median gap between pings is %v with every ping hung, want about the 200 ms interval: %v", median, gaps)
	}
}

// The goroutine ends with the stream, whatever ended it: a final frame, an error frame, a frame that cannot be decoded.
func TestCoddyProbeStopsWhenTheStreamBreaks(t *testing.T) {
	withProbeFloor(t, 0)
	var pings atomic.Int32
	f := newFakeRemote(t, func(w http.ResponseWriter, r *http.Request, _ int, _ WireRequest) {
		if strings.HasSuffix(r.URL.Path, "/alive") {
			pings.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set(CoddyProbeHeader, "id="+probeTestID+"; every_ms=20; grace_ms=100")
		fw := startStream(w)
		for pings.Load() < 2 {
			time.Sleep(5 * time.Millisecond)
		}
		fw.raw("data: {not json\n\n")
	})
	p := newTestCoddy(t, coddyInput(f))
	res := streamOnce(p, userMsg("hi"), nil)
	if res.err == nil {
		t.Fatal("a malformed frame must fail the call")
	}
	n := pings.Load()
	time.Sleep(120 * time.Millisecond)
	if pings.Load() != n {
		t.Errorf("the pinger outlived a broken stream: %d then %d", n, pings.Load())
	}
}

func TestCoddyProbeIntervalHasAFloor(t *testing.T) {
	if got := probeInterval(time.Millisecond); got != time.Second {
		t.Errorf("a remote asking for a ping every millisecond is held to the floor: %v", got)
	}
	if got := probeInterval(10 * time.Second); got != 10*time.Second {
		t.Errorf("%v", got)
	}
}

type recordingTransport struct {
	inner http.RoundTripper
	mu    sync.Mutex
	paths []string
}

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.paths = append(r.paths, req.URL.Path)
	r.mu.Unlock()
	return r.inner.RoundTrip(req)
}

func (r *recordingTransport) saw(suffix string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, p := range r.paths {
		if strings.HasSuffix(p, suffix) {
			n++
		}
	}
	return n
}

// The ping is a request of the row like any other: it goes through the row's own HTTP client, so providers[].proxy, the private CA, the
// client certificate and the HTTP/1.1-only transport apply to it (the provider transport rule of the repository).
func TestCoddyProbePingRidesTheRowsOwnHTTPClient(t *testing.T) {
	withProbeFloor(t, 0)
	pr := newProbeRemote(t, "id="+probeTestID+"; every_ms=20; grace_ms=100")
	rec := &recordingTransport{inner: pr.srv.Client().Transport}
	cp, err := newCoddyProvider(coddyInput(pr.fakeRemote), &http.Client{Transport: rec})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan collected, 1)
	go func() { done <- streamOnce(cp, userMsg("hello"), nil) }()
	waitFor(t, 5*time.Second, "pings", func() bool { return rec.saw("/alive") >= 2 })
	if rec.saw("/completions") != 1 {
		t.Errorf("the completion did not go through the same client: %v", rec.paths)
	}
	pr.finish()
	if res := <-done; res.err != nil {
		t.Fatal(res.err)
	}
}
