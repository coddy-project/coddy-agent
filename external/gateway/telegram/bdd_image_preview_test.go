//go:build gateway || gateway.telegram

package telegram

// Godog harness for features/gateway_telegram_image_preview.feature: a chat
// turn runs the real agent, whose read of an image file - or call of an MCP
// tool that returns a screenshot - hands the picture to a scripted model that
// reads images; the bot has to send the picture into the chat. Telegram is
// the fake Bot API, the MCP server this test binary re-executed; nothing
// leaves the machine.

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
	"github.com/EvilFreelancer/coddy-agent/internal/mcp/mcptest"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// TestHelperMCPTestServer is the stdio server mcptest.Stdio declares: this
// test binary re-executed, serving its token.
func TestHelperMCPTestServer(t *testing.T) { mcptest.Main() }

// pictureMCPToken is what the screenshot server of the MCP scenario answers.
const pictureMCPToken = "TG-SHOT"

const (
	pictureChatID = int64(8080)
	pictureUserID = int64(8080)
)

type pictureWorld struct {
	root  string
	shot  []byte
	fake  *fakeAPI
	model *httptest.Server
	mgr   *session.Manager
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
	if w.mgr != nil {
		// The MCP servers a turn started belong to the manager's pool.
		w.mgr.CloseMCP()
		w.mgr = nil
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

	if err := os.MkdirAll(filepath.Join(w.root, "work"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(w.root, "work", name), w.shot, 0o644); err != nil {
		return err
	}
	read, _ := json.Marshal(map[string]any{"path": name})
	return w.startChat([]llmstub.Rule{{Match: "look at the screenshot", Tool: &llmstub.ToolCall{Name: "read", Arguments: read}}}, "It is a red square.", false)
}

// chatWithScreenshotServer builds the same chat with an MCP server in the
// operator's mcp.json whose tool returns a screenshot, and no file at all.
// MCP tools are the bot's admins', so the person is one, and the operator
// lets the agent call tools without asking.
func (w *pictureWorld) chatWithScreenshotServer(server string) error {
	w.shot = mcptest.Picture(pictureMCPToken)
	home := filepath.Join(w.root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	srv := mcptest.Stdio(server, pictureMCPToken)
	if err := config.UpsertMCPJSONServer(config.GlobalMCPJSONPath(home), server, config.MCPJSONFromServer(srv)); err != nil {
		return err
	}
	return w.startChat([]llmstub.Rule{
		{Match: "take a screenshot", Tool: &llmstub.ToolCall{Name: server + "__" + mcptest.ScreenshotTool, Arguments: json.RawMessage(`{}`)}},
		{Match: "take three screenshots", Tool: &llmstub.ToolCall{Name: server + "__" + mcptest.ScreenshotTool, Arguments: json.RawMessage(`{"count":3}`)}},
	}, "The page is blank.", true)
}

// startChat starts the scripted model - one of calls happens on the
// person's request, answer once the pictures came - and the bot over it,
// with the person as its admin when admin is set.
func (w *pictureWorld) startChat(calls []llmstub.Rule, answer string, admin bool) error {
	stub := &llmstub.Server{Model: "coddy-demo", StripTags: []string{"turn_context"}, Rules: append(calls,
		llmstub.Rule{Match: "pictures the tool calls above returned", Answer: answer})}
	w.model = httptest.NewServer(stub.Handler())

	home := filepath.Join(w.root, "home")
	cwd := filepath.Join(w.root, "work")
	for _, dir := range []string{home, cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: cwd},
		Providers: []config.ProviderConfig{{Name: "stub", Type: "openai", APIBase: w.model.URL + "/v1", APIKey: "sk-stub"}},
		Models:    []config.ModelEntry{{Model: "stub/coddy-demo", MaxContextTokens: 131072, Multimodal: true}},
		Agent:     config.Agent{Model: "stub/coddy-demo"},
	}
	var admins []int64
	if admin {
		admins = []int64{pictureUserID}
		cfg.Tools.PermissionMode = config.PermModeBypass
	}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	log := slog.New(slog.DiscardHandler)
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		return agent.NewAgent(cfg, st, snd, log).Run(ctx, prompt)
	}
	store := &session.FileStore{Root: filepath.Join(w.root, "sessions")}
	w.mgr = session.NewManager(cfg, noopUpdateSender{}, runner, log, cwd, store)

	w.fake = openFakeAPI(tgfake.Options{})
	w.bot = New(&config.TelegramGatewayConfig{
		Enabled: true, Token: "t", DefaultAccess: config.AccessAll, DefaultIsolation: config.IsolationIndividual, Admins: admins,
	}, w.mgr, cwd, log, "", nil)
	w.bot.setAPI(w.fake.api)
	return nil
}

func (w *pictureWorld) personAsksForThreeScreenshots() error {
	msg := w.fake.userMessage(pictureChatID, pictureUserID, "Please take three screenshots of the page.")
	w.bot.processMessage(context.Background(), w.fake.api, msg, w.chatKey())
	return nil
}

// chatReceivesAlbum: the pictures of the call are photos of one album, in
// order, each captioned with its name and holding the picture the server
// returned.
func (w *pictureWorld) chatReceivesAlbum(first, last string) error {
	want := mcptest.Pictures(pictureMCPToken, 3)
	var photos []tgfake.MessageView
	for _, m := range w.fake.fake.Chat(pictureChatID).Messages {
		if m.From == "bot" && m.Photo != nil {
			photos = append(photos, m)
		}
	}
	if len(photos) != len(want) {
		return fmt.Errorf("the chat received %d photos, want %d:\n%s", len(photos), len(want), w.fake.fake.Chat(pictureChatID).Text())
	}
	for i, m := range photos {
		name := fmt.Sprintf("browser-take_screenshot-%d.png", i+1)
		if m.MediaGroupID == "" || m.MediaGroupID != photos[0].MediaGroupID {
			return fmt.Errorf("photo %d is not in the album of the first (%q, %q)", i+1, m.MediaGroupID, photos[0].MediaGroupID)
		}
		if m.Caption != name {
			return fmt.Errorf("photo %d is captioned %q, want %q", i+1, m.Caption, name)
		}
		if got, ok := w.fake.fake.File(m.Photo.FileID); !ok || !bytes.Equal(got, want[i]) {
			return fmt.Errorf("photo %d is not the picture the server returned", i+1)
		}
	}
	if photos[0].Caption != first || photos[len(photos)-1].Caption != last {
		return fmt.Errorf("the album runs from %q to %q", photos[0].Caption, photos[len(photos)-1].Caption)
	}
	if n := len(w.fake.fake.Calls("sendPhoto")); n != 0 {
		return fmt.Errorf("the pictures also went as %d single photos", n)
	}
	return nil
}

func (w *pictureWorld) personAsksForScreenshot() error {
	msg := w.fake.userMessage(pictureChatID, pictureUserID, "Please take a screenshot of the page.")
	w.bot.processMessage(context.Background(), w.fake.api, msg, w.chatKey())
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
	sc.Step(`^a chat whose agent reads images and an MCP server "([^"]*)" that takes screenshots$`, w.chatWithScreenshotServer)
	sc.Step(`^the person asks the agent to take a screenshot of the page$`, w.personAsksForScreenshot)
	sc.Step(`^the person asks the agent to take three screenshots of the page$`, w.personAsksForThreeScreenshots)
	sc.Step(`^the chat receives the three screenshots as one album captioned "([^"]*)" to "([^"]*)"$`, w.chatReceivesAlbum)
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
