package agent

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// Pictures a tool call hands the model (read or MCP) ride on that
// call's result message, the one place every surface already looks: the web
// UI previews them on the call's row, a Telegram chat is sent them, the
// console and an editor show the call's text. The transcript never gains a
// message nobody typed. Only what the provider is sent moves them into a user
// message of their own (withToolImages), because an OpenAI-compatible tool
// result cannot carry an image.

// modelReadsImages reports whether the session's current model is configured
// to accept images (models[].multimodal). A missing or unknown entry fails
// closed, the rule the HTTP surface applies to prompt attachments.
func (a *Agent) modelReadsImages() bool {
	entry := a.cfg.FindModelEntry(a.state.EffectiveModelID(a.cfg))
	return entry != nil && entry.Multimodal
}

// toolImageRefusal is Env.ImageRefusal: why the session's model cannot be
// shown a picture, or nil when it can.
func (a *Agent) toolImageRefusal() error {
	if !a.modelReadsImages() {
		return fmt.Errorf("the session's model %s does not read images (models[].multimodal is not set), so the picture cannot be shown to it",
			a.state.EffectiveModelID(a.cfg))
	}
	return nil
}

// attachToolImage is Env.AttachImage. The model is checked when the picture
// is handed over, not when the tools are listed: read is offered to every
// model, and switch_model can change the model in the middle of a turn. The
// copy with the session's assets is what the surfaces show, so a later change
// to the workspace file never changes what this call showed; a copy that
// cannot be written costs the surfaces their preview, never the model its
// picture.
func (a *Agent) attachToolImage(name, mimeType string, data []byte) error {
	if strings.TrimSpace(a.currentToolCallID) == "" {
		return fmt.Errorf("no tool call is running to attach the picture to")
	}
	if err := a.toolImageRefusal(); err != nil {
		return err
	}
	return a.recordToolImage(name, mimeType, data)
}

// recordToolImage keeps a picture on the running call. An MCP tool's picture
// goes through it directly, whatever the model: the tool returned it either
// way, so the transcript and the surfaces keep it for the operator and for a
// later switch to a model that reads images, while the projection sends a
// model that does not only its name.
func (a *Agent) recordToolImage(name, mimeType string, data []byte) error {
	if strings.TrimSpace(a.currentToolCallID) == "" {
		return fmt.Errorf("no tool call is running to attach the picture to")
	}
	part := llm.ImagePart{Name: name, MIMEType: mimeType, Size: len(data)}
	if sd := strings.TrimSpace(a.state.GetPersistedSessionDir()); sd != "" {
		asset, thumb, err := session.SaveToolImageAsset(sd, name, mimeType, data)
		if err != nil {
			a.log.Warn("the picture a tool call attached was not saved with the session's assets", "name", name, "err", err)
		}
		part.FilePath, part.ThumbnailPath = asset, thumb
	}
	if part.FilePath == "" {
		// Nothing to build the picture from later: it rides inline.
		part.DataURL = "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
	}
	a.callImages = append(a.callImages, part)
	return nil
}

// keepToolFile saves a file an MCP tool returned that no model is shown -
// an audio clip, a binary resource - with the session's assets and returns
// the line the call's result names it with: the model cannot look at it, but
// it can open the copy with its tools. A resource keeps the name of its URI;
// anything else is named after the server, the tool and its place.
func (a *Agent) keepToolFile(server, tool string, f mcp.ToolFile) string {
	name := f.Name
	if name == "" {
		name = fmt.Sprintf("%s-%s-%s-%d%s", server, tool, f.Kind, f.Index, toolFileExt[f.MIMEType])
	}
	facts := fmt.Sprintf("%d bytes", len(f.Data))
	if f.MIMEType != "" {
		facts = f.MIMEType + ", " + facts
	}
	path, err := session.SaveToolFileAsset(a.state.GetPersistedSessionDir(), name, f.Data)
	if err != nil {
		return fmt.Sprintf("[MCP %s %d omitted: %s (%s): %v]", f.Kind, f.Index, name, facts, err)
	}
	return fmt.Sprintf("[MCP %s %d: %s (%s), saved as %s]", f.Kind, f.Index, name, facts, path)
}

