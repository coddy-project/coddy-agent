package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// ToolSessionDescribe is the tool a model calls to file its own conversation.
const ToolSessionDescribe = "session_describe"

// SessionDescribeTool lets the model keep the session's own filing honest. The
// title and the tags are written once, when the first message names a new chat,
// and a long conversation walks away from both: the model is the one that can
// see it has, and a call costs less than the operator noticing weeks later that
// nothing in History says what anything was about.
func SessionDescribeTool() *tooling.Tool {
	stringList := func(description string) map[string]interface{} {
		return map[string]interface{}{
			"type":        "array",
			"items":       map[string]interface{}{"type": "string"},
			"description": description,
		}
	}
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolSessionDescribe,
			Description: "Read or change how this conversation is filed in the session list: the title it is listed under and the tags it is grouped by. " +
				"With no arguments it only reports the current filing. Call it when the conversation has moved on from the title it was given, " +
				"when a new topic deserves a label, or when the user asks to rename or tag the session. " +
				"Tags are stored folded: lower case, inner spaces as hyphens, duplicates dropped, at most 8 per session and 32 characters each - " +
				"the answer reports what was actually stored, which is what a filter has to be asked for.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"title": map[string]interface{}{
						"type": "string",
						"description": "New title for the session, a short phrase naming the work. " +
							"An empty string drops a pinned title and goes back to the one derived from the first message.",
					},
					"tags": stringList("Replaces every tag with this set; an empty array clears them. Use add_tags/remove_tags to change a few instead."),
					"add_tags": stringList("Tags to file the session under, keeping the ones it already has. " +
						"Additions past the per-session cap are dropped, so the labels already there survive."),
					"remove_tags": stringList("Tags to take off the session, named in any spelling: they are matched after the same folding."),
				},
			},
		},
		RequiresPermission: false,
		Execute:            executeSessionDescribe,
	}
}

func executeSessionDescribe(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	if env == nil || env.FileSession == nil {
		return "", fmt.Errorf("%s is not available in this runtime", ToolSessionDescribe)
	}
	var args struct {
		Title      *string   `json:"title"`
		Tags       *[]string `json:"tags"`
		AddTags    []string  `json:"add_tags"`
		RemoveTags []string  `json:"remove_tags"`
	}
	if s := strings.TrimSpace(argsJSON); s != "" && s != "{}" {
		if err := json.Unmarshal([]byte(s), &args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	// Replacing the set and editing it are two different intentions, and a call
	// carrying both leaves the order between them to whoever reads the code.
	if args.Tags != nil && (len(args.AddTags) > 0 || len(args.RemoveTags) > 0) {
		return "", fmt.Errorf("pass either tags (the whole set) or add_tags/remove_tags (a change to it), not both")
	}

	// One call, whether this is a read or a write: an update naming nothing
	// writes nothing. The session reports what it moved, so a tag the folding
	// dropped and a title that was already the stored one both come back as no
	// change - which is what the model needs to see to stop asking for it again.
	result, err := env.FileSession(tooling.SessionFilingUpdate{
		Title:      args.Title,
		Tags:       args.Tags,
		AddTags:    args.AddTags,
		RemoveTags: args.RemoveTags,
	})
	if err != nil {
		return "", err
	}

	changed := result.Changed
	if changed == nil {
		changed = []string{}
	}
	tags := result.Filing.Tags
	if tags == nil {
		tags = []string{}
	}
	out, err := json.Marshal(map[string]interface{}{
		"object":  "session.filing",
		"title":   result.Filing.Title,
		"tags":    tags,
		"changed": changed,
	})
	if err != nil {
		return "", err
	}
	return string(out), nil
}
