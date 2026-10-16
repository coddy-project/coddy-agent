//go:build cli

package cli

// Godog harness for features/cli_image_paste.feature: the console harness
// over the stub runner, with the clipboard and the markup window replaced by
// scripted stand-ins (the real ones are tested against Xvfb in
// external/cli/clipimage and external/cli/markup/xwindow).

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/external/cli/clipimage"
)

type cliImageState struct {
	*cliTUIState
	mu     sync.Mutex
	copied []byte
	marked []byte
	shown  image.Point
	io     imageIO
}

func pngOf(w, h int, c color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func (s *cliImageState) modelsTakePictures() error {
	for i := range s.cfg.Models {
		s.cfg.Models[i].Multimodal = true
	}
	return nil
}

func (s *cliImageState) clipboardHolds(w, h int) error {
	s.copied = pngOf(w, h, color.RGBA{0x30, 0x60, 0x90, 0xff})
	s.io.read = func(context.Context) (clipimage.Image, error) {
		pic, err := png.Decode(bytes.NewReader(s.copied))
		if err != nil {
			return clipimage.Image{}, err
		}
		return clipimage.Image{Data: s.copied, MIME: "image/png", Picture: pic}, nil
	}
	return s.install()
}

func (s *cliImageState) markupClicksDone() error {
	s.marked = pngOf(40, 30, color.RGBA{0xff, 0x3b, 0x30, 0xff})
	s.io.edit = func(_ context.Context, img image.Image) ([]byte, bool, error) {
		s.mu.Lock()
		s.shown = img.Bounds().Size()
		s.mu.Unlock()
		return s.marked, true, nil
	}
	return s.install()
}

func (s *cliImageState) markupCancelled() error {
	s.io.edit = func(context.Context, image.Image) ([]byte, bool, error) { return nil, false, nil }
	return s.install()
}

func (s *cliImageState) noDisplay() error {
	s.io.edit = nil
	return s.install()
}

// install hands the stand-ins to the app the background built.
func (s *cliImageState) install() error {
	if s.app == nil {
		return fmt.Errorf("no console app to install the clipboard into")
	}
	s.app.imgIO = s.io
	return nil
}

func (s *cliImageState) operatorPresses(key string) error {
	switch key {
	case "ctrl+v":
		s.press("\x16")
	case "alt+v":
		s.press("\x1bv")
	default:
		return fmt.Errorf("unknown key %q", key)
	}
	return nil
}

func (s *cliImageState) markupWasShown(w, h int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shown != image.Pt(w, h) {
		return fmt.Errorf("the markup window was shown %v, want %dx%d", s.shown, w, h)
	}
	return nil
}

// receivedPictures waits for the first stub turn and checks the pictures its
// prompt carried.
func (s *cliImageState) receivedPictures(n int) error {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.cliTUIState.mu.Lock()
		got := len(s.pictures)
		var count int
		if got > 0 {
			count = len(s.pictures[0])
		}
		s.cliTUIState.mu.Unlock()
		if got > 0 {
			if count != n {
				return fmt.Errorf("the stub turn received %d pictures, want %d", count, n)
			}
			return nil
		}
		time.Sleep(15 * time.Millisecond)
	}
	return fmt.Errorf("no stub turn ran")
}

func (s *cliImageState) receivedIs(which string) error {
	want := s.copied
	if which == "marked-up" {
		want = s.marked
	}
	s.cliTUIState.mu.Lock()
	defer s.cliTUIState.mu.Unlock()
	if len(s.pictures) == 0 || len(s.pictures[0]) == 0 {
		return fmt.Errorf("the stub turn received no picture")
	}
	got, err := decodeDataURL(s.pictures[0][0].DataURL)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("the stub turn received %d bytes that are not the %s picture", len(got), which)
	}
	return nil
}

func decodeDataURL(u string) ([]byte, error) {
	_, b64, ok := strings.Cut(u, ";base64,")
	if !ok || !strings.HasPrefix(u, "data:image/") {
		return nil, fmt.Errorf("not an image data URL: %.40s", u)
	}
	return base64.StdEncoding.DecodeString(b64)
}

func (s *cliImageState) queuedCarries(text string, n int) error {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rows, err := s.app.mgr.QueuedTurnMessages(s.app.sessionID)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if strings.TrimSpace(r.Text) == text {
				if len(r.ImageParts) != n {
					return fmt.Errorf("the queued message carries %d pictures, want %d", len(r.ImageParts), n)
				}
				return nil
			}
		}
		time.Sleep(15 * time.Millisecond)
	}
	return fmt.Errorf("no queued message %q", text)
}

func initializeCLIImagePasteScenario(sc *godog.ScenarioContext) {
	s := &cliImageState{cliTUIState: &cliTUIState{}}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		s.io = imageIO{}
		s.copied, s.marked, s.shown = nil, nil, image.Point{}
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.shutdown()
		return ctx, nil
	})
	sc.Step(`^a coddy console app over a stub agent runner$`, s.aConsoleAppOverStubRunner)
	sc.Step(`^the configured models take pictures$`, s.modelsTakePictures)
	sc.Step(`^the clipboard holds a (\d+)x(\d+) picture$`, s.clipboardHolds)
	sc.Step(`^the markup window marks the picture up and clicks Done$`, s.markupClicksDone)
	sc.Step(`^the markup window is cancelled$`, s.markupCancelled)
	sc.Step(`^there is no display for the markup window$`, s.noDisplay)
	sc.Step(`^the console app starts$`, s.theConsoleAppStarts)
	sc.Step(`^the operator presses (ctrl\+v|alt\+v)$`, s.operatorPresses)
	sc.Step(`^the editor shows "([^"]*)"$`, func(text string) error { return s.waitScreen(text, 3*time.Second) })
	sc.Step(`^the screen shows "([^"]*)"$`, func(text string) error { return s.waitScreen(text, 3*time.Second) })
	sc.Step(`^the screen does not show "([^"]*)"$`, s.screenDoesNotShow)
	sc.Step(`^the markup window was shown the (\d+)x(\d+) picture$`, s.markupWasShown)
	sc.Step(`^the operator submits the prompt "([^"]*)"$`, s.operatorSubmitsPrompt)
	sc.Step(`^the stub turn received (\d+) pictures?$`, s.receivedPictures)
	sc.Step(`^the picture the stub turn received is the (marked-up|copied) one$`, s.receivedIs)
	sc.Step(`^the queued message "([^"]*)" carries (\d+) pictures?$`, s.queuedCarries)
}

func TestCLIImagePasteFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "cli-image-paste",
		ScenarioInitializer: initializeCLIImagePasteScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/cli_image_paste.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("cli image paste feature suite failed")
	}
}
