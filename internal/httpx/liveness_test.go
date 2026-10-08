package httpx

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// The two constants are the whole contract between the handler that streams
// heartbeats and the relay that bounds its own client: the bound is what a
// vanished peer costs at most, the heartbeat is the gap the bound includes.
func TestLivenessConstantsAreTheDecidedOnes(t *testing.T) {
	if LivenessBound != 45*time.Second {
		t.Fatalf("LivenessBound = %v, want 45s", LivenessBound)
	}
	if LivenessHeartbeat != 15*time.Second {
		t.Fatalf("LivenessHeartbeat = %v, want 15s", LivenessHeartbeat)
	}
	if LivenessHeartbeat > LivenessBound/3 {
		t.Fatalf("the heartbeat %v exceeds a third of the bound %v", LivenessHeartbeat, LivenessBound)
	}
	b, h := Liveness()
	if b != LivenessBound || h != LivenessHeartbeat {
		t.Fatalf("Liveness() = %v, %v, want the constants", b, h)
	}
}

// B = H + U: the user timeout the kernel gets is what is left of the bound
// after the heartbeat gap, 30 s of 45 s by default.
func TestCallUserTimeoutIsTheBoundLessTheHeartbeat(t *testing.T) {
	if got := CallUserTimeout(LivenessBound, LivenessHeartbeat); got != 30*time.Second {
		t.Fatalf("CallUserTimeout(B, H) = %v, want 30s", got)
	}
	tests := []struct {
		name      string
		bound, hb time.Duration
		want      time.Duration
	}{
		{name: "scaled", bound: 13 * time.Second, hb: 3 * time.Second, want: 10 * time.Second},
		{name: "a longer heartbeat is clamped to a third of the bound", bound: 9 * time.Second, hb: 9 * time.Second, want: 6 * time.Second},
		{name: "no heartbeat leaves the whole bound", bound: 12 * time.Second, hb: 0, want: 12 * time.Second},
		{name: "a negative heartbeat counts as none", bound: 12 * time.Second, hb: -time.Second, want: 12 * time.Second},
		{name: "no bound means no option", bound: 0, hb: time.Second, want: 0},
		{name: "a negative bound means no option", bound: -time.Second, hb: time.Second, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CallUserTimeout(tt.bound, tt.hb)
			if got != tt.want {
				t.Fatalf("CallUserTimeout(%v, %v) = %v, want %v", tt.bound, tt.hb, got, tt.want)
			}
			// The clamp keeps U at two thirds of B or more whenever a bound exists.
			if tt.bound > 0 && got < tt.bound*2/3 {
				t.Fatalf("U = %v is below two thirds of B = %v", got, tt.bound)
			}
		})
	}
}

// The seam scales the pair for a test and puts the constants back.
func TestScaleLivenessReplacesAndRestores(t *testing.T) {
	restore := ScaleLiveness(6*time.Second, 2*time.Second)
	b, h := Liveness()
	if b != 6*time.Second || h != 2*time.Second {
		t.Fatalf("scaled Liveness() = %v, %v", b, h)
	}
	restore()
	b, h = Liveness()
	if b != LivenessBound || h != LivenessHeartbeat {
		t.Fatalf("after restore Liveness() = %v, %v, want the constants", b, h)
	}
	// A second restore is harmless and does not undo a later scaling.
	later := ScaleLiveness(9*time.Second, 3*time.Second)
	restore()
	if b, _ := Liveness(); b != 9*time.Second {
		t.Fatalf("a stale restore undid a newer scaling: bound %v", b)
	}
	later()
}

// ProbeCall is UserTimeoutFor with U = B - H: set for the call, 0 after it.
func TestProbeCallSetsTheBoundLessTheHeartbeatAndRestores(t *testing.T) {
	var during, after time.Duration
	var errDuring error
	srv := tcpServer(t, func(w http.ResponseWriter, r *http.Request) {
		restore, err := ProbeCall(r, 13*time.Second, 3*time.Second)
		errDuring = err
		tc, _ := TCPConn(r)
		during, _ = platform.TCPUserTimeout(tc)
		restore()
		after, _ = platform.TCPUserTimeout(tc)
	})
	requireUserTimeoutSupport(t, srv)
	if _, err := srv.Client().Get(srv.URL); err != nil {
		t.Fatal(err)
	}
	if errDuring != nil {
		t.Fatalf("ProbeCall: %v", errDuring)
	}
	if during != 10*time.Second {
		t.Fatalf("during the call the option was %v, want 10s (13s - 3s)", during)
	}
	if after != 0 {
		t.Fatalf("after restore the option was %v, want 0", after)
	}
}

// A request that cannot be probed gets a restore it can still defer.
func TestProbeCallWithoutAConnectionIsANoop(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/coddy/llm/completions", nil)
	restore, err := ProbeCall(r, LivenessBound, LivenessHeartbeat)
	if !errors.Is(err, platform.ErrUserTimeoutUnsupported) {
		t.Fatalf("error = %v, want ErrUserTimeoutUnsupported", err)
	}
	if restore == nil {
		t.Fatal("restore is nil")
	}
	restore()
}
