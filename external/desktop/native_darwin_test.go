//go:build desktop && desktop.native && darwin && cgo

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
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
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
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
		readyErr := func() error {
			ticker := time.NewTicker(50 * time.Millisecond)
			defer ticker.Stop()
			for {
				if _, err := os.Stat(ready); err == nil {
					return nil
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-ticker.C:
				}
			}
		}()
		if readyErr != nil {
			_ = cmd.Process.Kill()
			<-done
			t.Fatalf("native helper never became ready for termination: %v\n%s", readyErr, output.String())
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
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	handle := NewWindowHandle()
	reports := make(chan nativePageReport, 64)
	driver := make(chan error, 1)
	serverStopped := make(chan struct{})
	go func() {
		err := driveNativeWindow(ctx, handle, reports)
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
			mux.HandleFunc("/desktop/", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				_, _ = w.Write([]byte(`<!doctype html><title>Coddy native integration</title><input type="file"><script>
async function report() {
 await fetch('/report', {method:'POST', body:JSON.stringify({at:Date.now(),x:screenX,y:screenY,
 outerWidth,outerHeight,innerWidth,innerHeight,visibility:document.visibilityState})});
 setTimeout(report, 50);
}
report(); document.addEventListener('visibilitychange', report);
</script>`))
			})
			mux.HandleFunc("/report", func(w http.ResponseWriter, r *http.Request) {
				var report nativePageReport
				if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
					http.Error(w, err.Error(), 400)
					return
				}
				select {
				case reports <- report:
				default:
				}
				w.WriteHeader(http.StatusNoContent)
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
	X, Y                                             int
	OuterWidth, OuterHeight, InnerWidth, InnerHeight int
	Visibility                                       string
	At                                               int64
}

func driveNativeWindow(ctx context.Context, handle *WindowHandle, reports <-chan nativePageReport) error {
	pageAfter := func(label string, after int64) (nativePageReport, error) {
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for {
			select {
			case report := <-reports:
				if report.At >= after {
					return report, nil
				}
			case <-timer.C:
				return nativePageReport{}, fmt.Errorf("%s: no fresh WKWebView report", label)
			case <-ctx.Done():
				return nativePageReport{}, ctx.Err()
			}
		}
	}
	if _, err := pageAfter("loaded page", 0); err != nil {
		return err
	}
	if !handle.WaitForWindow(time.Second) {
		return errors.New("native window is not controllable")
	}
	readFrame := func() (nativeObservedFrame, error) {
		cmd := nativeTestCommand(ctx, os.Getenv("CODDY_NATIVE_WINDOW_PROBE"), fmt.Sprint(os.Getpid()))
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
		page, err := pageAfter(label, time.Now().UnixMilli())
		if err != nil {
			return frame, err
		}
		fmt.Printf("%s: window=%+v page=%+v\n", label, frame, page)
		return frame, nil
	}
	initial, err := observe("activate initial window", handle.Activate, func(f nativeObservedFrame) bool { return f.OnScreen })
	if err != nil {
		return err
	}
	// WKWebView masks outer dimensions, screen position and visibility on this
	// host. Fresh page telemetry still covers the inner viewport, while the
	// public window-server probe observes the window's actual bounds/visibility.
	framed, err := observe("decorate", handle.Decorate, func(f nativeObservedFrame) bool { return f.Height > initial.Height })
	if err != nil {
		return err
	}
	undecorated, err := observe("undecorate", handle.Undecorate, func(f nativeObservedFrame) bool { return f.Height < framed.Height })
	if err != nil {
		return err
	}
	initial = undecorated
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
	_, err = observe("restore", handle.ToggleMaximise, func(f nativeObservedFrame) bool { return f.Width == resized.Width && f.Height == resized.Height })
	if err != nil {
		return err
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
   double nextArea = [bounds[@"Width"] doubleValue] * [bounds[@"Height"] doubleValue];
   if (nextArea > area) { largest = report; area = nextArea; }
  }
  if (!largest) return 1;
  NSData *json = [NSJSONSerialization dataWithJSONObject:largest options:0 error:nil];
  fwrite(json.bytes, 1, json.length, stdout);
  return 0;
 }
}`
