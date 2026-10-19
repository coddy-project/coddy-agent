//go:build http

package httpserver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestLlmMsgsToCoddyOpenAIIncludesCreatedAt(t *testing.T) {
	out := llmMsgsToCoddyOpenAI([]llm.Message{
		{Role: llm.RoleUser, Content: "u", CreatedAt: "2026-05-01T12:00:00Z"},
		{Role: llm.RoleAssistant, Content: "a", CreatedAt: "2026-05-01T12:00:01Z"},
	})
	if len(out) != 2 {
		t.Fatalf("len=%d", len(out))
	}
	if got, _ := out[0]["created_at"].(string); got != "2026-05-01T12:00:00Z" {
		t.Fatalf("user created_at: %#v", out[0])
	}
	if got, _ := out[1]["created_at"].(string); got != "2026-05-01T12:00:01Z" {
		t.Fatalf("assistant created_at: %#v", out[1])
	}
}

func TestLlmMsgsToCoddyOpenAIOmitsEmptyCreatedAt(t *testing.T) {
	out := llmMsgsToCoddyOpenAI([]llm.Message{
		{Role: llm.RoleUser, Content: "u"},
	})
	if _, ok := out[0]["created_at"]; ok {
		t.Fatalf("expected no created_at, got %#v", out[0])
	}
}

func TestLlmMsgsToCoddyOpenAIForSessionIncludesPersistedFilePreview(t *testing.T) {
	assets := t.TempDir()
	thumb := session.ThumbnailPathInAssets(assets, "photo.png")
	if err := os.MkdirAll(filepath.Dir(thumb), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(thumb, []byte("png"), 0o444); err != nil {
		t.Fatal(err)
	}
	out := llmMsgsToCoddyOpenAIForSession("sess_files", assets, "", []llm.Message{
		{
			Role:    llm.RoleUser,
			Content: "look",
			ImageParts: []llm.ImagePart{{
				DataURL:       "data:image/png;base64,abc",
				Name:          "photo.png",
				FilePath:      filepath.Join(assets, "photo.png"),
				ThumbnailPath: thumb,
			}},
		},
	})
	files, ok := out[0]["files"].([]map[string]interface{})
	if !ok || len(files) != 1 {
		t.Fatalf("files: %#v", out[0]["files"])
	}
	if got := files[0]["name"]; got != "photo.png" {
		t.Fatalf("name = %#v", got)
	}
	if got := files[0]["mime_type"]; got != "image/png" {
		t.Fatalf("mime_type = %#v", got)
	}
	if got := files[0]["preview_url"]; got != "/coddy/sessions/sess_files/assets/photo.png/thumbnail" {
		t.Fatalf("preview_url = %#v", got)
	}
}

// A thumbnail recorded but no longer on disk gets no address: the card would
// load a 404 in its place. The original's address stays.
func TestLlmMsgsToCoddyOpenAIForSessionGivesNoPreviewOfAMissingThumbnail(t *testing.T) {
	assets := t.TempDir()
	original := filepath.Join(assets, "photo.png")
	if err := os.WriteFile(original, []byte("\x89PNG\r\n\x1a\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	out := llmMsgsToCoddyOpenAIForSession("sess_files", assets, "", []llm.Message{{
		Role:       llm.RoleTool,
		ToolCallID: "r1",
		Content:    "photo.png: PNG image",
		ImageParts: []llm.ImagePart{{Name: "photo.png", MIMEType: "image/png", FilePath: original, ThumbnailPath: session.ThumbnailPathInAssets(assets, "photo.png")}},
	}})
	files := out[0]["files"].([]map[string]interface{})
	if _, ok := files[0]["preview_url"]; ok {
		t.Errorf("a missing thumbnail was addressed: %#v", files[0])
	}
	if files[0]["url"] != "/coddy/sessions/sess_files/assets/photo.png" || files[0]["mime_type"] != "image/png" {
		t.Errorf("file = %#v, want the original's address and type", files[0])
	}
}
