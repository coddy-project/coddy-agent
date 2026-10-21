package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/jpeg"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// TestHelperMCPImageServer re-executes the test binary as a tiny stdio MCP server.
func TestHelperMCPImageServer(t *testing.T) {
	if os.Getenv("CODDY_TEST_MCP_IMAGE_SERVER") != "1" {
		t.Skip("helper process")
	}
	reader := bufio.NewScanner(os.Stdin)
	for reader.Scan() {
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		if json.Unmarshal(reader.Bytes(), &req) != nil || req.ID == nil {
			continue
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "serverInfo": map[string]string{"name": "screens", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{
				map[string]any{"name": "capture", "description": "Screenshot", "inputSchema": map[string]any{"type": "object"}},
				map[string]any{"name": "capture_only", "description": "Image only", "inputSchema": map[string]any{"type": "object"}},
				map[string]any{"name": "capture_broken", "description": "Damaged screenshot", "inputSchema": map[string]any{"type": "object"}},
				map[string]any{"name": "capture_formats", "description": "Pictures of every kind", "inputSchema": map[string]any{"type": "object"}},
				map[string]any{"name": "capture_error", "description": "A failed capture", "inputSchema": map[string]any{"type": "object"}},
				map[string]any{"name": "capture_shift", "description": "Pictures after refused ones", "inputSchema": map[string]any{"type": "object"}},
			}}
		case "tools/call":
			if content, isError, ok := mcpImageHelperCall(req.Params.Name); ok {
				result = map[string]any{"content": content, "isError": isError}
				break
			}
			picture := testPNG(2, 2, color.NRGBA{R: 200, A: 255})
			if req.Params.Name == "capture_broken" {
				picture = picture[:len(picture)-10]
			}
			content := []any{map[string]any{"type": "image", "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(picture)}}
			if req.Params.Name != "capture_only" {
				content = append([]any{map[string]any{"type": "text", "text": "Captured screen"}}, content...)
			}
			result = map[string]any{"content": content}
		}
		response, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		_, _ = os.Stdout.Write(append(response, '\n'))
	}
	os.Exit(0)
}

// mcpImageHelperCall answers the helper's tools beyond the screenshots of
// the feature: capture_formats returns a PNG labelled image/jpeg, a GIF, a
// JPEG and an SVG; capture_error is a failed call that carries a picture.
func mcpImageHelperCall(name string) (content []any, isError bool, ok bool) {
	block := func(mimeType string, data []byte) map[string]any {
		return map[string]any{"type": "image", "mimeType": mimeType, "data": base64.StdEncoding.EncodeToString(data)}
	}
	switch name {
	case "capture_formats":
		var g, j bytes.Buffer
		frame := image.NewPaletted(image.Rect(0, 0, 3, 2), palette.Plan9)
		_ = gif.Encode(&g, frame, nil)
		_ = jpeg.Encode(&j, image.NewNRGBA(image.Rect(0, 0, 3, 2)), nil)
		return []any{
			block("image/jpeg", testPNG(2, 2, color.NRGBA{B: 200, A: 255})),
			block("image/gif", g.Bytes()),
			block("image/jpeg", j.Bytes()),
			block("image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)),
		}, false, true
	case "capture_shift":
		return []any{
			map[string]any{"type": "image", "mimeType": "image/png", "data": "not base64!"},
			block("image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)),
			block("image/png", testPNG(2, 2, color.NRGBA{G: 200, A: 255})),
		}, false, true
	case "capture_error":
		return []any{
			map[string]any{"type": "text", "text": "the page did not load"},
			block("image/png", testPNG(2, 2, color.NRGBA{R: 200, A: 255})),
		}, true, true
	}
	return nil, false, false
}

type mcpImageFeatureState struct {
	readImageFeatureState
	client *mcp.Client
}

func (s *mcpImageFeatureState) reset() error {
	s.close()
	return s.readImageFeatureState.reset()
}

func (s *mcpImageFeatureState) close() {
	if s.client != nil {
		_ = s.client.Close()
		s.client = nil
	}
	s.readImageFeatureState.close()
}

func (s *mcpImageFeatureState) server() error {
	client, err := mcp.NewStdioClient(context.Background(), "screens", os.Args[0], []string{"-test.run=TestHelperMCPImageServer"}, []string{"CODDY_TEST_MCP_IMAGE_SERVER=1"}, slog.Default())
	if err != nil {
		return err
	}
	s.client = client
	return nil
}

func (s *mcpImageFeatureState) run(multimodal bool, toolName string) error {
	s.multimodal = multimodal
	a := s.agent()
	s.st.AddSessionMCPClient(s.client)
	s.provider.steps = []evStep{
		{calls: []llm.ToolCall{{ID: "mcp1", Name: "screens__" + toolName, InputJSON: `{}`}}},
		{text: "answer"},
	}
	_, err := a.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "capture"}})
	return err
}

func (s *mcpImageFeatureState) runMultimodal() error { return s.run(true, "capture") }
func (s *mcpImageFeatureState) runTextOnly() error   { return s.run(false, "capture") }
func (s *mcpImageFeatureState) runDamaged() error    { return s.run(true, "capture_broken") }
func (s *mcpImageFeatureState) runImageOnly() error  { return s.run(true, "capture_only") }

func (s *mcpImageFeatureState) imageOnlyResult() error {
	for _, msg := range s.st.GetMessages() {
		if msg.Role == llm.RoleTool && msg.ToolCallID == "mcp1" {
			if msg.Content != "[MCP image 1: screens-capture_only-1.png]" || len(msg.ImageParts) != 1 || msg.ImageParts[0].Name != "screens-capture_only-1.png" {
				return fmt.Errorf("image-only MCP result = %+v", msg)
			}
			return nil
		}
	}
	return fmt.Errorf("image-only MCP result not persisted")
}

func (s *mcpImageFeatureState) damagedResult() error {
	for _, msg := range s.st.GetMessages() {
		if msg.Role == llm.RoleTool && msg.ToolCallID == "mcp1" {
			if !strings.Contains(msg.Content, "Captured screen") || !strings.Contains(msg.Content, "MCP image 1 omitted") || len(msg.ImageParts) != 0 {
				return fmt.Errorf("damaged MCP result = %+v", msg)
			}
			return nil
		}
	}
	return fmt.Errorf("damaged MCP result not persisted")
}

func (s *mcpImageFeatureState) noDamagedProjection() error {
	tail, err := s.requestTail()
	if err != nil {
		return err
	}
	if len(tail) != 2 || tail[1].Role != llm.RoleTool || len(tail[1].ImageParts) != 0 {
		return fmt.Errorf("damaged provider tail = %+v", tail)
	}
	return nil
}

func (s *mcpImageFeatureState) resultImage() (llm.ImagePart, error) {
	for _, msg := range s.st.GetMessages() {
		if msg.Role == llm.RoleTool && msg.ToolCallID == "mcp1" {
			if msg.Content != "Captured screen" || len(msg.ImageParts) != 1 {
				return llm.ImagePart{}, fmt.Errorf("MCP result: text %q, images %d", msg.Content, len(msg.ImageParts))
			}
			return msg.ImageParts[0], nil
		}
	}
	return llm.ImagePart{}, fmt.Errorf("MCP result not persisted")
}

func (s *mcpImageFeatureState) savedResult() error {
	part, err := s.resultImage()
	if err != nil {
		return err
	}
	if part.Name != "screens-capture-1.png" || part.FilePath == "" {
		return fmt.Errorf("MCP image = %+v", part)
	}
	data, err := session.ReadToolImageAsset(s.st.SessionDir, strings.TrimPrefix(part.FilePath, session.AssetsPath(s.st.SessionDir)+string(os.PathSeparator)), toolImagesMaxBytes)
	if err != nil {
		return err
	}
	want := testPNG(2, 2, color.NRGBA{R: 200, A: 255})
	if !bytes.Equal(data, want) {
		return fmt.Errorf("saved image changed")
	}
	return nil
}

func (s *mcpImageFeatureState) projectedImage() error {
	tail, err := s.requestTail()
	if err != nil {
		return err
	}
	if len(tail) != 3 || tail[1].Role != llm.RoleTool || len(tail[1].ImageParts) != 0 || tail[2].Role != llm.RoleUser || len(tail[2].ImageParts) != 1 || !strings.HasPrefix(tail[2].ImageParts[0].DataURL, "data:image/png;base64,") {
		return fmt.Errorf("provider tail = %+v", tail)
	}
	return nil
}

func (s *mcpImageFeatureState) textOnlyProjection() error {
	tail, err := s.requestTail()
	if err != nil {
		return err
	}
	if len(tail) != 3 || len(tail[2].ImageParts) != 0 || !strings.Contains(tail[2].Content, "screens-capture-1.png") {
		return fmt.Errorf("text-only provider tail = %+v", tail)
	}
	return nil
}

func (s *mcpImageFeatureState) surfaceImage() error {
	update := s.sender.final("mcp1")
	if update == nil {
		return fmt.Errorf("no completed MCP update")
	}
	images := session.ToolImagesFromMeta(update.Meta)
	if len(images) != 1 || images[0].Name != "screens-capture-1.png" || images[0].URL == "" {
		return fmt.Errorf("MCP update images = %+v", images)
	}
	return nil
}

func TestMCPToolImagesFeature(t *testing.T) {
	s := &mcpImageFeatureState{}
	suite := godog.TestSuite{
		Name: "mcp-tool-images",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) { return ctx, s.reset() })
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				s.close()
				return ctx, nil
			})
			sc.Step(`^an MCP server that returns a screenshot$`, s.server)
			sc.Step(`^the session model reads MCP images$`, s.modelReadsImages)
			sc.Step(`^the model calls the MCP screenshot tool and answers$`, s.runMultimodal)
			sc.Step(`^the model calls the image-only MCP tool and answers$`, s.runImageOnly)
			sc.Step(`^the text-only model calls the MCP screenshot tool and answers$`, s.runTextOnly)
			sc.Step(`^the model calls the damaged MCP screenshot tool and answers$`, s.runDamaged)
			sc.Step(`^the MCP result keeps its screenshot in the transcript and session assets$`, s.savedResult)
			sc.Step(`^the image-only result keeps its screenshot in the transcript$`, s.imageOnlyResult)
			sc.Step(`^the next model request carries the screenshot after the MCP result$`, s.projectedImage)
			sc.Step(`^the next model request names the screenshot without image data$`, s.textOnlyProjection)
			sc.Step(`^the MCP tool update gives the surfaces the screenshot$`, s.surfaceImage)
			sc.Step(`^the MCP result keeps its text and reports the omitted image$`, s.damagedResult)
			sc.Step(`^the next model request has no damaged image data$`, s.noDamagedProjection)
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/mcp_tool_images.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("MCP image feature failed")
	}
}
