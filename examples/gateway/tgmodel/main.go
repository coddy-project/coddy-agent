// Command tgmodel is the offline Telegram stand's scripted model fixture.
// It is not part of the Coddy binary; tgfake still serves the fake Bot API.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/EvilFreelancer/tgfake/pkg/llmstub"

	"github.com/EvilFreelancer/coddy-agent/internal/llm/llmtest"
)

func main() {
	port := flag.Int("port", 0, "loopback port; 0 chooses a free one")
	rulesFile := flag.String("rules", "", "offline stand rules JSON")
	delay := flag.Duration("delay", 50*time.Millisecond, "pause between answer chunks")
	flag.Parse()
	if err := run(*port, *rulesFile, *delay); err != nil {
		fmt.Fprintln(os.Stderr, "tgmodel:", err)
		os.Exit(1)
	}
}

func run(port int, rulesFile string, delay time.Duration) error {
	raw, err := os.ReadFile(rulesFile)
	if err != nil {
		return err
	}
	var rules []llmstub.Rule
	if err := json.Unmarshal(raw, &rules); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	stub := &llmstub.Server{Model: "coddy-demo", Rules: rules, Delay: delay, StripTags: []string{"turn_context"}}
	return serveModel(ctx, listener, stub, os.Stdout)
}

func serveModel(ctx context.Context, listener net.Listener, stub *llmstub.Server, output io.Writer) error {
	server := &http.Server{Handler: llmtest.DiscoveryHandler(stub), ReadHeaderTimeout: 10 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		_ = server.Close()
		if serveErr := <-done; serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return errors.Join(err, serveErr)
		}
		return err
	}
	err := <-done
	_, _ = fmt.Fprintf(output, "offline model fixture: %d physical requests\n", stub.Calls())
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
