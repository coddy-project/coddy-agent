package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestCaptureArtifactDeduplicatesAndRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	sessionDir := filepath.Join(root, "session")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(workspace, "report.txt")
	if err := os.WriteFile(file, []byte("immutable bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	first, err := CaptureArtifact(sessionDir, workspace, "report.txt")
	if err != nil {
		t.Fatal(err)
	}
	second, err := CaptureArtifact(sessionDir, workspace, "report.txt")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.SHA256 != second.SHA256 {
		t.Fatalf("dedup = %#v, %#v", first, second)
	}
	if first.SourcePath != file || first.SourceRelativePath != "report.txt" {
		t.Fatalf("source metadata = %#v, want absolute %q and relative report.txt", first, file)
	}
	if got, err := os.ReadFile(ArtifactPath(sessionDir, first.SHA256)); err != nil || string(got) != "immutable bytes" {
		t.Fatalf("artifact = %q, %v", got, err)
	}

	if err := os.Symlink(file, filepath.Join(workspace, "linked.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureArtifact(sessionDir, workspace, "linked.txt"); err == nil {
		t.Fatal("symlink source was accepted")
	}
}

func TestAssistantPlacesVerifiedArtifactAndKeepsToolFallbackUntilThen(t *testing.T) {
	artifact := llm.Artifact{ID: "artifact-1", Name: "report.txt"}
	st := &State{}
	st.AddMessage(llm.Message{Role: llm.RoleTool, ToolCallID: "share", Artifacts: []llm.Artifact{artifact}})
	if got := st.GetMessages()[0].Artifacts; len(got) != 1 || got[0].ID != artifact.ID {
		t.Fatalf("tool fallback = %#v", got)
	}

	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "The report is ready."})
	msgs := st.GetMessages()
	answer := msgs[1]
	if len(answer.Artifacts) != 1 || answer.Artifacts[0].ID != artifact.ID {
		t.Fatalf("assistant artifacts = %#v", answer.Artifacts)
	}
	if want := `<coddy_file id="artifact-1"/>`; answer.Content != "The report is ready.\n\n"+want {
		t.Fatalf("assistant content = %q, want server marker %q", answer.Content, want)
	}
}

func TestMarkerOnlyAssistantResponseKeepsArtifactFallback(t *testing.T) {
	artifact := llm.Artifact{ID: "artifact-1", Name: "report.txt"}
	st := &State{}
	st.AddMessage(llm.Message{Role: llm.RoleTool, ToolCallID: "share", Artifacts: []llm.Artifact{artifact}})
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: `<coddy_file id="hallucinated"/>`})
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "The real answer."})
	msgs := st.GetMessages()
	if len(msgs[1].Artifacts) != 0 || msgs[1].Content != "" {
		t.Fatalf("marker-only response became an artifact placement: %#v", msgs[1])
	}
	if len(msgs[2].Artifacts) != 1 || msgs[2].Artifacts[0].ID != artifact.ID {
		t.Fatalf("real answer did not place fallback artifact: %#v", msgs[2])
	}
}

func TestArtifactPlacementPersistsAcrossReload(t *testing.T) {
	root := t.TempDir()
	st := &State{ID: "sess_artifact_placement", CWD: root, SessionDir: filepath.Join(root, "sessions", "sess_artifact_placement")}
	if err := os.MkdirAll(st.SessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	artifact := llm.Artifact{ID: "artifact-1", Name: "report.txt", SourcePath: filepath.Join(root, "report.txt"), SourceRelativePath: "report.txt"}
	st.AddMessage(llm.Message{Role: llm.RoleTool, ToolCallID: "share", Artifacts: []llm.Artifact{artifact}})
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "Ready."})
	store := &FileStore{Root: filepath.Join(root, "sessions")}
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
	snap, err := store.ReadSnapshot(st.ID)
	if err != nil {
		t.Fatal(err)
	}
	answer := snap.Messages[1]
	if len(answer.Artifacts) != 1 || answer.Artifacts[0].SourceRelativePath != "report.txt" || !strings.Contains(answer.Content, `<coddy_file id="artifact-1"/>`) {
		t.Fatalf("reloaded artifact placement = %#v", answer)
	}
}

func TestArtifactSourcePathReportsRemovedSource(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(workspace, "report.txt")
	if err := os.WriteFile(file, []byte("report"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionDir := filepath.Join(root, "session")
	a, err := CaptureArtifact(sessionDir, workspace, "report.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if _, err := ArtifactSourcePath(sessionDir, workspace, a.ID); !errors.Is(err, ErrArtifactSourceUnavailable) {
		t.Fatalf("source error = %v, want unavailable", err)
	}
}

func TestReadArtifactRejectsTamperedAndSymlinkedContent(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	sessionDir := filepath.Join(root, "session")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "report.txt"), []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := CaptureArtifact(sessionDir, workspace, "report.txt")
	if err != nil {
		t.Fatal(err)
	}
	path := ArtifactPath(sessionDir, a.SHA256)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tampered"), 0o400); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadArtifact(sessionDir, a.ID); err != nil {
		t.Fatalf("ReadArtifact should leave digest verification to the HTTP stream: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(workspace, "report.txt"), path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadArtifact(sessionDir, a.ID); err == nil {
		t.Fatal("symlinked artifact was accepted")
	}
}
