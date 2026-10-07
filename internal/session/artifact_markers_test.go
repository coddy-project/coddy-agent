package session

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestArtifactMarkersSkipsUnsafeIDs(t *testing.T) {
	markers := ArtifactMarkers([]llm.Artifact{
		{ID: "safe_123-abc"},
		{ID: `unsafe"/>`},
	})
	if markers != `<coddy_file id="safe_123-abc"/>` {
		t.Fatalf("markers = %q", markers)
	}
}
