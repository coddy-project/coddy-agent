package mcptest_test

import (
	"bytes"
	"context"
	"log/slog"
	"os/exec"
	"slices"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp/mcptest"
)

func TestHelperMCPTestServer(t *testing.T) { mcptest.Main() }

// TestEveryTransportAnswersItsToken connects to one server of each kind the
// way a session does, calls its tool, and takes a screenshot over 3 MiB with
// it: over 4 MiB of base64 on one line of JSON, and on one data line of an
// event stream for the remote servers that answer with one.
func TestEveryTransportAnswersItsToken(t *testing.T) {
	httpSrv := mcptest.NewHTTPServer("HTTP-TOKEN")
	defer httpSrv.Close()
	streamSrv := mcptest.NewEventStreamServer("STREAM-TOKEN")
	defer streamSrv.Close()
	sseSrv := mcptest.NewSSEServer("SSE-TOKEN")
	defer sseSrv.Close()
	for _, tc := range []struct {
		srv   config.MCPServerConfig
		token string
	}{
		{mcptest.Stdio("native", "STDIO-TOKEN"), "STDIO-TOKEN"},
		{config.MCPServerConfig{Name: "remote", Type: "http", URL: httpSrv.URL}, "HTTP-TOKEN"},
		{config.MCPServerConfig{Name: "streamed", Type: "http", URL: streamSrv.URL}, "STREAM-TOKEN"},
		{config.MCPServerConfig{Name: "legacy", Type: "sse", URL: sseSrv.URL}, "SSE-TOKEN"},
	} {
		t.Run(tc.srv.Name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			client, err := mcp.Connect(ctx, tc.srv, t.TempDir(), slog.Default())
			if err != nil {
				t.Fatalf("connect: %v", err)
			}
			defer func() { _ = client.Close() }()
			var names []string
			for _, tool := range client.Tools() {
				names = append(names, tool.Name)
			}
			if !slices.Equal(names, []string{mcptest.Tool, mcptest.ScreenshotTool}) {
				t.Fatalf("tools = %v, want [%s %s]", names, mcptest.Tool, mcptest.ScreenshotTool)
			}
			got, err := client.CallTool(ctx, mcptest.Tool, `{}`)
			if err != nil || got != tc.token {
				t.Fatalf("CallTool = %q, %v; want %q", got, err, tc.token)
			}
			shot, err := client.CallToolResult(ctx, mcptest.ScreenshotTool, `{"large":true}`)
			if err != nil {
				t.Fatalf("the screenshot call: %v", err)
			}
			if shot.Text != tc.token || len(shot.Images) != 1 || !bytes.Equal(shot.Images[0].Data, mcptest.LargePicture(tc.token)) {
				t.Fatalf("the screenshot call answered text %q and %d images, want the token and the large picture", shot.Text, len(shot.Images))
			}
		})
	}
	for name, srv := range map[string]*mcptest.Server{"http": httpSrv, "stream": streamSrv, "sse": sseSrv} {
		if srv.Calls() != 2 {
			t.Errorf("%s server: %d tool calls, want two", name, srv.Calls())
		}
	}
}

// TestNPXPackageAnswersItsToken starts the local package through the real
// npx, which is how most published MCP servers are run.
func TestNPXPackageAnswersItsToken(t *testing.T) {
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx is not on PATH")
	}
	srv, err := mcptest.NPX("packaged", "NPX-TOKEN", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client, err := mcp.Connect(ctx, srv, t.TempDir(), slog.Default())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = client.Close() }()
	got, err := client.CallTool(ctx, mcptest.Tool, `{}`)
	if err != nil || got != "NPX-TOKEN" {
		t.Fatalf("CallTool = %q, %v; want NPX-TOKEN", got, err)
	}
}
