// Command tgfake is the offline stand for the Telegram gateway: a fake Bot
// API server with a chat page, and optionally a scripted model server, so
// coddy serve can run a bot with no token, no phone and no network. See
// docs/surfaces/gateway.md (Debugging against a fake Bot API).
//
//	go run ./cmd/tgfake --llm                     # fake Bot API + scripted model on :18790
//	CODDY_TELEGRAM_API_BASE=http://127.0.0.1:18790 coddy serve --gateway --http=false
//	open http://127.0.0.1:18790/                  # the person's side of the chat
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/tgfake"
	"github.com/EvilFreelancer/coddy-agent/internal/tgfake/llmstub"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18790", "address to listen on")
	token := flag.String("token", "", "the only bot token accepted; empty accepts any")
	botName := flag.String("bot-username", "coddy_fake_bot", "username getMe reports")
	pollMax := flag.Duration("poll-max", 30*time.Second, "longest a getUpdates request is held open")
	verbose := flag.Bool("verbose", false, "print every Bot API call")

	llm := flag.Bool("llm", false, "also serve a scripted OpenAI-compatible model under /v1")
	llmModel := flag.String("llm-model", "coddy-demo", "model id the scripted model reports")
	llmScript := flag.String("llm-script", "", "JSON file with [{\"match\": \"...\", \"answer\": \"...\"}] rules; a rule with \"tool\": {\"name\": ..., \"arguments\": {...}} calls that tool first and answers its result")
	llmDelay := flag.Duration("llm-delay", 50*time.Millisecond, "pause between streamed chunks")
	llmChunk := flag.Int("llm-chunk-words", 1, "words per streamed chunk")
	var llmAnswers stringList
	flag.Var(&llmAnswers, "llm-answer", "a canned answer, used in turn; repeatable")
	flag.Parse()

	opts := tgfake.Options{Token: *token, BotUsername: *botName, MaxPollWait: *pollMax}
	if *verbose {
		opts.Logf = log.Printf
	}
	var stub *llmstub.Server
	if *llm {
		stub = &llmstub.Server{Model: *llmModel, Answers: llmAnswers, Delay: *llmDelay, ChunkWords: *llmChunk}
		if *llmScript != "" {
			rules, err := loadRules(*llmScript)
			if err != nil {
				fail(err)
			}
			stub.Rules = rules
		}
	}

	fake := tgfake.New(opts)
	srv := &http.Server{Addr: *addr, Handler: newMux(fake, stub), ReadHeaderTimeout: 10 * time.Second}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fail(err)
	}
	origin := "http://" + ln.Addr().String()
	printBanner(origin, *botName, stub)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		// Release the long polls first, then let the listener drain.
		fake.Close()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fail(err)
	}
}

// newMux mounts the fake and, when given, the scripted model.
func newMux(fake *tgfake.Server, stub *llmstub.Server) http.Handler {
	if stub == nil {
		return fake.Handler()
	}
	mux := http.NewServeMux()
	mux.Handle("/v1/", stub.Handler())
	mux.Handle("/", fake.Handler())
	return mux
}

func printBanner(origin, botName string, stub *llmstub.Server) {
	fmt.Printf("tgfake: fake Bot API for @%s at %s\n", botName, origin)
	fmt.Printf("  chat page:   %s/\n", origin)
	fmt.Printf("  point coddy: %s=%s\n", config.TelegramAPIBaseEnv, origin)
	if stub != nil {
		fmt.Printf("  model:       %s/v1 (id %s)\n", origin, stub.Model)
		fmt.Printf("\nconfig.yaml for an offline stand:\n\n")
		fmt.Printf("providers:\n  - name: stub\n    type: openai\n    api_base: \"%s/v1\"\n    api_key: \"sk-tgfake\"\n", origin)
		fmt.Printf("models:\n  - model: stub/%s\nagent:\n  model: stub/%s\n", stub.Model, stub.Model)
		fmt.Printf("httpserver:\n  enable: false\n")
		fmt.Printf("gateways:\n  telegram:\n    enable: true\n    token: \"123456:fake\"\n\n")
	}
}

func loadRules(path string) ([]llmstub.Rule, error) {
	data, err := os.ReadFile(path) //nolint:gosec // operator-supplied path
	if err != nil {
		return nil, err
	}
	var rules []llmstub.Rule
	if err := json.Unmarshal(data, &rules); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return rules, nil
}

// stringList collects a repeatable flag.
type stringList []string

func (l *stringList) String() string     { return strings.Join(*l, ", ") }
func (l *stringList) Set(v string) error { *l = append(*l, v); return nil }

func fail(err error) {
	fmt.Fprintln(os.Stderr, "tgfake:", err)
	os.Exit(1)
}
