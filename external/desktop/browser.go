//go:build desktop

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// BrowserEnvVar names the browser to open the window with, for a machine whose
// browser is somewhere the search below does not look.
const BrowserEnvVar = "CODDY_DESKTOP_BROWSER"

// errNoBrowser is the one failure `auto` is allowed to fall through on.
var errNoBrowser = errors.New("desktop: no Chromium-family browser found to open a window with")

// BrowserCommand is the executable that opens the window, and whatever has to
// come before the window's own flags.
//
// A configured browser is only a path; the Args field exists for the tests,
// which put this test binary and its selector there, and it keeps them from
// having to compile a fake browser of their own.
type BrowserCommand struct {
	Path string
	Args []string
	// Name is the folder this browser's profile lives in. Empty takes it from
	// Path, which is what everything but a test does.
	Name string
}

// IsSet reports whether a caller named a browser at all.
func (b BrowserCommand) IsSet() bool { return strings.TrimSpace(b.Path) != "" }

// profileName is the folder under <home>/desktop/profiles this browser keeps
// its profile in: one per browser, not one per run, so that a second launch
// meets the first browser's singleton and hands it the window rather than
// opening a second application against the same chats.
func (b BrowserCommand) profileName() string {
	name := strings.TrimSpace(b.Name)
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(b.Path), filepath.Ext(b.Path))
	}
	cleaned := make([]rune, 0, len(name))
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			cleaned = append(cleaned, r)
		default:
			cleaned = append(cleaned, '-')
		}
	}
	out := strings.Trim(string(cleaned), "-")
	if out == "" {
		return "browser"
	}
	return out
}

// findBrowser answers the browser to open the window with: the one the caller
// named, else the one the environment names, else the first of this platform's
// candidates that is actually installed.
//
// Naming one by hand works on every platform, ported or not, which is why the
// environment is read before the list is consulted: a macOS operator who
// points CODDY_DESKTOP_BROWSER at the executable inside their Chrome bundle
// gets a window today.
func findBrowser(explicit BrowserCommand) (BrowserCommand, error) {
	if explicit.IsSet() {
		return explicit, nil
	}
	if named := strings.TrimSpace(os.Getenv(BrowserEnvVar)); named != "" {
		if path, err := resolveBrowserPath(named); err == nil {
			return BrowserCommand{Path: path}, nil
		}
		return BrowserCommand{}, fmt.Errorf("desktop: %s names %q, which is not an executable here", BrowserEnvVar, named)
	}
	candidates := browserCandidates()
	if len(candidates) == 0 {
		return BrowserCommand{}, fmt.Errorf("%w: the window is ported to Linux only for now, and this is %s. Name one with %s to open it anyway",
			errNoBrowser, runtime.GOOS, BrowserEnvVar)
	}
	for _, candidate := range candidates {
		if path, err := resolveBrowserPath(candidate); err == nil {
			return BrowserCommand{Path: path}, nil
		}
	}
	return BrowserCommand{}, errNoBrowser
}

// resolveBrowserPath accepts both a name on PATH and a path to a binary.
func resolveBrowserPath(candidate string) (string, error) {
	if strings.ContainsRune(candidate, filepath.Separator) || strings.ContainsRune(candidate, '/') {
		info, err := os.Stat(candidate)
		if err != nil {
			return "", err
		}
		if info.IsDir() {
			return "", fmt.Errorf("%s is a directory", candidate)
		}
		return candidate, nil
	}
	return exec.LookPath(candidate)
}

// browserArgs are the window's own flags.
//
// --user-data-dir is the load-bearing one. Without it, --app against a browser
// the operator already has open hands the window to that instance and the
// process we started exits immediately, so this run could not tell when the
// window closed; and the agent's session cookie would land in the profile they
// read mail in. With it, the window is a process of its own, and the cookie
// stays here. It costs disk, which the surface's page says out loud.
// firstWindowSize is the shape the first window of a profile opens at: a
// landscape rectangle, like every editor and browser on the desktop, rather
// than the tall default a Chromium application window takes. It is passed
// only when the profile is new, because from the second launch on the browser
// remembers where the operator left the window and this flag would override
// it on every start.
const firstWindowSize = "1280,820"

