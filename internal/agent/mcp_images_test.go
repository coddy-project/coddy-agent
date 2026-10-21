package agent

import (
	"os"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// runMCPImageTool calls one tool of the helper MCP server in a turn of a
// model that reads images and returns the call's result message.
func runMCPImageTool(t *testing.T, tool string) (*mcpImageFeatureState, llm.Message) {
	t.Helper()
	s := &mcpImageFeatureState{}
	if err := s.reset(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.close)
	if err := s.server(); err != nil {
		t.Fatal(err)
	}
	s.multimodal = true
	if err := s.run(true, tool); err != nil {
		t.Fatal(err)
	}
	for _, msg := range s.st.GetMessages() {
		if msg.Role == llm.RoleTool && msg.ToolCallID == "mcp1" {
			return s, msg
		}
	}
	t.Fatal("the MCP result was not kept")
	return nil, llm.Message{}
}

// The pictures of an MCP result are told by their content, as read tells a
// file, and named for what the model is sent: a PNG labelled image/jpeg goes
// as a PNG, a GIF as the PNG of its first frame, and an SVG, which no
// provider takes as a picture, is named in the result instead.
func TestMCPPicturesAreToldByTheirContent(t *testing.T) {
	_, msg := runMCPImageTool(t, "capture_formats")
	var got []string
	for _, p := range msg.ImageParts {
		got = append(got, p.Name+" "+p.MIMEType)
	}
	want := []string{
		"screens-capture_formats-1.png image/png",
		"screens-capture_formats-2.png image/png",
		"screens-capture_formats-3.jpg image/jpeg",
	}
	if strings.Join(got, ", ") != strings.Join(want, ", ") {
		t.Fatalf("pictures = %v, want %v", got, want)
	}
	if !strings.Contains(msg.Content, "[MCP image 4 omitted: not a PNG, JPEG, GIF or WebP image") {
		t.Fatalf("the result %q does not name the SVG it left out", msg.Content)
	}
}

// A result the server marked as an error fails the call, as any failed call
// it keeps no picture, and nothing is saved with the session's assets.
func TestAFailedMCPCallKeepsNoPicture(t *testing.T) {
	s, msg := runMCPImageTool(t, "capture_error")
	if len(msg.ImageParts) != 0 || !strings.Contains(msg.Content, "the page did not load") {
		t.Fatalf("the failed call's result = %+v", msg)
	}
	if entries, _ := os.ReadDir(session.AssetsPath(s.st.SessionDir)); len(entries) != 0 {
		t.Fatalf("a failed call saved %d assets", len(entries))
	}
}

// Every picture keeps the number of its place among the image blocks of the
// result, whatever was left out before it, so a note and a name never point
// at different blocks.
func TestMCPPicturesAreNumberedByTheirPlaceInTheResult(t *testing.T) {
	_, msg := runMCPImageTool(t, "capture_shift")
	if len(msg.ImageParts) != 1 || msg.ImageParts[0].Name != "screens-capture_shift-3.png" {
		t.Fatalf("pictures = %+v, want the third block as screens-capture_shift-3.png", msg.ImageParts)
	}
	if !strings.Contains(msg.Content, "[MCP image 1 omitted: the data is not base64]") || !strings.Contains(msg.Content, "[MCP image 2 omitted: not a PNG, JPEG, GIF or WebP image") {
		t.Fatalf("the result %q does not number what it left out by place", msg.Content)
	}
}