// toolFileExt names a file a tool returned with no name of its own by its
// type. The table is fixed, not the system's, so a name does not depend on
// the machine.
var toolFileExt = map[string]string{
	"audio/wav": ".wav", "audio/x-wav": ".wav", "audio/wave": ".wav",
	"audio/mpeg": ".mp3", "audio/mp3": ".mp3", "audio/ogg": ".ogg", "audio/webm": ".webm",
	"audio/flac": ".flac", "audio/aac": ".aac", "audio/mp4": ".m4a",
	"application/pdf": ".pdf", "application/json": ".json", "application/zip": ".zip",
	"text/plain": ".txt", "text/csv": ".csv",
}

// loadToolImage is the data URL of a picture kept as its copy with this
// session's assets, found by the copy's name, so a bundle that moved still
// has it. Only the copy that was saved is sent (session.ReadToolImageAsset):
// every request builds the same bytes, and a copy that is gone or changed is
// missing, which the model is told instead.
func (a *Agent) loadToolImage(p llm.ImagePart) (string, error) {
	sd := strings.TrimSpace(a.state.GetPersistedSessionDir())
	if sd == "" || strings.TrimSpace(p.FilePath) == "" {
		return "", fmt.Errorf("the picture %s has no saved copy", p.Name)
	}
	data, err := session.ReadToolImageAsset(sd, filepath.Base(p.FilePath), toolImagesMaxBytes)
	if err != nil {
		return "", err
	}
	mimeType := p.MIMEType
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

// partBytes is what a picture adds to a request: its data URL, or the one its
// file will make.
func partBytes(p llm.ImagePart) int {
	if p.DataURL != "" {
		return len(p.DataURL)
	}
	return len("data:"+p.MIMEType+";base64,") + base64.StdEncoding.EncodedLen(p.Size)
}

// callResultMessage is toolResultMessage for the call that just returned,
// carrying the pictures it attached. A failed call keeps none, and either way
// the pictures are taken, so they never reach the next call's result.
func (a *Agent) callResultMessage(tc llm.ToolCall, result string, execErr error, callRules string) llm.Message {
	msg := toolResultMessage(tc, result, execErr, callRules)
	if execErr == nil && tc.Name == "share_file" {
		if artifact, ok := sharedArtifact(a.state.GetPersistedSessionDir(), result); ok {
			msg.Artifacts = []llm.Artifact{artifact}
		}
	}
	images := a.callImages
	a.callImages = nil
	if execErr == nil && len(images) > 0 {
		msg.ImageParts = images
	}
	return msg
}

func sharedArtifact(sessionDir, result string) (llm.Artifact, bool) {
	var out struct {
		Artifact session.Artifact `json:"artifact"`
	}
	if json.Unmarshal([]byte(result), &out) != nil || out.Artifact.ID == "" {
		return llm.Artifact{}, false
	}
	a, _, err := session.ReadArtifact(sessionDir, out.Artifact.ID)
	if err != nil {
		return llm.Artifact{}, false
	}
	return llm.Artifact{ID: a.ID, Name: a.Name, SHA256: a.SHA256, Size: a.Size, SourcePath: a.SourcePath, SourceRelativePath: a.SourceRelativePath}, true
}

// sharedArtifactMeta puts a shared file on the _meta of share_file's final
// tool_call_update, under coddy.artifacts beside coddy.images, where the web
// UI reads it to draw the file's card while the turn runs; after a reload the
// card comes from the tool result row instead.
func sharedArtifactMeta(meta map[string]interface{}, sessionID string, artifact llm.Artifact) map[string]interface{} {
	if meta == nil {
		meta = map[string]interface{}{}
	}
	coddy, _ := meta["coddy"].(map[string]interface{})
	if coddy == nil {
		coddy = map[string]interface{}{}
		meta["coddy"] = coddy
	}
	route := session.ArtifactRoute(sessionID, artifact.ID)
	coddy["artifacts"] = []map[string]interface{}{{
		"id": artifact.ID, "name": artifact.Name, "sha256": artifact.SHA256,
		"size": artifact.Size, "sourcePath": artifact.SourcePath, "relativePath": artifact.SourceRelativePath,
		"url": route, "revealUrl": route + "/reveal",
		// Copied a moment ago: the file is where it was, and its card opens it.
		"sourceState": session.ArtifactSourcePresent,
	}}
	return meta
}

// toolImagesForSurfaces describes the pictures of a finished call for the
// _meta of its final tool_call_update. Only a picture saved with the
// session's assets is listed: nothing else has an address a surface can load.
func toolImagesForSurfaces(sessionID string, parts []llm.ImagePart) []session.ToolImage {
	var out []session.ToolImage
	for _, p := range parts {
		if strings.TrimSpace(p.FilePath) == "" {
			continue
		}
		asset := filepath.Base(p.FilePath)
		mimeType := p.MIMEType
		if mimeType == "" {
			mimeType = dataURLType(p.DataURL)
		}
		img := session.ToolImage{
			Name:     p.Name,
			MIMEType: mimeType,
			Asset:    asset,
			URL:      session.AssetRoute(sessionID, asset),
		}
		if strings.TrimSpace(p.ThumbnailPath) != "" {
			img.PreviewURL = session.AssetThumbnailRoute(sessionID, asset)
		}
		out = append(out, img)
	}
	return out
}

// dataURLType is the media type a data URL names.
func dataURLType(dataURL string) string {
	rest, ok := strings.CutPrefix(dataURL, "data:")
	if !ok {
		return ""
	}
	if end := strings.IndexAny(rest, ";,"); end >= 0 {
		return rest[:end]
	}
	return ""
}

const (
	// toolImagesMaxCount and toolImagesMaxBytes bound the pictures one request
	// carries (pictureWindowStart). Pictures stay in the history and go out
	// with every request, and a provider refuses a request past its limits:
	// the Anthropic API takes 32 MB in all, and pictures of at most 2000
	// pixels a side once a request holds more than 20 of them.
	toolImagesMaxCount = 20
	toolImagesMaxBytes = 20 << 20
)

// pictureKey addresses a part of the history: the message, the part.
type pictureKey [2]int

// selectPictures picks the pictures a request carries: every picture of the
// history, attached to a prompt or returned by a tool, from the oldest to
// the newest, in one window that ends at the newest picture
// (pictureWindowStart). Text files attached to a prompt are no pictures and
// take no place in it. A picture kept as its file is loaded as it is chosen,
// and one whose copy cannot be read is missing; with no loader (the context
// estimate) no file is opened and every copy is taken to be there. Nothing
// is chosen for a model that does not read images. The data URLs of the
// chosen pictures come back, keyed by where they are.
func selectPictures(msgs []llm.Message, readsImages bool, load func(llm.ImagePart) (string, error)) (kept map[pictureKey]string, missing map[pictureKey]bool) {
	kept, missing = map[pictureKey]string{}, map[pictureKey]bool{}
	if !readsImages {
		return kept, missing
	}
	var keys []pictureKey
	var sizes []int
	for i, m := range msgs {
		for j, p := range m.ImageParts {
			if llm.IsPicture(p) {
				keys = append(keys, pictureKey{i, j})
				sizes = append(sizes, partBytes(p))
			}
		}
	}
	for _, key := range keys[pictureWindowStart(sizes):] {
		p := msgs[key[0]].ImageParts[key[1]]
		url := p.DataURL
		if url == "" && load != nil {
			loaded, err := load(p)
			if err != nil {
				missing[key] = true
				continue
			}
			url = loaded
		}
		kept[key] = url
	}
	return kept, missing
}

// pictureWindowStart is the first of the pictures a request carries, given
// the size of every picture of the history from the oldest on. The window is
// a run of the newest pictures that grows with every new one until it holds
// more than toolImagesMaxCount pictures or toolImagesMaxBytes of them; then
// the oldest leave together, until half of each is left. Every request until
// the next overflow begins as the one before it did and only adds at its
// end, so the provider's prompt cache holds: a session of screenshots
// changes what was sent once every ten pictures or so, not with each one.
// The newest picture always stays, unless it alone is larger than a request
// takes. The walk depends on the sizes alone, never on which copies can be
// read, so every request of a history draws the same window.
func pictureWindowStart(sizes []int) int {
	start, count, total := 0, 0, 0
	for _, n := range sizes {
		count++
		total += n
		if count <= toolImagesMaxCount && total <= toolImagesMaxBytes {
			continue
		}
		for count > 1 && (count > toolImagesMaxCount/2 || total > toolImagesMaxBytes/2) {
			total -= sizes[start]
			count--
			start++
		}
		if total > toolImagesMaxBytes {
			total -= sizes[start]
			count--
			start++
		}
	}
	return start
}

// withToolImages returns msgs as the provider is sent them: the pictures the
// tool results carry move into one user message right after each run of tool
// results. A tool message cannot hold an image in the OpenAI-compatible
// schema, and a user message between the results of one step would break the
// assistant(tool_calls) -> tool results adjacency strict endpoints require.
// Only the pictures selectPictures chooses go out, prompt attachments
// included; a picture left out is named where it was, in the note of its
// step or in its own prompt. Built from the history alone, the projection is
// the same bytes on every request, so the provider's prompt cache holds until
// a new picture pushes an old one out.
//
// A model that does not read images - the session switched to one after the
// read - is sent no picture at all, and is told which pictures the steps and
// the prompts came with; the text files of a prompt still go. The input is
// never written.
func withToolImages(msgs []llm.Message, readsImages bool, load func(llm.ImagePart) (string, error)) []llm.Message {
	msgs = withoutArtifacts(msgs)
	kept, missing := selectPictures(msgs, readsImages, load)
	if untouched(msgs, readsImages, kept) {
		return msgs
	}
	out := make([]llm.Message, 0, len(msgs)+2)
	var pending []llm.ImagePart
	var names, omitted, gone []string
	flush := func() {
		if len(names) == 0 && len(omitted) == 0 && len(gone) == 0 {
			return
		}
		out = append(out, toolImagesMessage(pending, names, omitted, gone, readsImages))
		pending, names, omitted, gone = nil, nil, nil, nil
	}
	for i, m := range msgs {
		if m.Role != llm.RoleTool {
			flush()
			if len(m.ImageParts) > 0 {
				var parts, left []llm.ImagePart
				for j, p := range m.ImageParts {
					switch url, ok := kept[pictureKey{i, j}]; {
					case !llm.IsPicture(p):
						parts = append(parts, p)
					case ok:
						p.DataURL = url
						parts = append(parts, p)
					default:
						left = append(left, p)
					}
				}
				if len(left) > 0 {
					m.Content = withAttachmentsLeftOut(m.Content, left, readsImages)
				}
				m.ImageParts = parts
			}
			out = append(out, m)
			continue
		}
		for j, p := range m.ImageParts {
			ref := fmt.Sprintf("%s (from call %s)", p.Name, m.ToolCallID)
			url, ok := kept[pictureKey{i, j}]
			switch {
			case ok:
				p.DataURL = url
				pending = append(pending, p)
				names = append(names, ref)
			case missing[pictureKey{i, j}]:
				gone = append(gone, ref)
			default:
				omitted = append(omitted, ref)
			}
		}
		m.ImageParts = nil
		out = append(out, m)
	}
	flush()
	return out
}

// withoutArtifacts removes surface-only download metadata from the provider
// projection without rewriting the persisted transcript.
func withoutArtifacts(msgs []llm.Message) []llm.Message {
	for i := range msgs {
		if len(msgs[i].Artifacts) == 0 {
			continue
		}
		out := append([]llm.Message(nil), msgs...)
		for i := range out {
			out[i].Artifacts = nil
		}
		return out
	}
	return msgs
}

// untouched reports whether the projection would leave msgs as they are: no
// tool result carries a picture, and every picture of the prompts goes out as
// it is stored.
func untouched(msgs []llm.Message, readsImages bool, kept map[pictureKey]string) bool {
	for i, m := range msgs {
		for j, p := range m.ImageParts {
			if m.Role == llm.RoleTool {
				return false
			}
			if !llm.IsPicture(p) {
				continue
			}
			if url, ok := kept[pictureKey{i, j}]; !readsImages || !ok || url != p.DataURL {
				return false
			}
		}
	}
	return true
}

// withAttachmentsLeftOut names in a prompt the pictures attached to it that
// the request leaves out, so the model does not answer as if the prompt were
// text alone: all of them for a model that cannot take pictures, the older
// ones past the request budget otherwise.
func withAttachmentsLeftOut(content string, parts []llm.ImagePart, readsImages bool) string {
	names := make([]string, 0, len(parts))
	for _, p := range parts {
		name := p.Name
		if name == "" {
			name = filepath.Base(p.FilePath)
		}
		names = append(names, name)
	}
	why := "pictures the current model cannot be shown"
	if readsImages {
		why = "pictures left out of this request to keep it within what the provider takes"
	}
	return strings.TrimRight(content, "\n") + "\n\n[This message came with " + why + ": " + strings.Join(names, ", ") + ".]"
}

// toolImagesMessage is the user message that carries the pictures of one
// step to the provider and names the ones left out of the request, or tells a
// model that cannot take pictures what it is not shown.
func toolImagesMessage(parts []llm.ImagePart, names, omitted, missing []string, readsImages bool) llm.Message {
	if !readsImages {
		return llm.Message{
			Role: llm.RoleUser,
			Content: "The tool calls above returned pictures the current model cannot be shown: " +
				strings.Join(omitted, ", ") + ".",
		}
	}
	var b strings.Builder
	if len(names) > 0 {
		b.WriteString("The pictures the tool calls above returned, in order:\n- ")
		b.WriteString(strings.Join(names, "\n- "))
	}
	if len(omitted) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("Pictures the tool calls above returned, left out of this request to keep it within what the provider takes; read the file again or repeat the call to see one: ")
		b.WriteString(strings.Join(omitted, ", "))
		b.WriteString(".")
	}
	if len(missing) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("Pictures the tool calls above returned whose saved copy is gone; read the file again or repeat the call to see one: ")
		b.WriteString(strings.Join(missing, ", "))
		b.WriteString(".")
	}
	return llm.Message{Role: llm.RoleUser, Content: b.String(), ImageParts: parts}
}

// imageTokensEach is what one picture counts for in the context estimate:
// about what a provider charges for a screenshot-sized image (the Anthropic
// API: the pixels over 750, about 1.2 megapixels once it resizes a picture).
const imageTokensEach = 1600

// conversationTokens estimates the conversation as the provider reads it: its
// text, the reasoning and tool calls a message spends beyond its content, and
// the pictures a request carries, which the text leaves out - the ones
// selectPictures chooses, without opening a file. Result eviction, automatic
// compaction and the context ring measure with it.
func conversationTokens(msgs []llm.Message, readsImages bool) int {
	kept, _ := selectPictures(msgs, readsImages, nil)
	total := session.EstimateContextTokens(conversationText(msgs)) + len(kept)*imageTokensEach
	for _, m := range msgs {
		total += session.EstimateContextTokens(m.Reasoning)
		for _, call := range m.ToolCalls {
			total += session.EstimateContextTokens(call.Name) + session.EstimateContextTokens(call.InputJSON)
		}
	}
	return total
}
