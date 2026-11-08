//go:build desktop

package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// The window runs one surface because it is handed one subsystem descriptor,
// and every other subsystem either duplicates a `coddy serve` of the same home
// or fights it for a port, a token or a cron tick. They are named, never
// turned off: the settings screen inside the window draws the live
// configuration, so a value this process cleared is the value a save from that
// screen would write to the operator's file.
func TestTheSurfacesLeftToServeAreNamedAndNothingIsEdited(t *testing.T) {
	cfg := &config.Config{}
	cfg.Swarm.Enabled = true
	cfg.Scheduler.Enabled = true
	cfg.Metrics.Enable = true
	cfg.Browser.Enable = true
	cfg.Gateways.Telegram.Enabled = true
	cfg.Gateways.Pachca.Enabled = true
	before := fmt.Sprintf("%+v", *cfg)

	left := SurfacesLeftToServe(cfg)

	if after := fmt.Sprintf("%+v", *cfg); after != before {
		t.Fatalf("the operator's configuration was edited:\n before %s\n after  %s", before, after)
	}
	joined := strings.Join(left, " ")
	for _, want := range []string{"swarm", "scheduler", "metrics", "browser", "telegram", "pachca"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the operator was not told that %s was left to `coddy serve`: %v", want, left)
		}
	}
}

// A configuration that enables nothing extra says nothing: a line naming
// surfaces the operator never asked for would be noise on every launch.
func TestNothingIsSaidWhenThereIsNothingToSay(t *testing.T) {
	if left := SurfacesLeftToServe(&config.Config{}); len(left) != 0 {
		t.Fatalf("a plain configuration reported %v", left)
	}
	if left := SurfacesLeftToServe(nil); left != nil {
		t.Fatalf("no configuration reported %v", left)
	}
}

func TestAWindowBackendThatDoesNotExistIsNamed(t *testing.T) {
	err := openWindow(context.Background(), Options{Window: "webview2"}, "http://127.0.0.1:1/desktop/")
	if err == nil {
		t.Fatal("an unknown --window value was accepted")
	}
	for _, want := range []string{"webview2", WindowAuto, WindowBrowser, WindowNative} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the message %q does not name %q", err, want)
		}
	}
}

// Asking for the native window in a build that has none is an error that says
// how to get one, not a run that opens nothing and waits.
func TestTheNativeWindowSaysWhichTagCarriesIt(t *testing.T) {
	if nativeAvailable {
		t.Skip("this build has the native window")
	}
	err := openWindow(context.Background(), Options{Window: WindowNative}, "http://127.0.0.1:1/desktop/")
	if err == nil || !strings.Contains(err.Error(), "desktop.native") {
		t.Fatalf("--window native answered %v, which does not name the tag that carries it", err)
	}
}

func TestRunRefusesAHomelessOrServerlessLaunch(t *testing.T) {
	if err := Run(context.Background(), Options{}); err == nil {
		t.Fatal("a run with no agent home was accepted")
	}
	err := Run(context.Background(), Options{Home: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "serve") {
		t.Fatalf("a run with nothing to serve answered %v", err)
	}
}

// A machine with no window this run can wait on is not a failure: Coddy
// serves, says where, and lasts until the context ends.
func TestWithNoWindowTheRunServesAndSaysWhere(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	out := &strings.Builder{}
	served := make(chan error, 1)
	go func() {
		served <- finishWindow(Options{Out: out}, ErrNoWaitableWindow, "http://127.0.0.1:4321/desktop/", nil, ctx)
	}()
	cancel()
	if err := <-served; err != nil {
		t.Fatalf("the run ended with %v", err)
	}
	if !strings.Contains(out.String(), "http://127.0.0.1:4321/desktop/") {
		t.Fatalf("the operator was not told where to look: %q", out)
	}
}

func TestAWindowThatFailedForAnyOtherReasonIsTheRunsError(t *testing.T) {
	boom := errors.New("the browser died")
	if err := finishWindow(Options{}, boom, "http://x/", nil, context.Background()); !errors.Is(err, boom) {
		t.Fatalf("finishWindow swallowed %v into %v", boom, err)
	}
}
