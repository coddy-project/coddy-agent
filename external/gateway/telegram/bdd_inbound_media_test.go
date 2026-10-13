//go:build gateway || gateway.telegram

package telegram

// Godog harness for features/gateway_telegram_inbound_media.feature: the
// person sends the bot a photo, a document or an album through the fake Bot
// API, the bot downloads the files with getFile and runs the real manager and
// agent, and a scripted model that reads images answers. The requests the
// model receives are recorded, so the scenarios check what it was shown.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/tgfake/pkg/llmstub"
	tgfake "github.com/EvilFreelancer/tgfake/pkg/server"
	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	inboundChatID = int64(9090)
	inboundUserID = int64(9090)
)

// modelRequests records the bodies of the chat completions a model server is
// sent.
type modelRequests struct {
	mu     sync.Mutex
	bodies [][]byte
}

func (r *modelRequests) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasSuffix(req.URL.Path, "/chat/completions") {
			body, _ := io.ReadAll(req.Body)
			r.mu.Lock()
			r.bodies = append(r.bodies, body)
			r.mu.Unlock()
			req.Body = io.NopCloser(bytes.NewReader(body))
		}
		next.ServeHTTP(w, req)
	})
}

func (r *modelRequests) all() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]byte(nil), r.bodies...)
}

// askedPrompt is the user message of a recorded request that holds the
// person's words: its text and the data URLs of the pictures it carries.
type askedPrompt struct {
	text     string
	pictures []string
}

// promptOf finds the user message of body whose text contains words.
func promptOf(body []byte, words string) (askedPrompt, bool) {
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(body, &req) != nil {
		return askedPrompt{}, false
	}
	for _, m := range req.Messages {
		if m.Role != "user" {
			continue
		}
		var p askedPrompt
		var text string
		if json.Unmarshal(m.Content, &text) == nil {
			p.text = text
		} else {
			var parts []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
			}
			if json.Unmarshal(m.Content, &parts) != nil {
				continue
			}
			for _, part := range parts {
				switch part.Type {
				case "text":
					p.text += part.Text
				case "image_url":
					p.pictures = append(p.pictures, part.ImageURL.URL)
				}
			}
		}
		if strings.Contains(p.text, words) {
			return p, true
		}
	}
	return askedPrompt{}, false
}

type inboundWorld struct {
	root     string
	sent     [][]byte
	pdf      []byte
	requests *modelRequests
	fake     *fakeAPI
	model    *httptest.Server
	bot      *Bot
	cancel   context.CancelFunc
	ctx      context.Context
}

func (w *inboundWorld) reset() error {
	w.close()
	root, err := os.MkdirTemp("", "coddy-tg-inbound-*")
	if err != nil {
		return err
	}
	w.root = root
	w.sent = nil
	w.ctx, w.cancel = context.WithCancel(context.Background())
	return nil
}

