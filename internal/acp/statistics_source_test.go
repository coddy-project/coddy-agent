package acp_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/statistics"
)

// sourceHandler reports the surface the turn arrived under. A request is
// answered on a goroutine of its own, so the channel is what hands it over.
type sourceHandler struct {
	mockHandler
	seen chan string
}

func (h *sourceHandler) HandleSessionPrompt(ctx context.Context, _ acp.SessionPromptParams) (*acp.SessionPromptResult, error) {
	h.seen <- statistics.Source(ctx)
	return &acp.SessionPromptResult{StopReason: acp.StopReasonEndTurn}, nil
}

// The editor's wire names itself for the usage history, so its turns are told
// apart from the console's instead of sharing the bucket of a surface that
// named none.
func TestSessionPromptNamesTheACPWire(t *testing.T) {
	handler := &sourceHandler{seen: make(chan string, 1)}
	var out bytes.Buffer
	server := acp.NewServerWithWriter(handler, slog.New(slog.DiscardHandler), &out)
	line := `{"jsonrpc":"2.0","id":1,"method":"session/prompt","params":{"sessionId":"sess_acp","prompt":[{"type":"text","text":"hi"}]}}` + "\n"
	if err := server.Run(context.Background(), strings.NewReader(line)); err != nil && err != io.EOF {
		t.Fatalf("run: %v", err)
	}
	select {
	case got := <-handler.seen:
		if got != "acp" {
			t.Fatalf("the turn reached the agent as %q, want \"acp\"", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the prompt never reached the agent")
	}
}
