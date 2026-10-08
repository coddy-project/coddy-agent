//go:build http

package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

type probeState struct {
	t  *testing.T
	fx *sharedFixture
	pc *probedCall
}

func (s *probeState) remote(asked bool) error {
	s.fx = newSharedFixture(s.t)
	s.fx.srv.sharedProbeG = 300 * time.Millisecond
	s.fx.srv.sharedProbeI = 100 * time.Millisecond
	if asked {
		s.pc = s.fx.openProbed(s.t)
		return nil
	}
	started := s.fx.holdCalls()
	resp := s.fx.completeWith(sharedTestSharedTok, false)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the call: %d", resp.StatusCode)
	}
	s.pc = &probedCall{resp: resp, rd: newSSEReader(s.t, resp)}
	<-started
	return nil
}

func (s *probeState) asked() error    { return s.remote(true) }
func (s *probeState) notAsked() error { return s.remote(false) }

func (s *probeState) pingEvery(ms, seconds int) error {
	end := time.Now().Add(time.Duration(seconds) * time.Second)
	for time.Now().Before(end) {
		resp := s.fx.ping(s.pc.id, sharedTestSharedTok)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			return fmt.Errorf("a ping answered %d", resp.StatusCode)
		}
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
	return nil
}

func (s *probeState) wait(seconds int) error {
	time.Sleep(time.Duration(seconds) * time.Second)
	return nil
}

func (s *probeState) silent() error { return nil }

func (s *probeState) holdsSlot() error {
	if n := s.fx.srv.sharedLimit.inUse(sharedBearerKey(sharedTestSharedTok)); n != 1 {
		return fmt.Errorf("the call holds %d slots, want 1", n)
	}
	return nil
}

func (s *probeState) cutWithin(seconds int) error {
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	for time.Now().Before(deadline) {
		if s.fx.srv.sharedLimit.inUse(sharedBearerKey(sharedTestSharedTok)) == 0 {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("the call still holds its slot after %d s of silence", seconds)
}

func (s *probeState) terminal(code string) error {
	for {
		ev, ok := s.pc.rd.next(5 * time.Second)
		if !ok {
			return fmt.Errorf("the stream ended without the terminal error %q", code)
		}
		if ev.typ == "error" && strings.Contains(string(ev.raw), code) {
			return nil
		}
	}
}

func TestSharedModelsProbeFeature(t *testing.T) {
	st := &probeState{t: t}
	suite := godog.TestSuite{
		Name: "shared-models-probe",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^a remote coddy with the probe grace of 300 ms and a call that asked for the probe$`, st.asked)
			sc.Step(`^a remote coddy with the probe grace of 300 ms and a call that did not ask for the probe$`, st.notAsked)
			sc.Step(`^the client pings every (\d+) ms for (\d+) seconds?$`, st.pingEvery)
			sc.Step(`^the client never pings, for (\d+) seconds?$`, st.wait)
			sc.Step(`^the client goes silent$`, st.silent)
			sc.Step(`^the call still holds its slot$`, st.holdsSlot)
			sc.Step(`^the call is cut and its slot is free within (\d+) seconds?$`, st.cutWithin)
			sc.Step(`^the stream ends with the terminal error "([^"]*)"$`, st.terminal)
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) { return ctx, nil })
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/shared_models_probe.feature"}, Strict: true, TestingT: t},
	}
	if suite.Run() != 0 {
		t.Fatal("shared-models-probe failed")
	}
}
