//go:build http

package httpserver

// Steps of the scenario of features/mcp_tool_calls.feature in which MCP
// tools return screenshots: a real ReAct turn over POST /v1/responses whose
// model reads images takes a screenshot with a server of every transport -
// "native" on stdio, "remote" over streamable HTTP answering with JSON,
// "streamed" over streamable HTTP answering with event streams (as the
// TypeScript SDK does by default) and "legacy" over HTTP+SSE - and the
// picture has to reach the model, the stream the web UI reads, the
// transcript and the session's asset route. "streamed" and "legacy" return a
// picture over 3 MiB, more than 4 MiB of base64 on one line of their event
// streams.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp/mcptest"
)

// mcpShotServers are the servers of the scenario, in the order the model
// takes their screenshots, with the token each answers and whether its
// picture is the large one.
var (
	mcpShotServers = []string{"native", "remote", "streamed", "legacy"}
	mcpShotTokens  = map[string]string{"native": "SHOT-NATIVE", "remote": "SHOT-HTTP", "streamed": "SHOT-STREAM", "legacy": "SHOT-SSE"}
	mcpShotLarge   = map[string]bool{"streamed": true, "legacy": true}
)

// mcpShotCallPrefix starts the id of every screenshot call.
const mcpShotCallPrefix = "shot-"

type mcpShotsState struct {
	servers []*mcptest.Server
	model   *mcpShotsProvider
	// events are the tool_call_update frames of the streamed turn, by call.
	events map[string][]map[string]any
}

func (k *mcpShotsState) close() {
	for _, srv := range k.servers {
		srv.Close()
	}
}

// mcpShotsProvider plays a model that reads images: each call of the turn
// takes the screenshot of the next server whose result is not in the history
// yet, and once every result is there it answers. Every request is kept.
type mcpShotsProvider struct {
	mu       sync.Mutex
	requests [][]llm.Message
}

func (p *mcpShotsProvider) Complete(ctx context.Context, messages []llm.Message, tools []llm.ToolDefinition) (*llm.Response, error) {
	return p.Stream(ctx, messages, tools, func(llm.StreamChunk) {})
}

func (p *mcpShotsProvider) Stream(_ context.Context, messages []llm.Message, tools []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	if len(tools) == 0 {
		onChunk(llm.StreamChunk{TextDelta: "Ready."})
		return &llm.Response{Content: "Ready.", StopReason: "end_turn"}, nil
	}
	p.mu.Lock()
	p.requests = append(p.requests, append([]llm.Message(nil), messages...))
	p.mu.Unlock()
	done := map[string]bool{}
	for _, m := range messages {
		if m.Role == llm.RoleTool && strings.HasPrefix(m.ToolCallID, mcpShotCallPrefix) {
			done[strings.TrimPrefix(m.ToolCallID, mcpShotCallPrefix)] = true
		}
	}
	for _, name := range mcpShotServers {
		if done[name] {
			continue
		}
		args := `{}`
		if mcpShotLarge[name] {
			args = `{"large":true}`
		}
		tc := llm.ToolCall{ID: mcpShotCallPrefix + name, Name: name + "__" + mcptest.ScreenshotTool, InputJSON: args}
		onChunk(llm.StreamChunk{ToolCall: &tc})
		return &llm.Response{ToolCalls: []llm.ToolCall{tc}, StopReason: "tool_use"}, nil
	}
	answer := "I looked at every screenshot."
	onChunk(llm.StreamChunk{TextDelta: answer})
	return &llm.Response{Content: answer, StopReason: "end_turn"}, nil
}

func (p *mcpShotsProvider) lastRequest() []llm.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) == 0 {
		return nil
	}
	return p.requests[len(p.requests)-1]
}

// mcpShotPicture is the picture the server name returns in the scenario.
func mcpShotPicture(name string) []byte {
	if mcpShotLarge[name] {
		return mcptest.LargePicture(mcpShotTokens[name])
	}
	return mcptest.Picture(mcpShotTokens[name])
}

// mcpShotName is the name the agent gives the picture of server name.
func mcpShotName(name string) string {
	return name + "-" + mcptest.ScreenshotTool + "-1.png"
}

