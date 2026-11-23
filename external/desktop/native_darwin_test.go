//go:build desktop && darwin

package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/desktop/shell"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// testing runs tests on goroutines. Re-exec into TestMain to reserve the initial
// Cocoa thread for Run, exactly as the desktop command does in the real binary.
func TestMain(m *testing.M) {
	if os.Getenv("CODDY_NATIVE_WINDOW_HELPER") == "1" {
		if err := nativeWindowIntegration(); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestNativeWindowIntegration(t *testing.T) {
	for _, mode := range []string{"close", "terminate"} {
		t.Run(mode, func(t *testing.T) { testNativeWindowIntegration(t, mode) })
	}
}

func testNativeWindowIntegration(t *testing.T, mode string) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := nativeTestCommand(ctx, os.Args[0], "-test.run=^TestNativeWindowIntegration$")
	cmd.Env = nil
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "USER=") && !strings.HasPrefix(entry, "CODDY_HOME=") && !strings.HasPrefix(entry, "HOME=") && !strings.HasPrefix(entry, "CFFIXED_USER_HOME=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	// Foundation ignores HOME alone on macOS; CFFIXED_USER_HOME scopes the
	// system-managed WebKit data as well. The helper exits before TempDir cleanup.
	helperHome := t.TempDir()
	probeSource := filepath.Join(helperHome, "window-probe.m")
	probe := filepath.Join(helperHome, "window-probe")
	if err := os.WriteFile(probeSource, []byte(nativeWindowProbe), 0600); err != nil {
		t.Fatal(err)
	}
	compile := nativeTestCommand(ctx, "/usr/bin/clang", "-fobjc-arc", "-framework", "Cocoa", "-framework", "CoreGraphics", probeSource, "-o", probe)
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile window probe: %v: %s", err, out)
	}
	// The first exec of a new binary waits for its Gatekeeper scan, which took
	// 5 s behind another scan on a busy host: pay it before any 5 s window.
	if err := nativeTestCommand(ctx, probe, "0").Run(); err != nil && !errors.As(err, new(*exec.ExitError)) {
		t.Fatalf("start window probe: %v", err)
	}
	downloads := filepath.Join(helperHome, "Downloads")
	if err := os.MkdirAll(downloads, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(downloads, "native-download.txt"), []byte("keep existing file"), 0600); err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(helperHome, "ready")
	cmd.Env = append(cmd.Env, "CODDY_NATIVE_WINDOW_READY="+ready, "CODDY_NATIVE_WINDOW_PROBE="+probe)
	cmd.Env = append(cmd.Env, "CODDY_NATIVE_WINDOW_HELPER=1", "CODDY_NATIVE_WINDOW_END="+mode, "CODDY_HOME="+filepath.Join(helperHome, "coddy"), "HOME="+helperHome, "CFFIXED_USER_HOME="+helperHome)
	platform.AdaptCommand(cmd)
	var output strings.Builder
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if mode == "terminate" {
		// The parent sends quit; waiting for a quit sender inside the target could
		// deadlock its normal exit while the sender awaits the termination reply.
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			if _, err := os.Stat(ready); err == nil {
				break
			}
			select {
			case err := <-done:
				// A failed drive ends the helper: report it now, not at the deadline.
				t.Fatalf("native helper exited before it was ready for termination: %v\n%s", err, output.String())
			case <-ctx.Done():
				_ = cmd.Process.Kill()
				<-done
				t.Fatalf("native helper never became ready for termination: %v\n%s", ctx.Err(), output.String())
			case <-ticker.C:
			}
		}
		quit := nativeTestCommand(ctx, probe, fmt.Sprint(cmd.Process.Pid), "terminate")
		if out, err := quit.CombinedOutput(); err != nil || string(out) != "1" {
			_ = cmd.Process.Kill()
			<-done
			t.Fatalf("request native termination: %v: %s\n%s", err, out, output.String())
		}
	}
	if err := <-done; err != nil {
		t.Fatalf("real WKWebView integration: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "native Run returned after window end and HTTP teardown") {
		t.Fatalf("helper exited without completing Go teardown: %s", output.String())
	}
	t.Log(output.String())
}

func nativeWindowIntegration() error {
	home := os.Getenv("CODDY_HOME")
	if home == "" {
		return errors.New("native helper requires its temporary CODDY_HOME")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	handle := NewWindowHandle()
	reports := make(chan nativePageReport, 64)
	// Set once WebKit fetches the anchor's file: by then that click is a
	// download of its own, which the next link can no longer cancel.
	var anchorAsked atomic.Bool
	// The driver asks for every page report: /report answers with the number
	// of the next ask, and the page then sends the report for it. No fresh
	// report waits on a page timer, which WebKit throttles while it considers
	// the page hidden: behind another window a 50 ms timer fired every 250 ms.
	var askMu sync.Mutex
	asked, nextAsk := 0, make(chan struct{})
	ask := func() int {
		askMu.Lock()
		defer askMu.Unlock()
		asked++
		close(nextAsk)
		nextAsk = make(chan struct{})
		return asked
	}
	driver := make(chan error, 1)
	serverStopped := make(chan struct{})
	go func() {
		err := driveNativeWindow(ctx, handle, ask, reports)
		driver <- err
		if err != nil {
			cancel()
		}
	}()
	err := Run(ctx, Options{
		Home: home, Window: WindowNative, Handle: handle,
		Serve: func(ctx context.Context, addr string) error {
			defer close(serverStopped)
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}
			mux := http.NewServeMux()
			shellHandler := shell.Handler(handle)
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("<!doctype html><title>Chat</title>"))
			})
			mux.HandleFunc("/desktop/", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/desktop/" {
					shellHandler.ServeHTTP(w, r)
					return
				}
				recorder := httptest.NewRecorder()
				shellHandler.ServeHTTP(recorder, r)
				for name, values := range recorder.Header() {
					w.Header()[name] = values
				}
				_, _ = w.Write([]byte(strings.Replace(recorder.Body.String(), "</body>", nativePageProbe+"</body>", 1)))
			})
			mux.HandleFunc("/download-anchor", func(w http.ResponseWriter, _ *http.Request) {
				anchorAsked.Store(true)
				w.Header().Set("Content-Type", "text/plain")
				_, _ = w.Write([]byte("anchor download"))
			})
			mux.HandleFunc("/download-anchor-asked", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, anchorAsked.Load())
			})
			mux.HandleFunc("/attachment", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				w.Header().Set("Content-Disposition", `attachment; filename="native-download.txt"`)
				_, _ = w.Write([]byte("attachment download"))
			})
			mux.HandleFunc("/report", func(w http.ResponseWriter, r *http.Request) {
				var report nativePageReport
				if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
					http.Error(w, err.Error(), 400)
					return
				}
				select {
				case reports <- report:
				case <-r.Context().Done():
					return
				case <-ctx.Done():
					return
				}
				for {
					askMu.Lock()
					n, next := asked, nextAsk
					askMu.Unlock()
					if n > report.Asked {
						_, _ = fmt.Fprint(w, n)
						return
					}
					select {
					case <-next:
					case <-r.Context().Done():
						return
					case <-ctx.Done():
						return
					}
				}
			})
			srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
			go func() { _ = srv.Serve(ln) }()
			<-ctx.Done()
			return srv.Close()
		},
	})
	if err != nil {
		return err
	}
	if err := <-driver; err != nil {
		return err
	}
	select {
	case <-serverStopped:
	default:
		return errors.New("Run returned before HTTP teardown")
	}
	if ctx.Err() != nil {
		return fmt.Errorf("closing the native window did not end Run: %w", ctx.Err())
	}
	fmt.Println("native Run returned after window end and HTTP teardown")
	return nil
}