func browserArgs(url, profileDir string, fresh bool) []string {
	args := []string{
		"--app=" + url,
		"--user-data-dir=" + profileDir,
		// A profile of our own is a fresh profile, and a fresh profile asks
		// the questions a first run asks. Nobody is there to answer them:
		// the window is supposed to open on a chat.
		"--no-first-run",
		"--no-default-browser-check",
		// Whose window this is, for the task bar and the window manager. On a
		// platform with no WM_CLASS the browser ignores it.
		"--class=" + WindowClass,
	}
	if fresh {
		args = append(args, "--window-size="+firstWindowSize)
	}
	return args
}

// WindowClass is what the window calls itself to the desktop: the WM_CLASS of
// an X11 window, and the name a task bar matches against the application
// entries it knows.
//
// Without it the window is the browser's. A Chromium window opened with --app
// takes its class from the address it opens - measured on a display of its
// own, ("127.0.0.1__desktop", "Google-chrome") - so a task bar finds the
// browser's entry, draws the browser's icon under the window and opens the
// browser's menu over it: its recent downloads, a new incognito window, none
// of it Coddy's. The entry the Linux packages install names this same string
// in StartupWMClass (packaging/desktop/coddy-desktop.desktop), which is what
// the match is made on.
const WindowClass = "coddy-desktop"

// contentSettingAllow is Chromium's CONTENT_SETTING_ALLOW, the value a
// permission exception carries in the profile's Preferences.
const contentSettingAllow = 1

// windowOrigin is the origin of the address the window opens on, which is what
// a permission is granted to. An address that will not parse yields "", and a
// grant keyed on it matches nothing rather than everything.
func windowOrigin(addr string) string {
	parsed, err := url.Parse(addr)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

// seedProfile writes the preferences a fresh profile needs before its first
// window, and only then: an existing profile is the operator's, whatever they
// have changed in it since. Every Chromium-family browser reads the same file.
//
// There are two.
//
// The window's content is Coddy's own interface in the language the operator
// chose for it, and a chat is whatever language they type in, so the browser
// reads the page as foreign and offers to translate it - a bubble anchored over
// the tab strip, on every launch. The flag that used to turn this off is gone
// and `--disable-features=Translate,TranslateUI` does not stop the bubble in
// current Chrome (checked against 157), while this preference does.
//
// The web UI shows system notifications while no window of it has the person's
// attention (Settings - Appearance - Notifications), and the switch that turns
// them on asks the browser for the permission. In an application window that
// question arrives as Chromium's own bubble over the tab strip, with no address
// bar beside it to say where it came from, so the answer is here instead: this
// window's origin, and only it, may raise them. The origin holds across
// launches because the port does (port.go); should the remembered port be taken
// and the launcher pick another, the grant no longer matches and the operator
// answers the bubble once, in a window that still works.
func seedProfile(profileDir, origin string) (fresh bool, err error) {
	dir := filepath.Join(profileDir, "Default")
	prefs := filepath.Join(dir, "Preferences")
	if _, err := os.Stat(prefs); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, err
	}
	// Chromium keys a content setting by a pair of patterns, "<primary>,<secondary>",
	// the embedder's being "*" for a page that is not framed by another origin.
	seeded := map[string]any{
		"translate": map[string]any{"enabled": false},
		"profile": map[string]any{
			"content_settings": map[string]any{
				"exceptions": map[string]any{
					"notifications": map[string]any{
						origin + ",*": map[string]any{"setting": contentSettingAllow},
					},
				},
			},
		},
	}
	body, err := json.Marshal(seeded)
	if err != nil {
		return false, err
	}
	if err := os.WriteFile(prefs, body, 0o600); err != nil {
		return false, err
	}
	return true, nil
}