// startShotsServer boots the gateway on a model that reads images, with one
// screenshot server of every transport declared in <home>/mcp.json.
func (s *mcpE2EState) startShotsServer() error {
	if err := s.makeHome(); err != nil {
		return err
	}
	k := &mcpShotsState{model: &mcpShotsProvider{}, events: map[string][]map[string]any{}}
	s.shots = k
	remote := mcptest.NewHTTPServer(mcpShotTokens["remote"])
	streamed := mcptest.NewEventStreamServer(mcpShotTokens["streamed"])
	legacy := mcptest.NewSSEServer(mcpShotTokens["legacy"])
	k.servers = []*mcptest.Server{remote, streamed, legacy}
	for _, srv := range []config.MCPServerConfig{
		mcptest.Stdio("native", mcpShotTokens["native"]),
		{Name: "remote", Type: "http", URL: remote.URL},
		{Name: "streamed", Type: "http", URL: streamed.URL},
		{Name: "legacy", Type: "sse", URL: legacy.URL},
	} {
		if err := config.UpsertMCPJSONServer(config.GlobalMCPJSONPath(s.home), srv.Name, config.MCPJSONFromServer(srv)); err != nil {
			return err
		}
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: s.home, CWD: s.cwd},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/eyes", MaxTokens: 200, MaxContextTokens: 200000, Multimodal: true}},
		Agent:     config.Agent{Model: "fake/eyes"},
	}
	s.serve(cfg, k.model)
	return nil
}

func (s *mcpE2EState) scriptedModelTakesEveryScreenshot() error {
	if s.shots == nil {
		return fmt.Errorf("the gateway with the screenshot servers is not running")
	}
	return nil
}

