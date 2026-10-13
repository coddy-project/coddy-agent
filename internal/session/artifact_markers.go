package session

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

var coddyFileMarker = regexp.MustCompile(`(?is)<coddy_file\s+id\s*=\s*(?:"[^"]*"|'[^']*')\s*/\s*>`)
var safeArtifactMarkerID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// StripArtifactMarkers removes UI-only placement markers before history leaves
// the server. The UI associates markers only with the artifacts persisted on
// the same assistant message; markers are never model instructions.
func StripArtifactMarkers(content string) string {
	return strings.TrimSpace(coddyFileMarker.ReplaceAllString(content, ""))
}

// StripAssistantContent is StripArtifactMarkers for the text of an assistant
// message that may stand at the join of a truncated message and its
// continuation: whitespace at the start of the text is kept when keepStart is
// set (the message goes on from a truncated one) and at the end when keepEnd is
// set (the message is itself truncated), since that whitespace is part of one
// text that is joined with nothing between. A text of nothing but whitespace
// is nothing.
func StripAssistantContent(content string, keepStart, keepEnd bool) string {
	s := coddyFileMarker.ReplaceAllString(content, "")
	core := strings.TrimSpace(s)
	if core == "" {
		return ""
	}
	if keepStart {
		core = s[:len(s)-len(strings.TrimLeftFunc(s, unicode.IsSpace))] + core
	}
	if keepEnd {
		core += s[len(strings.TrimRightFunc(s, unicode.IsSpace)):]
	}
	return core
}

// FollowsTruncated says whether an assistant message appended after msgs goes
// on from a truncated one: the last message that says anything is an assistant
// message marked Truncated. An assistant message with no text and no tool call
// is not in the way, a step that came back empty having nothing to join.
func FollowsTruncated(msgs []llm.Message) bool {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role == llm.RoleAssistant && strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 {
			continue
		}
		return m.Role == llm.RoleAssistant && m.Truncated
	}
	return false
}

// ContinuedAnswer returns the text of msgs[i], an assistant message, with the
// truncated messages it goes on from in front of it: the whole answer the
// reader watched stream in when the output limit cut it off and the model went
// on, where the transcript holds it as several messages. The parts are joined
// as stored, with nothing between, and an empty step between two of them is
// passed over as FollowsTruncated passes it. A message that goes on from
// nothing is returned as it is.
func ContinuedAnswer(msgs []llm.Message, i int) string {
	text := msgs[i].Content
	for j := i - 1; j >= 0; j-- {
		m := msgs[j]
		if m.Role == llm.RoleAssistant && strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 {
			continue
		}
		if m.Role != llm.RoleAssistant || !m.Truncated || len(m.ToolCalls) > 0 {
			break
		}
		text = m.Content + text
	}
	return text
}

// ArtifactMarkers renders placement markers for already-verified artifact
// associations. Callers must never pass client or model-supplied ids here.
func ArtifactMarkers(artifacts []llm.Artifact) string {
	markers := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		if safeArtifactMarkerID.MatchString(artifact.ID) {
			markers = append(markers, `<coddy_file id="`+artifact.ID+`"/>`)
		}
	}
	return strings.Join(markers, "\n")
}

func placePendingArtifacts(msgs []llm.Message, assistant *llm.Message) {
	if assistant.Role != llm.RoleAssistant {
		return
	}
	keepStart := FollowsTruncated(msgs)
	assistant.Content = StripAssistantContent(assistant.Content, keepStart, assistant.Truncated)
	// A truncated text and the message that goes on from it are one text,
	// joined with nothing between, so markers after it would stand inside that
	// text. A truncated message takes none: the message that completes the
	// answer takes them.
	if assistant.Content == "" || assistant.Truncated {
		return
	}
	placed := make(map[string]struct{})
	for _, msg := range msgs {
		if msg.Role != llm.RoleAssistant {
			continue
		}
		for _, artifact := range msg.Artifacts {
			if artifact.ID != "" {
				placed[artifact.ID] = struct{}{}
			}
		}
	}
	for _, artifact := range assistant.Artifacts {
		if artifact.ID != "" {
			placed[artifact.ID] = struct{}{}
		}
	}
	var pending []llm.Artifact
	for _, msg := range msgs {
		if msg.Role != llm.RoleTool {
			continue
		}
		for _, artifact := range msg.Artifacts {
			if artifact.ID == "" {
				continue
			}
			if _, ok := placed[artifact.ID]; ok {
				continue
			}
			placed[artifact.ID] = struct{}{}
			pending = append(pending, artifact)
		}
	}
	if len(pending) == 0 {
		return
	}
	assistant.Artifacts = append(assistant.Artifacts, pending...)
	// The markers close the text, so the whitespace it ended with goes; the
	// whitespace it began with, when it goes on from a truncated message, stays.
	start := ""
	if keepStart {
		start = assistant.Content[:len(assistant.Content)-len(strings.TrimLeftFunc(assistant.Content, unicode.IsSpace))]
	}
	assistant.Content = start + strings.TrimSpace(assistant.Content) + "\n\n" + ArtifactMarkers(pending)
}