// openBrowserWindow starts the browser and returns when its window closes, or
// when ctx ends, in which case the window is closed with it.
func openBrowserWindow(ctx context.Context, opts Options, url string) error {
	browser, err := findBrowser(opts.Browser)
	if err != nil {
		return err
	}
	profileDir := filepath.Join(opts.Home, "desktop", "profiles", browser.profileName())
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return fmt.Errorf("desktop: the window's profile directory: %w", err)
	}
	fresh, err := seedProfile(profileDir, windowOrigin(url))
	if err != nil {
		// A profile that could not be seeded still opens a window; the
		// operator gets the translate bubble and one permission question,
		// and nothing worse.
		logger(opts.Log).Warn("the window's profile could not be prepared", "profile", profileDir, "error", err)
	}
	argv := append(append([]string{}, browser.Args...), browserArgs(url, profileDir, fresh)...)
	// The operator names the browser; the rest of the argv is this file's own flags.
	cmd := exec.Command(browser.Path, argv...)
	platform.AdaptCommand(cmd)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("desktop: starting %s: %w", browser.Path, err)
	}
	logger(opts.Log).Info("desktop window opened", "browser", browser.Path, "url", url, "profile", profileDir)

	// The window belongs to the window manager, and from here on the strip
	// inside it is what drives it: the buttons a frameless window has to carry
	// and the drag that replaces its title bar. Finding it is asynchronous
	// because the browser maps it a moment after it starts, and a window that
	// never appears only means the strip draws no buttons.
	if opts.Handle != nil {
		go func() {
			ctl := newWindowControl(opts.Log, cmd.Process.Pid)
			if !ctl.WaitForWindow(15 * time.Second) {
				logger(opts.Log).Debug("the window did not appear on the display, so the strip draws no window buttons")
				return
			}
			opts.Handle.Attach(ctl)
			// The mark the window carries, for a task bar with no installed
			// entry of Coddy's to take one from: the browser gives an
			// application window the page's favicon at 16 pixels and nothing
			// larger (window_icon_x11.go).
			if i, ok := ctl.(windowIconSetter); ok {
				i.setIcon()
			}
			if !opts.Framed {
				// The browser is still settling the window it has just mapped,
				// and it writes the same property the frame is decided by, so
				// the first moment is the worst one to ask.
				time.Sleep(600 * time.Millisecond)
				if err := ctl.Undecorate(); err != nil {
					logger(opts.Log).Warn("the system frame could not be taken off; the window keeps it", "error", err)
				}
			}
			// A bare rectangle with square corners looks like a bug beside
			// every other window on the desktop, so the corners are cut out of
			// the window itself - after the frame is off, because taking it
			// off remaps the window and a remap drops the shape - and laid
			// again whenever the window is resized or maximised. A window that
			// kept its frame is shaped too: roundCorners leaves such a window
			// whole, and the watcher is then what notices the frame going.
			if r, ok := ctl.(cornerRounder); ok {
				r.roundCorners()
				go r.watchShape(ctx.Done())
			}
			// Ctrl+T and Ctrl+W are taken from the browser here rather than
			// read in the page, which never sees them (chords.go).
			if g, ok := ctl.(chordGrabber); ok {
				g.grabChords(opts.Handle.pushGrabbed)
			}
			if opts.StartMinimised {
				if err := ctl.Minimise(); err != nil {
					logger(opts.Log).Debug("the window could not be opened minimised", "error", err)
				}
			}
		}()
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		// The window closing is the ordinary end of a desktop run, so this is
		// never an error of the run - but it is the one event that explains an
		// application that "just disappeared", and a browser that died of its
		// own accord says so in its exit status. It is logged at info for
		// that reason: the next report of a window that vanished is then a
		// line in the log rather than a guess.
		logger(opts.Log).Info("the window closed, so the desktop is ending", "status", exitStatus(err))
		return nil
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		if err := <-done; err != nil {
			logger(opts.Log).Debug("the window's browser ended with the run", "error", err)
		}
		return ctx.Err()
	}
}

// exitStatus describes how the browser ended, for the one log line that
// explains a window nobody closed.
func exitStatus(err error) string {
	if err == nil {
		return "closed"
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return fmt.Sprintf("exit %d", exit.ExitCode())
	}
	return err.Error()
}