// sendStreamedPrompt runs the turn the way the web UI does, with stream on,
// and keeps the tool_call_update frames it reads.
func (s *mcpE2EState) sendStreamedPrompt() error {
	s.sid = "sess_mcp_shots_1"
	req, err := http.NewRequest(http.MethodPost, s.ts.URL+"/v1/responses",
		bytes.NewReader([]byte(`{"model":"agent","input":"take a screenshot with every server","stream":true}`)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", s.sid)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("POST /v1/responses status %d: %s", res.StatusCode, raw)
	}
	for _, frame := range strings.Split(string(raw), "\n\n") {
		event, data := "", ""
		for _, line := range strings.Split(frame, "\n") {
			switch {
			case strings.HasPrefix(line, "event:"):
				event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
		}
		if event != "tool_call_update" {
			continue
		}
		var update map[string]any
		if err := json.Unmarshal([]byte(data), &update); err != nil {
			return fmt.Errorf("a tool_call_update frame is not JSON: %v", err)
		}
		id, _ := update["toolCallId"].(string)
		s.shots.events[id] = append(s.shots.events[id], update)
	}
	return nil
}

// streamGivesEveryShotItsPicture: the completed update of every screenshot
// call names its picture with the addresses the web UI loads it from, the
// way a read's update names the picture it showed the model.
func (s *mcpE2EState) streamGivesEveryShotItsPicture() error {
	for _, name := range mcpShotServers {
		var images []any
		for _, u := range s.shots.events[mcpShotCallPrefix+name] {
			if u["status"] != "completed" {
				continue
			}
			meta, _ := u["_meta"].(map[string]any)
			coddy, _ := meta["coddy"].(map[string]any)
			images, _ = coddy["images"].([]any)
		}
		if len(images) != 1 {
			return fmt.Errorf("the completed update of %q names %d pictures, want one: %+v", name, len(images), s.shots.events[mcpShotCallPrefix+name])
		}
		img, _ := images[0].(map[string]any)
		url, _ := img["url"].(string)
		preview, _ := img["preview_url"].(string)
		if img["name"] != mcpShotName(name) || img["mime_type"] != "image/png" ||
			!strings.HasPrefix(url, "/coddy/sessions/"+s.sid+"/assets/") || preview != url+"/thumbnail" {
			return fmt.Errorf("the picture of %q on the stream is %+v", name, img)
		}
	}
	return nil
}

// modelSawEveryShot: the last request of the turn carries every screenshot,
// each in a user message right after its tool result, as the picture the
// server returned.
func (s *mcpE2EState) modelSawEveryShot() error {
	req := s.shots.model.lastRequest()
	for _, name := range mcpShotServers {
		at := -1
		for i, m := range req {
			if m.Role == llm.RoleTool && m.ToolCallID == mcpShotCallPrefix+name {
				at = i
			}
		}
		if at < 0 || at+1 >= len(req) {
			return fmt.Errorf("the last request has no result of the screenshot of %q", name)
		}
		if got := req[at].Content; got != mcpShotTokens[name] {
			return fmt.Errorf("the result of %q reads %q, want its token", name, got)
		}
		if len(req[at].ImageParts) != 0 {
			return fmt.Errorf("the result of %q carries its picture inside the tool message", name)
		}
		next := req[at+1]
		want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(mcpShotPicture(name))
		if next.Role != llm.RoleUser || len(next.ImageParts) != 1 || next.ImageParts[0].Name != mcpShotName(name) || next.ImageParts[0].DataURL != want {
			return fmt.Errorf("the message after the result of %q does not carry its screenshot: role %s, %d pictures", name, next.Role, len(next.ImageParts))
		}
	}
	return nil
}

// transcriptServesEveryShot: after a reload the transcript keeps every
// screenshot on its tool row, the full picture is the one the server returned
// and its preview is a PNG.
func (s *mcpE2EState) transcriptServesEveryShot() error {
	res, err := http.Get(s.ts.URL + "/coddy/sessions/" + s.sid + "/messages")
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	var body struct {
		Messages []struct {
			Role       string `json:"role"`
			ToolCallID string `json:"tool_call_id"`
			Files      []struct {
				Name       string `json:"name"`
				MIMEType   string `json:"mime_type"`
				URL        string `json:"url"`
				PreviewURL string `json:"preview_url"`
			} `json:"files"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return err
	}
	rows := map[string]int{}
	for i, m := range body.Messages {
		if m.Role == "tool" {
			rows[m.ToolCallID] = i
		}
		if m.Role == "user" && len(m.Files) > 0 {
			return fmt.Errorf("the transcript gained a user message with %d files nobody sent", len(m.Files))
		}
	}
	for _, name := range mcpShotServers {
		i, ok := rows[mcpShotCallPrefix+name]
		if !ok {
			return fmt.Errorf("the transcript has no row for the screenshot of %q", name)
		}
		files := body.Messages[i].Files
		if len(files) != 1 || files[0].Name != mcpShotName(name) || files[0].MIMEType != "image/png" {
			return fmt.Errorf("the row of %q carries %+v", name, files)
		}
		full, kind, err := s.fetch(files[0].URL)
		if err != nil {
			return err
		}
		if kind != "image/png" || !bytes.Equal(full, mcpShotPicture(name)) {
			return fmt.Errorf("%s serves %s of %d bytes, not the screenshot of %q", files[0].URL, kind, len(full), name)
		}
		if _, kind, err := s.fetch(files[0].PreviewURL); err != nil || kind != "image/png" {
			return fmt.Errorf("the preview of %q: %s, %v", name, kind, err)
		}
	}
	return nil
}

func (s *mcpE2EState) fetch(route string) ([]byte, string, error) {
	res, err := http.Get(s.ts.URL + route)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = res.Body.Close() }()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("GET %s: %s", route, res.Status)
	}
	return data, res.Header.Get("Content-Type"), nil
}

func (s *mcpE2EState) registerShotSteps(sc *godog.ScenarioContext) {
	sc.Step(`^a coddy HTTP server whose model reads images, with screenshot tools on MCP servers "native" run as a binary, "remote" over streamable http, "streamed" over streamable http answering with event streams and "legacy" over sse$`, s.startShotsServer)
	sc.Step(`^a scripted model that takes a screenshot with every MCP server in turn and then answers$`, s.scriptedModelTakesEveryScreenshot)
	sc.Step(`^I send an agent prompt over POST /v1/responses with streaming on$`, s.sendStreamedPrompt)
	sc.Step(`^the stream gives every screenshot call its picture for the web UI$`, s.streamGivesEveryShotItsPicture)
	sc.Step(`^the model saw every screenshot right after its tool result$`, s.modelSawEveryShot)
	sc.Step(`^the transcript keeps every screenshot on its tool row and serves the picture the server returned$`, s.transcriptServesEveryShot)
}
