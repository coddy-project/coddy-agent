package agent

import (
	"encoding/json"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// The web UI draws a shared file's card from _meta.coddy.artifacts of the
// final tool_call_update (consumeComposerSse.ts); a key anywhere else leaves
// the card to the next reload.
func TestSharedArtifactMetaIsWhereTheWebUIReadsIt(t *testing.T) {
	meta := sharedArtifactMeta(map[string]interface{}{"coddy": map[string]interface{}{"todoPlan": "kept"}}, "sess_1",
		llm.Artifact{ID: "a1", Name: "report.pdf", SHA256: "abc", Size: 3, SourcePath: "/w/report.pdf", SourceRelativePath: "report.pdf"})
	raw, _ := json.Marshal(meta)
	var wire struct {
		Artifacts any `json:"artifacts"`
		Coddy     struct {
			TodoPlan  string `json:"todoPlan"`
			Artifacts []struct {
				ID        string `json:"id"`
				URL       string `json:"url"`
				RevealURL string `json:"revealUrl"`
			} `json:"artifacts"`
		} `json:"coddy"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Artifacts != nil {
		t.Fatalf("artifacts at the top of _meta: %s", raw)
	}
	if len(wire.Coddy.Artifacts) != 1 || wire.Coddy.Artifacts[0].ID != "a1" || wire.Coddy.Artifacts[0].URL != "/coddy/sessions/sess_1/artifacts/a1" || wire.Coddy.Artifacts[0].RevealURL != "/coddy/sessions/sess_1/artifacts/a1/reveal" {
		t.Fatalf("coddy.artifacts %s", raw)
	}
	if wire.Coddy.TodoPlan != "kept" {
		t.Fatalf("the rest of coddy was lost: %s", raw)
	}
}
