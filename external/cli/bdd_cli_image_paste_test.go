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
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/external/cli/clipimage"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type cliImageState struct {
	*cliTUIState
	mu     sync.Mutex
	copied []byte
	marked []byte
	shown  image.Point
	io     imageIO
	// model is the scripted model of a console over the real agent.
	model *recordingModel
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

// aConsoleOverTheAgent builds the console the way `coddy` does - the real
// agent, a session store on disk - over a scripted OpenAI-compatible model
// marked as one that takes pictures.
func (s *cliImageState) aConsoleOverTheAgent() error {
	s.model = newRecordingModel()
	home, err := os.MkdirTemp("", "coddy-cli-image-")
	if err != nil {
		return err
	}
	s.home, s.homes, s.cwd = home, append(s.homes, home), filepath.Join(home, "work")
	if err := os.MkdirAll(s.cwd, 0o755); err != nil {
		return err
	}
	yaml := fmt.Sprintf(`providers:
  - name: stub
    type: openai
    api_base: "%s/v1"
    api_key: "sk-stub"
models:
  - model: stub/coddy-demo
    max_context_tokens: 131072
    multimodal: true
agent:
  model: stub/coddy-demo
rules:
  auto_discover: false
`, s.model.ts.URL)
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(yaml), 0o644); err != nil {
		return err
	}
	cfg, err := config.LoadFromCLI(config.CLIPaths{Home: home, CWD: s.cwd})
	if err != nil {
		return err
	}
	s.cfg = cfg
	s.store = &session.FileStore{Root: filepath.Join(home, "sessions")}
	s.app = buildApp(cfg, s.store, slog.New(slog.DiscardHandler), &bddTerminal{cols: 100, rows: 35}, "dark", true)
	s.wakerAttached = true
	return nil
}

func (s *cliImageState) modelAnswers() error {
	return s.waitScreen(promptInputAnswer, 20*time.Second)
}

// modelWasSentThePicture finds the user message of the completion request
// that carries the prompt, and the copied picture next to it as an image part.
func (s *cliImageState) modelWasSentThePicture(prompt string) error {
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(s.copied)
	for _, body := range s.model.calls() {
		var req struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if json.Unmarshal(body, &req) != nil {
			continue
		}
		for _, m := range req.Messages {
			var parts []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
			}
			if m.Role != "user" || json.Unmarshal(m.Content, &parts) != nil {
				continue
			}
			text, picture := false, false
			for _, p := range parts {
				text = text || strings.Contains(p.Text, prompt)
				picture = picture || (p.Type == "image_url" && p.ImageURL.URL == want)
			}
			if text && picture {
				return nil
			}
		}
	}
	return fmt.Errorf("no request carried %q with the picture (%d requests)", prompt, len(s.model.calls()))
}

// sessionKeepsThePicture reads the session from disk the way the web UI's
// GET /coddy/sessions/{id}/messages does: a user message whose picture is a
// file of the session's assets with a thumbnail, which the web UI turns into
// a preview card (external/httpserver/coddy_coddy.go).
func (s *cliImageState) sessionKeepsThePicture() error {
	var id string
	if err := s.onLoop(2*time.Second, func() { id = s.app.sessionID }); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		snap, err := s.store.ReadSnapshot(id)
		if err == nil {
			for _, m := range snap.Messages {
				if m.Role != llm.RoleUser || len(m.ImageParts) != 1 {
					continue
				}
				p := m.ImageParts[0]
				data, err := os.ReadFile(p.FilePath)
				if err != nil || !bytes.Equal(data, s.copied) {
					return fmt.Errorf("the session's copy of the picture %q: %v", p.FilePath, err)
				}
				if filepath.Dir(p.FilePath) != filepath.Join(snap.Dir, "assets") {
					return fmt.Errorf("the picture %q is not with the session's assets", p.FilePath)
				}
				thumb, err := os.ReadFile(p.ThumbnailPath)
				if err != nil {
					return fmt.Errorf("no thumbnail: %v", err)
				}
				if _, err := png.Decode(bytes.NewReader(thumb)); err != nil {
					return fmt.Errorf("the thumbnail is not a PNG: %v", err)
				}
				if got := session.UserMessageText(m.Content); got != "[image #1] what is on it?" {
					return fmt.Errorf("the console shows the prompt as %q", got)
				}
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no saved user message with the picture (%v)", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// sessionHoldsAWebPrompt writes into the console's session the message a
// prompt sent from the web UI with a picture leaves: the text, the note of
// the files saved with it (internal/agent filePathsNote), and the picture.
func (s *cliImageState) sessionHoldsAWebPrompt(text string) error {
	var err error
	if e := s.onLoop(3*time.Second, func() {
		st := s.app.mgr.SessionByID(s.app.sessionID)
		if st == nil {
			err = fmt.Errorf("no live session")
			return
		}
		dir, derr := s.store.EnsureLayout(st.GetID())
		if derr != nil {
			err = derr
			return
		}
		asset := filepath.Join(dir, "assets", "pasted-1.png")
		note := "\n\n<coddy_session_assets>Uploaded files saved to session assets (read-only). You can read or copy them:\n- " +
			asset + "\n</coddy_session_assets>"
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: text + note,
			ImageParts: []llm.ImagePart{{Name: "pasted-1.png", FilePath: asset}}})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "It is a chart."})
		err = s.store.Save(st)
	}); e != nil {
		return e
	}
	return err
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
	sc.Step(`^a coddy console app over the agent and a scripted model that takes pictures$`, s.aConsoleOverTheAgent)
	sc.Step(`^the model answers$`, s.modelAnswers)
	sc.Step(`^the model was sent the picture with the prompt "([^"]*)"$`, s.modelWasSentThePicture)
	sc.Step(`^the session keeps the picture with a thumbnail the web UI shows$`, s.sessionKeepsThePicture)
	sc.Step(`^the session holds a prompt "([^"]*)" sent from the web UI with a picture$`, s.sessionHoldsAWebPrompt)
	sc.Step(`^the stub turn streams the text "([^"]*)"$`, s.stubStreamsText)
	// A message is queued only while the manager runs a turn; the first turn
	// of a fresh console can take seconds to reach the stub on a loaded
	// runner, and a message sent before then is held as a prompt of its own.
	sc.Step(`^the stub turn blocks until cancelled$`, s.stubBlocksUntilCancelled)
	sc.Step(`^the operator starts a new session$`, s.operatorStartsNewSession)
	sc.Step(`^the operator resumes the session before$`, s.operatorResumesSessionBefore)
}

func TestCLIImagePasteFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "cli-image-paste",
		ScenarioInitializer: initializeCLIImagePasteScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/cli_image_paste.feature", "../../features/cli_image_paste_surfaces.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("cli image paste feature suite failed")
	}
}
