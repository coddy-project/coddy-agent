//go:build gateway || gateway.telegram

package telegram

// Godog harness for features/gateway_telegram_image_preview.feature: a chat
// turn runs the real agent, whose read of an image file hands the picture to
// a scripted model that reads images; the bot has to send the picture into
// the chat. Telegram is the fake Bot API; nothing leaves the machine.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/tgfake/pkg/llmstub"
	tgfake "github.com/EvilFreelancer/tgfake/pkg/server"
	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	pictureChatID = int64(8080)
	pictureUserID = int64(8080)
)

type pictureWorld struct {
	root  string
	shot  []byte
	fake  *fakeAPI
	model *httptest.Server
	bot   *Bot
}

func (w *pictureWorld) reset() error {
	w.close()
	root, err := os.MkdirTemp("", "coddy-tg-picture-*")
	if err != nil {
		return err
	}
	w.root = root
	return nil
}

func (w *pictureWorld) close() {
	if w.bot != nil {
		w.bot.asks.stop()
		w.bot = nil
	}
	if w.fake != nil {
		w.fake.close()
		w.fake = nil
	}
	if w.model != nil {
		w.model.Close()
		w.model = nil
	}
	if w.root != "" {
		_ = os.RemoveAll(w.root)
		w.root = ""
	}
}

func (w *pictureWorld) chatKey() string {
	return sessionstore.SessionKey(adapterName, pictureChatID, pictureUserID, config.IsolationIndividual, false)
}

// chatThatReadsImages builds the path a chat turn takes in `coddy serve` with
// the gateway alone, on a model marked multimodal, over a workspace holding
// one screenshot.
func (w *pictureWorld) chatThatReadsImages(name string) error {
	img := image.NewNRGBA(image.Rect(0, 0, 8, 6))
	for y := 0; y < 6; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.NRGBA{R: 220, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return err
	}
	w.shot = buf.Bytes()

	read, _ := json.Marshal(map[string]any{"path": name})
	stub := &llmstub.Server{Model: "coddy-demo", StripTags: []string{"turn_context"}, Rules: []llmstub.Rule{
		{Match: "look at the screenshot", Tool: &llmstub.ToolCall{Name: "read", Arguments: read}},
		{Match: "pictures the tool calls above returned", Answer: "It is a red square."},
	}}
	w.model = httptest.NewServer(stub.Handler())

	home := filepath.Join(w.root, "home")
	cwd := filepath.Join(w.root, "work")
	for _, dir := range []string{home, cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(cwd, name), w.shot, 0o644); err != nil {
		return err
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: cwd},
		Providers: []config.ProviderConfig{{Name: "stub", Type: "openai", APIBase: w.model.URL + "/v1", APIKey: "sk-stub"}},
		Models:    []config.ModelEntry{{Model: "stub/coddy-demo", MaxContextTokens: 131072, Multimodal: true}},
		Agent:     config.Agent{Model: "stub/coddy-demo"},
	}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	log := slog.New(slog.DiscardHandler)
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		return agent.NewAgent(cfg, st, snd, log).Run(ctx, prompt)
	}
	store := &session.FileStore{Root: filepath.Join(w.root, "sessions")}
	mgr := session.NewManager(cfg, noopUpdateSender{}, runner, log, cwd, store)

	w.fake = openFakeAPI(tgfake.Options{})
	w.bot = New(&config.TelegramGatewayConfig{
		Enabled: true, Token: "t", DefaultAccess: config.AccessAll, DefaultIsolation: config.IsolationIndividual,
	}, mgr, cwd, log, "", nil)
	w.bot.setAPI(w.fake.api)
	return nil
}

func (w *pictureWorld) personAsks() error {
	msg := w.fake.userMessage(pictureChatID, pictureUserID, "Please look at the screenshot.")
	w.bot.processMessage(context.Background(), w.fake.api, msg, w.chatKey())
	return nil
}

func (w *pictureWorld) chatReceivesPhoto(name string) error {
	for _, m := range w.fake.fake.Chat(pictureChatID).Messages {
		if m.From != "bot" || m.Photo == nil {
			continue
		}
		if m.Caption != name {
			return fmt.Errorf("the photo is captioned %q, want %q", m.Caption, name)
		}
		got, ok := w.fake.fake.File(m.Photo.FileID)
		if !ok || !bytes.Equal(got, w.shot) {
			return fmt.Errorf("the photo is not the screenshot the model was shown")
		}
		return nil
	}
	return fmt.Errorf("the chat received no photo:\n%s", w.fake.fake.Chat(pictureChatID).Text())
}

func (w *pictureWorld) answerFollows(answer string) error {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		view := w.fake.fake.Chat(pictureChatID)
		photoAt, answerAt := -1, -1
		for i, m := range view.Messages {
			if m.From == "bot" && m.Photo != nil && photoAt < 0 {
				photoAt = i
			}
			if m.From == "bot" && strings.Contains(m.Text, answer) {
				answerAt = i
			}
		}
		if answerAt >= 0 {
			if photoAt < 0 {
				return fmt.Errorf("the answer came without the photo:\n%s", view.Text())
			}
			if answerAt < photoAt {
				return fmt.Errorf("the answer stands above the photo it follows:\n%s", view.Text())
			}
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("the chat never showed %q:\n%s", answer, w.fake.fake.Chat(pictureChatID).Text())
}

func initializeImagePreviewScenario(sc *godog.ScenarioContext) {
	w := &pictureWorld{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, w.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		w.close()
		return ctx, nil
	})
	sc.Step(`^a chat whose agent reads images and a screenshot "([^"]*)" in the workspace$`, w.chatThatReadsImages)
	sc.Step(`^the person asks the agent to look at the screenshot$`, w.personAsks)
	sc.Step(`^the chat receives "([^"]*)" as a photo of the screenshot$`, w.chatReceivesPhoto)
	sc.Step(`^the answer "([^"]*)" follows in the chat$`, w.answerFollows)
}

func TestGatewayTelegramImagePreviewFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "gateway-telegram-image-preview",
		ScenarioInitializer: initializeImagePreviewScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../../features/gateway_telegram_image_preview.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("gateway telegram image preview feature failed")
	}
}