func (w *inboundWorld) close() {
	if w.cancel != nil {
		w.cancel()
		w.cancel = nil
	}
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

// solidPNG is a w x h picture of one colour.
func solidPNG(width, height int, c color.NRGBA) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// chatWhoseModelReadsImages builds the path a chat turn takes in `coddy
// serve` with the gateway alone, on a model marked multimodal.
func (w *inboundWorld) chatWhoseModelReadsImages() error {
	stub := &llmstub.Server{Model: "coddy-demo", StripTags: []string{"turn_context", "coddy_session_assets"}, Rules: []llmstub.Rule{
		{Match: "What is on this picture?", Answer: "It is a red square."},
		{Match: "Summarize the report", Answer: "It is a short report."},
		{Match: "Which one is the brightest?", Answer: "The second one."},
	}}
	w.requests = &modelRequests{}
	w.model = httptest.NewServer(w.requests.wrap(stub.Handler()))

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
		Models:    []config.ModelEntry{{Model: "stub/coddy-demo", MaxContextTokens: 131072, Multimodal: config.BoolPtr(true)}},
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
	w.bot.apiBase = w.fake.srv.URL
	w.bot.albumWait = 100 * time.Millisecond
	return nil
}

// send delivers what the person sent the way the polling loop does.
func (w *inboundWorld) send(in tgfake.IncomingMedia) error {
	in.ChatID, in.UserID = inboundChatID, inboundUserID
	msg, err := w.fake.userMedia(in)
	if err != nil {
		return err
	}
	w.bot.dispatch(w.ctx, w.fake.api, msg)
	return nil
}

func (w *inboundWorld) personSendsPhoto(caption string) error {
	pic := solidPNG(8, 6, color.NRGBA{R: 220, A: 255})
	w.sent = append(w.sent, pic)
	return w.send(tgfake.IncomingMedia{Type: "photo", Data: pic, Caption: caption})
}

func (w *inboundWorld) personSendsDocument(name, caption string) error {
	w.pdf = []byte("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n1 0 obj << /Type /Catalog >> endobj\ntrailer << >>\n%%EOF\n")
	w.sent = append(w.sent, w.pdf)
	return w.send(tgfake.IncomingMedia{Type: "document", Data: w.pdf, FileName: name, Caption: caption})
}

func (w *inboundWorld) personSendsAlbum(n int, caption string) error {
	for i := 0; i < n; i++ {
		pic := solidPNG(8+i, 6, color.NRGBA{R: uint8(60 * (i + 1)), G: 40, B: 40, A: 255})
		w.sent = append(w.sent, pic)
		in := tgfake.IncomingMedia{Type: "photo", Data: pic, MediaGroupID: "album-509"}
		if i == 0 {
			in.Caption = caption
		}
		if err := w.send(in); err != nil {
			return err
		}
	}
	return nil
}

// asked waits for the request that asks the model the person's words.
func (w *inboundWorld) asked(words string) (askedPrompt, error) {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, body := range w.requests.all() {
			if p, ok := promptOf(body, words); ok {
				return p, nil
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return askedPrompt{}, fmt.Errorf("the model was never asked %q (%d requests)", words, len(w.requests.all()))
}

func (w *inboundWorld) askedWithPictures(words string, n int) error {
	p, err := w.asked(words)
	if err != nil {
		return err
	}
	if len(p.pictures) != n {
		return fmt.Errorf("the model was shown %d pictures with %q, want %d", len(p.pictures), words, n)
	}
	for _, url := range p.pictures {
		if !strings.HasPrefix(url, "data:image/png;base64,") {
			return fmt.Errorf("a picture reached the model as %.40q, want a PNG data URL", url)
		}
	}
	return nil
}

func (w *inboundWorld) askedWithPicturesInOneTurn(words string, n int) error {
	if err := w.askedWithPictures(words, n); err != nil {
		return err
	}
	if err := w.answerComes("The second one."); err != nil {
		return err
	}
	if got := len(w.requests.all()); got != 1 {
		return fmt.Errorf("the album took %d model calls, want one turn of one call", got)
	}
	return nil
}

// assetFiles lists the bytes of every file kept with a session of the world.
func (w *inboundWorld) assetFiles() (map[string][]byte, error) {
	out := map[string][]byte{}
	err := filepath.WalkDir(filepath.Join(w.root, "sessions"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Base(filepath.Dir(path)) != "assets" {
			return err
		}
		data, err := os.ReadFile(path)
		if err == nil {
			out[path] = data
		}
		return err
	})
	return out, err
}

func (w *inboundWorld) sessionKeepsThePhoto() error {
	files, err := w.assetFiles()
	if err != nil {
		return err
	}
	for _, data := range files {
		if bytes.Equal(data, w.sent[0]) {
			return nil
		}
	}
	return fmt.Errorf("no asset of the session holds the photo the person sent (%d assets)", len(files))
}

func (w *inboundWorld) toldWhereKept(words, name string) error {
	p, err := w.asked(words)
	if err != nil {
		return err
	}
	files, err := w.assetFiles()
	if err != nil {
		return err
	}
	for path, data := range files {
		if filepath.Base(path) == name && bytes.Equal(data, w.pdf) {
			if !strings.Contains(p.text, "<coddy_session_assets>") || !strings.Contains(p.text, path) {
				return fmt.Errorf("the prompt does not name %s:\n%s", path, p.text)
			}
			return nil
		}
	}
	return fmt.Errorf("the session keeps no %s with the bytes the person sent", name)
}

func (w *inboundWorld) notSentTheBytes() error {
	for _, body := range w.requests.all() {
		if bytes.Contains(body, []byte("%PDF-")) || bytes.Contains(body, []byte("/Catalog")) {
			return fmt.Errorf("a request carries the document's bytes as text")
		}
	}
	return nil
}

func (w *inboundWorld) answerComes(answer string) error {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		count := 0
		for _, m := range w.fake.fake.Chat(inboundChatID).Messages {
			if m.From == "bot" && strings.Contains(m.Text, answer) {
				count++
			}
		}
		if count > 1 {
			return fmt.Errorf("the chat shows %q %d times:\n%s", answer, count, w.fake.fake.Chat(inboundChatID).Text())
		}
		if count == 1 {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("the chat never showed %q:\n%s", answer, w.fake.fake.Chat(inboundChatID).Text())
}

func initializeInboundMediaScenario(sc *godog.ScenarioContext) {
	w := &inboundWorld{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, w.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		w.close()
		return ctx, nil
	})
	sc.Step(`^a chat whose model reads images$`, w.chatWhoseModelReadsImages)
	sc.Step(`^the person sends a photo with the caption "([^"]*)"$`, w.personSendsPhoto)
	sc.Step(`^the person sends the document "([^"]*)" with the caption "([^"]*)"$`, w.personSendsDocument)
	sc.Step(`^the person sends an album of (\d+) photos with the caption "([^"]*)"$`, w.personSendsAlbum)
	sc.Step(`^the model is asked "([^"]*)" with (\d+) pictures? in one turn$`, w.askedWithPicturesInOneTurn)
	sc.Step(`^the model is asked "([^"]*)" with (\d+) pictures?$`, w.askedWithPictures)
	sc.Step(`^the session keeps the photo with its files$`, w.sessionKeepsThePhoto)
	sc.Step(`^the model is asked "([^"]*)" and told where "([^"]*)" is kept$`, w.toldWhereKept)
	sc.Step(`^the model is not sent the document's bytes as text$`, w.notSentTheBytes)
	sc.Step(`^the answer "([^"]*)" comes to the chat$`, w.answerComes)
}

func TestGatewayTelegramInboundMediaFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "gateway-telegram-inbound-media",
		ScenarioInitializer: initializeInboundMediaScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../../features/gateway_telegram_inbound_media.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("gateway telegram inbound media feature failed")
	}
}