// Reports come from the actual WKWebView, rather than a native state accessor.
type nativePageReport struct {
	Asked, Retries                                   int
	X, Y                                             int
	OuterWidth, OuterHeight, InnerWidth, InnerHeight int
	Visibility                                       string
	Notification                                     string
	NotificationClick                                string
	Frame                                            shell.WindowFrame
	Buttons                                          []nativeBarButton
	DragX                                            float64
	BarHeight                                        float64
	BarItems, TabLabels                              []nativePageRect
	TabCount, SelectedTab                            int
	MenuOpen                                         bool
	BarReady                                         bool
	GestureEvents                                    [][]any
	WindowActions                                    []map[string]any
}

func driveNativeWindow(ctx context.Context, handle *WindowHandle, ask func() int, reports <-chan nativePageReport) (err error) {
	// page asks for reports until one satisfies done (nil: the first). Each
	// answers an ask made after the call, so none predates it.
	page := func(label string, timeout time.Duration, done func(nativePageReport) bool) (nativePageReport, error) {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		var last nativePageReport
		for n := ask(); ; {
			select {
			case report := <-reports:
				if report.Asked < n {
					continue
				}
				if done == nil || done(report) {
					return report, nil
				}
				last = report
				time.Sleep(50 * time.Millisecond)
				n = ask()
			case <-timer.C:
				return last, fmt.Errorf("%s: no fresh WKWebView report matched within %s; last: %+v", label, timeout, last)
			case <-ctx.Done():
				return last, ctx.Err()
			}
		}
	}
	// A cold WebKit process on a shared CI runner can take longer to start.
	// Once loaded, each action must still produce a fresh report within 5 s.
	if _, err := page("loaded page", 60*time.Second, func(r nativePageReport) bool { return r.BarReady }); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		entries, err := os.ReadDir(filepath.Join(os.Getenv("HOME"), "Downloads"))
		if err != nil {
			return err
		}
		contents := make(map[string]bool)
		for _, entry := range entries {
			b, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), "Downloads", entry.Name()))
			if err == nil {
				contents[string(b)] = true
			}
		}
		if contents["keep existing file"] && contents["anchor download"] && contents["attachment download"] {
			fmt.Println("download attribute and attachment saved to temporary HOME/Downloads without overwriting")
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("downloads missing or existing file overwritten: %v", contents)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	// The page's permission request races a one-second timeout.
	report, err := page("notification probe", 5*time.Second, func(r nativePageReport) bool { return r.Notification != "pending" })
	if err != nil {
		return err
	}
	fmt.Printf("WKWebView Notification: %s; click=%s\n", report.Notification, report.NotificationClick)
	if !handle.WaitForWindow(time.Second) {
		return errors.New("native window is not controllable")
	}
	var windowNumber int64
	if err := nativeOnMain(ctx, func(w *nativeWindow) { windowNumber = objc.Send[int64](w.window, sel("windowNumber")) }); err != nil {
		return err
	}
	readFrame := func() (nativeObservedFrame, error) {
		cmd := nativeTestCommand(ctx, os.Getenv("CODDY_NATIVE_WINDOW_PROBE"), fmt.Sprint(os.Getpid()), fmt.Sprint(windowNumber))
		out, err := cmd.CombinedOutput()
		if err != nil {
			return nativeObservedFrame{}, fmt.Errorf("observe native window: %w: %s", err, out)
		}
		var frame nativeObservedFrame
		if err := json.Unmarshal(out, &frame); err != nil {
			return frame, err
		}
		return frame, nil
	}
	observe := func(label string, action func() error, matches func(nativeObservedFrame) bool) (nativeObservedFrame, error) {
		if action != nil {
			if err := action(); err != nil {
				return nativeObservedFrame{}, err
			}
		}
		deadline := time.Now().Add(5 * time.Second)
		var frame, previous nativeObservedFrame
		var stableSince time.Time
		for {
			var err error
			frame, err = readFrame()
			if err != nil {
				return frame, err
			}
			if matches(frame) {
				if frame != previous || stableSince.IsZero() {
					stableSince = time.Now()
				}
				if time.Since(stableSince) >= 200*time.Millisecond {
					break
				}
			} else {
				stableSince = time.Time{}
			}
			previous = frame
			if time.Now().After(deadline) {
				return frame, fmt.Errorf("%s was not observed; last window: %+v", label, frame)
			}
			select {
			case <-ctx.Done():
				return frame, ctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
		}
		report, err := page(label, 5*time.Second, nil)
		if err != nil {
			return frame, err
		}
		fmt.Printf("%s: window=%+v viewport=%dx%d frame=%+v\n", label, frame, report.InnerWidth, report.InnerHeight, report.Frame)
		return frame, nil
	}
	initial, err := observe("activate initial window", handle.Activate, func(f nativeObservedFrame) bool {
		return f.OnScreen
	})
	if err != nil {
		return fmt.Errorf("%w; native=%s", err, nativeWindowDiagnostics(ctx))
	}
	var focused bool
	if err := nativeOnMain(ctx, func(w *nativeWindow) {
		focused = boolOf(w.window, "isKeyWindow") && boolOf(nsApp(), "isActive")
	}); err != nil {
		return err
	}
	if !focused {
		return fmt.Errorf("focused gesture precondition failed; native=%s", nativeWindowDiagnostics(ctx))
	}
	checkLayout := func(label string) error {
		report, err = page(label, 5*time.Second, nil)
		if err != nil {
			return err
		}
		chrome, err := nativeObservedChrome(ctx)
		if err != nil {
			return err
		}
		var shift float64
		if err := nativeOnMain(ctx, func(w *nativeWindow) {
			shift = w.buttonShift
			for range 5 {
				w.refreshFrame()
			}
		}); err != nil {
			return err
		}
		after, err := nativeObservedChrome(ctx)
		if err != nil {
			return err
		}
		if len(chrome.Buttons) != len(after.Buttons) {
			return fmt.Errorf("%s: button count changed on refresh", label)
		}
		for i := range chrome.Buttons {
			if chrome.Buttons[i] != after.Buttons[i] {
				return fmt.Errorf("%s: button drift on refresh: before=%+v after=%+v", label, chrome.Buttons, after.Buttons)
			}
		}
		if chrome.Visible != 3 || report.Frame.Kind != shell.FrameSystem || len(chrome.Buttons) != 3 || len(report.BarItems) != 3 || len(report.Buttons) != 2 || report.BarHeight != 38 {
			return fmt.Errorf("%s: native=%+v page=%+v", label, chrome, report)
		}
		if chrome.CollectionBehavior != nsWindowCollectionBehaviorFullScreenNone {
			return fmt.Errorf("%s: frameless collectionBehavior=%d, want FullScreenNone", label, chrome.CollectionBehavior)
		}
		// Compare real AppKit frames in window coordinates with the actual SVGs
		// and label in the page, not their larger button/tab containers.
		centres := make([]float64, 0, 6)
		for _, button := range chrome.Buttons {
			centres = append(centres, button.Y+button.Height/2)
		}
		for _, item := range report.BarItems {
			centres = append(centres, item.Y+item.Height/2)
		}
		low, high := centres[0], centres[0]
		for _, centre := range centres {
			low, high = math.Min(low, centre), math.Max(high, centre)
		}
		menu := report.BarItems[0]
		gap := menu.X - chrome.ZoomRight
		originalInset := int(chrome.ZoomRight + chrome.LeftMargin - 2*shift)
		if shift < 0 || shift > 2 || report.Frame.LeftInset != originalInset {
			return fmt.Errorf("%s: shift=%.1f inset=%d original inset=%d", label, shift, report.Frame.LeftInset, originalInset)
		}
		if high-low > 1 || math.Abs(gap-(chrome.LeftMargin-2*shift)) > 2 || math.Abs(float64(report.Frame.LeftInset)-menu.X) > 1 {
			return fmt.Errorf("%s: centres=%v menu icon gap=%.1f red margin=%.1f inset=%d icon left=%.1f", label, centres, gap, chrome.LeftMargin, report.Frame.LeftInset, menu.X)
		}
		for _, button := range report.Buttons {
			if button.X <= chrome.ZoomRight || button.Right > float64(report.InnerWidth) || !button.Reachable || button.Label == "" {
				return fmt.Errorf("%s: unreachable control %+v", label, button)
			}
		}
		fmt.Printf("%s: centres=%v menu icon gap=%.1f red margin=%.1f inset=%d bar=%.0f\n", label, centres, gap, chrome.LeftMargin, report.Frame.LeftInset, report.BarHeight)
		return nil
	}
	checkLifecycle := func(label string, check func(string) error) error {
		var keyLost, keyRegained bool
		var other objc.ID
		if err := nativeOnMain(ctx, func(w *nativeWindow) {
			other = class("NSWindow").Send(sel("alloc")).Send(sel("initWithContentRect:styleMask:backing:defer:"), nsRect{Size: nsSize{Width: 200, Height: 200}}, nsWindowStyleMaskTitled, nsBackingStoreBuffered, false)
			other.Send(sel("setReleasedWhenClosed:"), false)
			other.Send(sel("makeKeyAndOrderFront:"), objc.ID(0))
			keyLost = boolOf(other, "isKeyWindow") && !boolOf(w.window, "isKeyWindow")
		}); err != nil {
			return err
		}
		defer func() {
			_ = nativeOnMain(ctx, func(w *nativeWindow) {
				other.Send(sel("close"))
				other.Send(sel("release"))
				nsApp().Send(sel("setAppearance:"), objc.ID(0))
			})
		}()
		if !keyLost {
			return fmt.Errorf("%s: second test window did not take key status", label)
		}
		if err := check(label + " key lost"); err != nil {
			return err
		}
		if err := nativeOnMain(ctx, func(w *nativeWindow) {
			other.Send(sel("orderOut:"), objc.ID(0))
			w.window.Send(sel("makeKeyAndOrderFront:"), objc.ID(0))
			keyRegained = boolOf(w.window, "isKeyWindow") && !boolOf(other, "isKeyWindow")
		}); err != nil {
			return err
		}
		if !keyRegained {
			return fmt.Errorf("%s: target window did not regain key status", label)
		}
		if err := check(label + " key regained"); err != nil {
			return err
		}
		if _, err := observe(label+" miniaturise", handle.Minimise, func(f nativeObservedFrame) bool { return !f.OnScreen }); err != nil {
			return err
		}
		if _, err := observe(label+" deminiaturise", handle.Activate, func(f nativeObservedFrame) bool { return f.OnScreen }); err != nil {
			return err
		}
		if err := check(label + " deminiaturised"); err != nil {
			return err
		}
		for _, name := range []string{"NSAppearanceNameAqua", "NSAppearanceNameDarkAqua", ""} {
			var applied bool
			if err := nativeOnMain(ctx, func(w *nativeWindow) {
				var appearance objc.ID
				if name != "" {
					appearance = class("NSAppearance").Send(sel("appearanceNamed:"), nsString(name))
				}
				nsApp().Send(sel("setAppearance:"), appearance)
				applied = (name == "" || appearance != 0) && nsApp().Send(sel("appearance")) == appearance
			}); err != nil {
				return err
			}
			if !applied {
				return fmt.Errorf("%s: application appearance %q was not applied", label, name)
			}
			if err := check(label + " appearance " + name); err != nil {
				return err
			}
		}
		return nil
	}
	checkWidths := func(label string) error {
		for _, width := range []int{1280, minWindowWidth} {
			if err := nativeOnMain(ctx, func(w *nativeWindow) { f := w.frame(); f.W = width; w.setFrame(f) }); err != nil {
				return err
			}
			if _, err := observe(fmt.Sprintf("%s width %d", label, width), nil, func(f nativeObservedFrame) bool { return f.Width == width }); err != nil {
				return err
			}
			if _, err := page("viewport resized", 5*time.Second, func(r nativePageReport) bool { return r.InnerWidth == width }); err != nil {
				return err
			}
			if err := checkLayout(fmt.Sprintf("%s %d", label, width)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := checkLifecycle("unified lifecycle", checkLayout); err != nil {
		return err
	}
	if err := checkWidths("unified layout"); err != nil {
		return err
	}
	// The gestures run on a window smaller than any runner's screen. A window
	// that fills the screen counts as zoomed, and a drag lets a zoomed window
	// loose at its earlier size, which is a different gesture from a move.
	gestureW, gestureH := min(initial.Width, 800), min(initial.Height, 500)
	if err := nativeOnMain(ctx, func(w *nativeWindow) {
		// The window keeps its place. WindowServer's origin includes the menu
		// bar, the desktop's coordinates start below it, so the observed X and
		// Y are not reused.
		f := w.frame()
		f.W, f.H = gestureW, gestureH
		w.setFrame(f)
	}); err != nil {
		return err
	}
	sized, err := observe("size for gestures", nil, func(f nativeObservedFrame) bool { return f.Width == gestureW && f.Height == gestureH })
	if err != nil {
		return err
	}
	initial = sized
	if _, err := page("gesture viewport", 5*time.Second, func(r nativePageReport) bool { return r.InnerWidth == gestureW }); err != nil {
		return err
	}
	if err := checkLayout("gesture layout"); err != nil {
		return err
	}
	if err := nativeOnMain(ctx, func(w *nativeWindow) {
		focused = boolOf(w.window, "isKeyWindow") && boolOf(nsApp(), "isActive") && objc.Send[uint64](w.window, sel("occlusionState"))&2 != 0
	}); err != nil {
		return err
	}
	if !focused {
		return fmt.Errorf("focused visible gesture precondition failed after lifecycle; page visibility=%s; native=%s", report.Visibility, nativeWindowDiagnostics(ctx))
	}
	fmt.Printf("before gestures: page visibility=%s native=%s\n", report.Visibility, nativeWindowDiagnostics(ctx))
	// Drive focused AppKit events through WebKit and the real shell, without
	// posting system-wide input or requiring Accessibility permission.
	for _, gesture := range []struct {
		label  string
		x, y   int
		target string
	}{
		{"empty-bar", int(report.DragX), 20, "drag"},
		{"bar-padding", int(report.DragX), 3, "coddy-desktop-tabs"},
	} {
		before := initial
		eventOffset, actionOffset := len(report.GestureEvents), len(report.WindowActions)
		dragErr := nativeContinuousDrag(ctx, gesture.x, gesture.y)
		if dragErr != nil {
			return dragErr
		}
		moved, err := observe("real "+gesture.label+" drag", nil, func(f nativeObservedFrame) bool {
			return f.X == before.X+160 && f.Y == before.Y+80
		})
		telemetry, telemetryErr := page("drag telemetry", time.Second, nil)
		if err != nil {
			return fmt.Errorf("%w; pointer events=%v; window actions=%v; native=%s", err, telemetry.GestureEvents[eventOffset:], telemetry.WindowActions[actionOffset:], nativeWindowDiagnostics(ctx))
		}
		if telemetryErr != nil {
			return telemetryErr
		}
		trustedDown, trustedMove, trustedUp := false, false, false
		var downX, downY, upX, upY float64
		for _, event := range telemetry.GestureEvents[eventOffset:] {
			if len(event) == 7 && event[0] == "pointerdown" && event[1] == gesture.target && event[6] == true {
				trustedDown = true
				downX, _ = event[2].(float64)
				downY, _ = event[3].(float64)
			}
			if len(event) == 7 && event[6] == true {
				trustedMove = trustedMove || event[0] == "pointermove"
				trustedUp = trustedUp || event[0] == "pointerup"
				if event[0] == "pointerup" {
					upX, _ = event[2].(float64)
					upY, _ = event[3].(float64)
				}
			}
		}
		begun, movedBy, ended := false, false, false
		for _, action := range telemetry.WindowActions[actionOffset:] {
			begun = begun || action["action"] == "move-begin"
			movedBy = movedBy || action["action"] == "move-by"
			ended = ended || action["action"] == "move-end"
		}
		if !trustedDown || !trustedMove || !trustedUp || !begun || !movedBy || !ended {
			return fmt.Errorf("%s did not traverse the shell: events=%v actions=%v", gesture.label, telemetry.GestureEvents, telemetry.WindowActions)
		}
		if math.Abs(upX-downX-160) > 1 || math.Abs(upY-downY-80) > 1 {
			return fmt.Errorf("page screen delta=%g,%g, actual pointer delta=160,80", upX-downX, upY-downY)
		}
		fmt.Printf("%s: final window delta=%d,%d page screen delta=%.0f,%.0f\n", gesture.label, moved.X-before.X, moved.Y-before.Y, upX-downX, upY-downY)
		initial = moved
		report = telemetry
	}
	// WKWebView masks outer dimensions, screen position and visibility on this
	// host. Fresh page telemetry still covers the inner viewport, while the
	// public window-server probe observes the window's actual bounds/visibility.
	framed, err := observe("decorate", handle.Decorate, func(f nativeObservedFrame) bool { return f.Height > initial.Height })
	if err != nil {
		return err
	}
	chrome, err := nativeObservedChrome(ctx)
	if err != nil {
		return err
	}
	if chrome.CollectionBehavior != chrome.OriginalCollectionBehavior {
		return fmt.Errorf("framed collectionBehavior=%d, want original=%d", chrome.CollectionBehavior, chrome.OriginalCollectionBehavior)
	}
	checkFramed := func(label string) error {
		// Wait for WebKit's report to yield to AppKit's pending layout.
		if _, err := page(label, 5*time.Second, nil); err != nil {
			return err
		}
		chrome, err := nativeObservedChrome(ctx)
		if err != nil {
			return err
		}
		if chrome.Visible != 3 || len(chrome.Buttons) != 3 || chrome.TitlebarHeight <= 0 {
			return fmt.Errorf("%s: invalid framed chrome %+v", label, chrome)
		}
		for _, button := range chrome.Buttons {
			centre := button.Y + button.Height/2
			if math.Abs(centre-chrome.TitlebarHeight/2) > 1 {
				return fmt.Errorf("%s: framed button centre=%.1f, standard titlebar centre=%.1f", label, centre, chrome.TitlebarHeight/2)
			}
		}
		fmt.Printf("%s: standard titlebar centre=%.1f buttons=%+v\n", label, chrome.TitlebarHeight/2, chrome.Buttons)
		return nil
	}
	if err := checkLifecycle("framed lifecycle", checkFramed); err != nil {
		return err
	}
	undecorated, err := observe("undecorate", handle.Undecorate, func(f nativeObservedFrame) bool { return f.Height < framed.Height })
	if err != nil {
		return err
	}
	initial = undecorated
	if err := checkWidths("roundtrip unified layout"); err != nil {
		return err
	}
	if err := nativeOnMain(ctx, func(w *nativeWindow) {
		// The window keeps its place; only the width checkWidths took is given
		// back. WindowServer's origin includes the menu bar, the desktop's
		// coordinates start below it, so the observed X and Y are not reused.
		f := w.frame()
		f.W = initial.Width
		w.setFrame(f)
	}); err != nil {
		return err
	}
	if _, err = observe("restore width", nil, func(f nativeObservedFrame) bool { return f.Width == initial.Width }); err != nil {
		return err
	}
	report, err = page("control viewport", 5*time.Second, func(r nativePageReport) bool { return r.InnerWidth == initial.Width && r.TabCount == 1 })
	if err != nil {
		return err
	}
	click := func(rect nativePageRect) error {
		return nativePointerGesture(ctx, int(rect.X+rect.Width/2), int(rect.Y+rect.Height/2), 0, 0, 1)
	}
	if err := click(report.BarItems[2]); err != nil {
		return err
	}
	report, err = page("native + creates second tab", 5*time.Second, func(r nativePageReport) bool { return r.TabCount == 2 && r.SelectedTab == 1 })
	if err != nil {
		return err
	}
	if err := click(report.TabLabels[0]); err != nil {
		return err
	}
	report, err = page("native tab press selects first tab", 5*time.Second, func(r nativePageReport) bool { return r.SelectedTab == 0 })
	if err != nil {
		return err
	}
	if err := click(report.BarItems[0]); err != nil {
		return err
	}
	if _, err := page("native menu press opens menu", 5*time.Second, func(r nativePageReport) bool { return r.MenuOpen }); err != nil {
		return err
	}
	if err := click(report.BarItems[0]); err != nil {
		return err
	}
	if _, err := page("native menu press closes menu", 5*time.Second, func(r nativePageReport) bool { return !r.MenuOpen }); err != nil {
		return err
	}
	if err := handle.BeginMove(initial.X+100, initial.Y+10); err != nil {
		return err
	}
	moved, err := observe("move", func() error { return handle.MoveBy(20, 10) }, func(f nativeObservedFrame) bool { return f.X > initial.X && f.Y > initial.Y })
	if err != nil {
		return err
	}
	if err := handle.BeginResize(); err != nil {
		return err
	}
	resized, err := observe("resize", func() error { return handle.ResizeBy("se", -80, -60) }, func(f nativeObservedFrame) bool { return f.Width < moved.Width && f.Height < moved.Height })
	if err != nil {
		return err
	}
	_, err = observe("maximise", handle.ToggleMaximise, func(f nativeObservedFrame) bool { return f.Width > resized.Width || f.Height > resized.Height })
	if err != nil {
		return err
	}
	if err := checkLayout("zoomed layout"); err != nil {
		return err
	}
	_, err = observe("restore", handle.ToggleMaximise, func(f nativeObservedFrame) bool { return f.Width == resized.Width && f.Height == resized.Height })
	if err != nil {
		return err
	}
	if err := checkLayout("restored layout"); err != nil {
		return err
	}
	for _, label := range []string{"double-click zoom", "double-click restore"} {
		report, err = page("double-click position", 5*time.Second, nil)
		if err != nil {
			return err
		}
		_, err = observe(label, func() error { return nativePointerGesture(ctx, int(report.DragX), 20, 0, 0, 2) }, func(f nativeObservedFrame) bool {
			if label == "double-click zoom" {
				return f.Width > resized.Width || f.Height > resized.Height
			}
			return f.Width == resized.Width && f.Height == resized.Height
		})
		if err != nil {
			return err
		}
	}
	for _, label := range []string{"green zoom", "green restore"} {
		_, err = observe(label, func() error {
			return nativeOnMain(ctx, func(w *nativeWindow) {
				w.window.Send(sel("standardWindowButton:"), 2).Send(sel("performClick:"), objc.ID(0))
			})
		}, func(f nativeObservedFrame) bool {
			if label == "green zoom" {
				return f.Width > resized.Width || f.Height > resized.Height
			}
			return f.Width == resized.Width && f.Height == resized.Height
		})
		if err != nil {
			return err
		}
	}
	_, err = observe("minimise", handle.Minimise, func(f nativeObservedFrame) bool { return !f.OnScreen })
	if err != nil {
		return err
	}
	_, err = observe("activate", handle.Activate, func(f nativeObservedFrame) bool { return f.OnScreen })
	if err != nil {
		return err
	}
	if os.Getenv("CODDY_NATIVE_WINDOW_END") == "close" {
		return handle.Close()
	}
	// Release the driver before the parent sends a system termination request.
	return os.WriteFile(os.Getenv("CODDY_NATIVE_WINDOW_READY"), []byte("ready"), 0600)
}

type nativeObservedFrame struct {
	X, Y, Width, Height int
	OnScreen            bool
}

func nativeTestCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "USER=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	platform.AdaptCommand(cmd)
	return cmd
}

// This separate test-only process observes the helper's actual window without
// giving the production backend a test state accessor.
const nativeWindowProbe = `#import <Cocoa/Cocoa.h>
#import <CoreGraphics/CoreGraphics.h>
int main(int argc, const char **argv) {
 @autoreleasepool {
  if (argc != 2 && argc != 3) return 2;
  int pid = atoi(argv[1]);
  if (argc == 3 && strcmp(argv[2], "terminate") == 0) {
   BOOL sent = [[NSRunningApplication runningApplicationWithProcessIdentifier:pid] terminate];
   printf("%d", sent);
   return sent ? 0 : 1;
  }
  NSArray *windows = CFBridgingRelease(CGWindowListCopyWindowInfo(kCGWindowListOptionAll, kCGNullWindowID));
  NSDictionary *largest = nil;
  double area = 0;
  for (NSDictionary *window in windows) {
   if ([window[(id)kCGWindowOwnerPID] intValue] != pid || [window[(id)kCGWindowLayer] intValue] != 0) continue;
   NSDictionary *bounds = window[(id)kCGWindowBounds];
   if ([bounds[@"Width"] intValue] < 100) continue;
   NSMutableDictionary *report = [bounds mutableCopy];
   report[@"OnScreen"] = window[(id)kCGWindowIsOnscreen] ?: @NO;
   report[@"WindowNumber"] = window[(id)kCGWindowNumber];
   if (argc == 3 && [report[@"WindowNumber"] intValue] != atoi(argv[2])) continue;
   double nextArea = [bounds[@"Width"] doubleValue] * [bounds[@"Height"] doubleValue];
   if (nextArea > area) { largest = report; area = nextArea; }
  }
  if (!largest) {
   if (argc == 3) { printf("{\"OnScreen\":false}"); return 0; }
   return 1;
  }
  NSData *json = [NSJSONSerialization dataWithJSONObject:largest options:0 error:nil];
  fwrite(json.bytes, 1, json.length, stdout);
  return 0;
 }
}`

func TestNativeStoreID(t *testing.T) {
	const valid = "cce93143-8f03-4809-b8b7-9e9773ae25ed"
	for _, tc := range []struct {
		name, content string
		mode          os.FileMode
		exists        bool
		preserved     bool
	}{
		{name: "missing"},
		{name: "valid", content: valid + "\n", mode: 0o640, exists: true, preserved: true},
		{name: "empty", mode: 0o600, exists: true},
		{name: "malformed", content: "not-a-uuid", mode: 0o600, exists: true},
		{name: "invalid hex", content: "zce93143-8f03-4809-b8b7-9e9773ae25ed", mode: 0o600, exists: true},
		{name: "extra separators", content: "--e93143-8f03-4809-b8b7-9e9773ae25ed", mode: 0o600, exists: true},
		// WebKit raises NSInvalidArgumentException for these two rather than
		// returning a store, which would end the process; they must never
		// reach +[WKWebsiteDataStore dataStoreForIdentifier:].
		{name: "nil uuid", content: "00000000-0000-0000-0000-000000000000", mode: 0o600, exists: true},
		{name: "deleted uuid", content: "00000000-0000-0000-0000-000000000001", mode: 0o600, exists: true},
		{name: "wrong version", content: "cce93143-8f03-1809-b8b7-9e9773ae25ed", mode: 0o600, exists: true},
		{name: "wrong variant", content: "cce93143-8f03-4809-c8b7-9e9773ae25ed", mode: 0o600, exists: true},
		{name: "unreadable", content: valid, mode: 0, exists: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "id")
			if tc.exists {
				if err := os.WriteFile(path, []byte(tc.content), tc.mode); err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "unreadable" {
				if _, err := os.ReadFile(path); err == nil {
					t.Skip("the current user can read mode-000 files")
				}
			}
			var warnings bytes.Buffer
			log := slog.New(slog.NewTextHandler(&warnings, nil))
			id, err := nativeStoreID(dir, log)
			if err != nil {
				t.Fatalf("recover the WebKit store identifier: %v", err)
			}
			if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(id) {
				t.Fatalf("not a UUID v4: %q", id)
			}
			if tc.preserved && id != valid {
				t.Fatalf("valid identifier changed to %q", id)
			}
			if tc.exists && !tc.preserved && id == tc.content {
				t.Fatal("unusable identifier was not replaced")
			}
			if tc.exists && !tc.preserved {
				if !strings.Contains(warnings.String(), "level=WARN") || !strings.Contains(warnings.String(), path) {
					t.Fatalf("recovery warning must name the identifier path: %q", warnings.String())
				}
			} else if warnings.Len() != 0 {
				t.Fatalf("unexpected warning: %s", warnings.String())
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != id+"\n" {
				t.Fatalf("persisted identifier = %q, error = %v", got, err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			wantMode := os.FileMode(0o600)
			if tc.preserved {
				wantMode = tc.mode
			}
			if info.Mode().Perm() != wantMode {
				t.Fatalf("identifier permissions = %o, want %o", info.Mode().Perm(), wantMode)
			}
			again, err := nativeStoreID(dir, log)
			if err != nil || again != id {
				t.Fatalf("identifier did not survive reread: %q, %v", again, err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 || entries[0].Name() != "id" {
				t.Fatalf("temporary files left behind: %v, %v", entries, err)
			}
		})
	}
}

// Appended only by the test server; all shell markup, scripts and routes are real.
const nativePageProbe = `<input type="file"><button id="notify" style="position:fixed;left:20px;top:60px;width:180px;height:50px" autofocus>Test notification</button><script>
let gestureEvents = [], windowActions = [];
for (const type of ['pointerdown','pointermove','pointerup']) document.addEventListener(type,e=>gestureEvents.push([type,e.target.id,e.screenX,e.screenY,e.clientX,e.clientY,e.isTrusted]),true);
const originalFetch=window.fetch; window.fetch=(url,init)=>{if(url==='/desktop/window' && init?.method==='POST') windowActions.push(JSON.parse(init.body));return originalFetch(url,init)};
let notification = 'pending', notificationClick = '';
async function probeNotification(event) {
 notificationClick = 'trusted='+event.isTrusted+'; activation='+navigator.userActivation.isActive;
 if (typeof Notification !== 'function') { notification = typeof Notification; return; }
 const permission = await Promise.race([Notification.requestPermission(),new Promise(resolve=>setTimeout(()=>resolve('timeout'),1000))]);
 try {
  const n = new Notification('Coddy native integration'); notification = 'permission='+permission+'; constructor=returned';
  n.onshow=()=>notification+='; event=show'; n.onerror=()=>notification+='; event=error';
 }
 catch (error) { notification = 'permission='+permission+'; constructor='+error.name+': '+error.message; }
}
document.getElementById('notify').onclick = probeNotification;
probeNotification({isTrusted:false});
// The shell opens its first chat in a frame right after it loads. A download
// started while that frame is still loading can be cancelled by it on a slow
// runner, so the links are followed once the first frame has finished.
// Both links navigate this document, and a second navigation cancels the
// first while WebKit is still deciding it: on a busy runner the anchor's
// download was lost. The attachment link is followed once the server has
// been asked for the anchor's file.
function startDownloads() {
 const link=(path,download)=>{const a=document.createElement('a'); a.href=path; if(download) a.download=download; document.body.appendChild(a); return a};
 const anchor=link('/download-anchor','native-download.txt'), attachment=link('/attachment');
 setTimeout(()=>anchor.click(),500);
 (async function followAttachment() {
  for (;;) {
   await new Promise(resolve=>setTimeout(resolve,100));
   try { if (await (await fetch('/download-anchor-asked')).text()==='true') { attachment.click(); return; } } catch (error) {}
  }
 })();
}
(function waitForFirstFrame() {
 const frame=document.querySelector('iframe');
 if (frame && frame.contentDocument && frame.contentDocument.readyState==='complete') { startDownloads(); return; }
 setTimeout(waitForFirstFrame,100);
})();
// A link whose response becomes a download cancels the document's pending
// loads in WebKit (fetch rejects with "Load failed"): send the report again.
let asked = 0, retries = 0;
const rect=e=>{const r=e.getBoundingClientRect();return {x:r.x,y:r.y,width:r.width,height:r.height}};
async function report() {
 for (;;) {
  try {
   const answer = await fetch('/report', {method:'POST', body:JSON.stringify({asked,retries,x:screenX,y:screenY,
   outerWidth,outerHeight,innerWidth,innerHeight,frame:await (await fetch('/desktop/window')).json(),gestureEvents,windowActions,dragX:document.getElementById('drag').getBoundingClientRect().x+30,buttons:['menu-btn','new-tab'].map(id=>{const e=document.getElementById(id),r=e.getBoundingClientRect();return {x:r.x,right:r.right,label:e.getAttribute('aria-label'),reachable:e.contains(document.elementFromPoint(r.x+r.width/2,r.y+r.height/2))}}),barHeight:document.getElementById('coddy-desktop-tabs').getBoundingClientRect().height,barItems:['#menu-btn svg','.tab[aria-selected="true"] .label','#new-tab svg'].map(s=>document.querySelector(s)).filter(Boolean).map(rect),tabLabels:[...document.querySelectorAll('.tab .label')].map(rect),tabCount:document.querySelectorAll('.tab').length,selectedTab:[...document.querySelectorAll('.tab')].findIndex(e=>e.getAttribute('aria-selected')==='true'),menuOpen:!document.getElementById('menu').hidden,barReady:document.querySelector('.tab')!==null,visibility:document.visibilityState,notification,notificationClick})});
   asked = Number(await answer.text());
  } catch (error) { retries++; await new Promise(resolve => setTimeout(resolve, 50)); }
 }
}
report();
</script>`

func nativePointerGesture(ctx context.Context, x, y, dx, dy, clicks int) error {
	steps := []struct{ kind, dx, dy int }{{1, 0, 0}}
	if dx != 0 || dy != 0 {
		steps = append(steps, struct{ kind, dx, dy int }{6, dx, dy})
	}
	steps = append(steps, struct{ kind, dx, dy int }{2, dx, dy})
	// The press and the release leave in one dispatch: a pause between them
	// gave the shell room to rebuild its tab row, which detaches the node the
	// press landed on, and WebKit then drops the click instead of letting it
	// reach the tab's listener. With no gap there is nothing to detach from.
	done := make(chan struct{})
	enqueueOnMain(func() {
		defer close(done)
		w := activeWindow()
		frame := objc.Send[nsRect](w.window, sel("frame"))
		windowNumber := objc.Send[int64](w.window, sel("windowNumber"))
		for _, step := range steps {
			event := class("NSEvent").Send(sel("mouseEventWithType:location:modifierFlags:timestamp:windowNumber:context:eventNumber:clickCount:pressure:"),
				step.kind, nsPoint{X: float64(x + step.dx), Y: frame.Size.Height - float64(y+step.dy)}, uint64(0), objc.Send[float64](class("NSProcessInfo").Send(sel("processInfo")), sel("systemUptime")),
				windowNumber, objc.ID(0), int64(1), int64(clicks), float64(1))
			nsApp().Send(sel("sendEvent:"), event)
		}
	})
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	// Allow WebKit's input IPC to deliver the pair before the next step.
	select {
	case <-time.After(150 * time.Millisecond):
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// The pointer travels in screen points; each event is converted against the
// current frame, just as AppKit does after the preceding event moved a window.
func nativeContinuousDrag(ctx context.Context, x, y int) (err error) {
	var origin nsPoint
	var original nsPoint
	var warp func(nsPoint) int32
	// loadCocoa already keeps CoreGraphics loaded for the entire helper process.
	// Resolve its test-only warp without opening or unloading another library.
	purego.RegisterLibFunc(&warp, purego.RTLD_DEFAULT, "CGWarpMouseCursorPosition")
	var screenTop float64
	var previous windowRect
	var backwards bool
	var warpErr error
	if err := nativeOnMain(ctx, func(w *nativeWindow) {
		f := objc.Send[nsRect](w.window, sel("frame"))
		original = objc.Send[nsPoint](class("NSEvent"), sel("mouseLocation"))
		screens := class("NSScreen").Send(sel("screens"))
		primary := objc.Send[nsRect](screens.Send(sel("objectAtIndex:"), 0), sel("frame"))
		screenTop = primary.Origin.Y + primary.Size.Height
		origin = nsPoint{X: f.Origin.X + float64(x), Y: f.Origin.Y + f.Size.Height - float64(y)}
		previous = w.frame()
	}); err != nil {
		return err
	}
	defer func() {
		restored := make(chan error, 1)
		enqueueOnMain(func() {
			if code := warp(nsPoint{X: original.X, Y: screenTop - original.Y}); code != 0 {
				restored <- fmt.Errorf("restore pointer: %d", code)
			} else {
				restored <- nil
			}
		})
		select {
		case cleanupErr := <-restored:
			err = errors.Join(err, cleanupErr)
		case <-time.After(time.Second):
			err = errors.Join(err, errors.New("pointer cleanup did not finish on Cocoa main thread"))
		}
	}()
	start := previous
	for i := 0; i <= 41; i++ {
		step := min(i, 40)
		kind := 6
		if i == 0 {
			kind = 1
		}
		if i == 41 {
			kind = 2
		}
		if err := nativeOnMain(ctx, func(w *nativeWindow) {
			frame := objc.Send[nsRect](w.window, sel("frame"))
			screenPoint := nsPoint{X: origin.X + float64(step*4), Y: origin.Y - float64(step*2)}
			if code := warp(nsPoint{X: screenPoint.X, Y: screenTop - screenPoint.Y}); code != 0 {
				warpErr = fmt.Errorf("warp pointer: %d", code)
				return
			}
			point := nsPoint{X: screenPoint.X - frame.Origin.X, Y: screenPoint.Y - frame.Origin.Y}
			timestamp := objc.Send[float64](class("NSProcessInfo").Send(sel("processInfo")), sel("systemUptime"))
			event := class("NSEvent").Send(sel("mouseEventWithType:location:modifierFlags:timestamp:windowNumber:context:eventNumber:clickCount:pressure:"), kind, point, uint64(0), timestamp, objc.Send[int64](w.window, sel("windowNumber")), objc.ID(0), int64(i+1), int64(1), float64(1))

			nsApp().Send(sel("sendEvent:"), event)
			current := w.frame()
			backwards = backwards || current.X < previous.X || current.Y < previous.Y
			previous = current
		}); err != nil {
			return err
		}
		if warpErr != nil {
			return warpErr
		}
		select {
		case <-time.After(8 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	fmt.Printf("continuous drag: start=%+v final=%+v total=160,80 backwards=%t\n", start, previous, backwards)
	if backwards {
		return fmt.Errorf("continuous native drag moved backwards: start=%+v final=%+v", start, previous)
	}
	return nil
}

type nativeChrome struct {
	Visible                                        int
	ZoomRight, LeftMargin, TitlebarHeight          float64
	Buttons                                        []nativePageRect
	CollectionBehavior, OriginalCollectionBehavior uint64
}

func nativeObservedChrome(ctx context.Context) (nativeChrome, error) {
	result := make(chan nativeChrome, 1)
	enqueueOnMain(func() {
		w := activeWindow()
		chrome := nativeChrome{
			CollectionBehavior:         objc.Send[uint64](w.window, sel("collectionBehavior")),
			OriginalCollectionBehavior: w.originalCollectionBehavior,
		}
		frame := objc.Send[nsRect](w.window, sel("frame"))
		top := frame.Size.Height
		chrome.TitlebarHeight = top - objc.Send[nsRect](w.window, sel("contentRectForFrameRect:"), frame).Size.Height
		for _, kind := range []int{0, 1, 2} {
			button := w.window.Send(sel("standardWindowButton:"), kind)
			if button == 0 {
				continue
			}
			if !boolOf(button, "isHidden") {
				chrome.Visible++
			}
			frame := objc.Send[nsRect](button, sel("frame"))
			frame = objc.Send[nsRect](button.Send(sel("superview")), sel("convertRect:toView:"), frame, objc.ID(0))
			chrome.Buttons = append(chrome.Buttons, nativePageRect{X: frame.Origin.X, Y: top - frame.Origin.Y - frame.Size.Height, Width: frame.Size.Width, Height: frame.Size.Height})
			if kind == 0 {
				chrome.LeftMargin = frame.Origin.X
			}
			if kind == 2 {
				chrome.ZoomRight = frame.Origin.X + frame.Size.Width
			}
		}
		result <- chrome
	})
	select {
	case r := <-result:
		return r, nil
	case <-ctx.Done():
		return nativeChrome{}, ctx.Err()
	}
}

type nativePageRect struct {
	X, Y, Width, Height float64
}

type nativeBarButton struct {
	X, Right  float64
	Label     string
	Reachable bool
}

func nativeOnMain(ctx context.Context, fn func(*nativeWindow)) error {
	done := make(chan struct{})
	enqueueOnMain(func() { defer close(done); fn(activeWindow()) })
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func nativeWindowDiagnostics(ctx context.Context) string {
	var state map[string]any
	err := nativeOnMain(ctx, func(w *nativeWindow) {
		screen := w.window.Send(sel("screen"))
		state = map[string]any{"frame": objc.Send[nsRect](w.window, sel("frame")), "windowNumber": objc.Send[int64](w.window, sel("windowNumber")), "screenFrame": objc.Send[nsRect](screen, sel("frame")), "backingScaleFactor": objc.Send[float64](screen, sel("backingScaleFactor")), "appActive": boolOf(nsApp(), "isActive"), "firstResponder": goString(w.window.Send(sel("firstResponder")).Send(sel("className"))), "occlusionState": objc.Send[uint64](w.window, sel("occlusionState"))}
		for _, name := range []string{"isKeyWindow", "isMainWindow", "isVisible", "isMiniaturized", "isZoomed"} {
			state[name] = boolOf(w.window, name)
		}
	})
	if err != nil {
		return err.Error()
	}
	probe := nativeTestCommand(ctx, os.Getenv("CODDY_NATIVE_WINDOW_PROBE"), fmt.Sprint(os.Getpid()), fmt.Sprint(state["windowNumber"]))
	out, err := probe.CombinedOutput()
	state["exactWindowServer"] = string(out)
	if err != nil {
		state["probeError"] = err.Error()
	}
	b, _ := json.Marshal(state)
	return string(b)
}

func TestNativeButtonShift(t *testing.T) {
	for _, tc := range []struct{ left, right, want float64 }{
		{14, 70, 2}, {8, 70, 1}, {7, 70, 0}, {3, 70, 0},
		{9.5, 70.75, 1.25},
	} {
		if got := nativeButtonShift(tc.left, tc.right); got != tc.want {
			t.Errorf("shift(%g, %g) = %g, want %g", tc.left, tc.right, got, tc.want)
		}
	}
}
