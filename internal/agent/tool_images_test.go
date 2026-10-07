package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func imagePart(name string) llm.ImagePart {
	return llm.ImagePart{DataURL: "data:image/png;base64,AAAA" + name, Name: name}
}

func toolResultWith(id string, images ...llm.ImagePart) llm.Message {
	return llm.Message{Role: llm.RoleTool, ToolCallID: id, Content: "result of " + id, ImageParts: images}
}

func TestWithToolImagesMovesThePicturesAfterEachRunOfResults(t *testing.T) {
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "look"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "r1", Name: "read"}, {ID: "g1", Name: "glob"}, {ID: "r2", Name: "read"}}},
		toolResultWith("r1", imagePart("a.png")),
		toolResultWith("g1"),
		toolResultWith("r2", imagePart("b.png")),
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "r3", Name: "read"}}},
		toolResultWith("r3", imagePart("c.png")),
		{Role: llm.RoleAssistant, Content: "seen"},
	}
	before, _ := json.Marshal(history)

	out := withToolImages(history, true, noToolImageFile)

	var roles []string
	for _, m := range out {
		roles = append(roles, string(m.Role))
	}
	want := "user,assistant,tool,tool,tool,user,assistant,tool,user,assistant"
	if strings.Join(roles, ",") != want {
		t.Fatalf("roles = %s, want %s", strings.Join(roles, ","), want)
	}
	for i, m := range out {
		if m.Role == llm.RoleTool && len(m.ImageParts) > 0 {
			t.Errorf("message %d: a tool result still carries %d image(s)", i, len(m.ImageParts))
		}
	}
	if got := imageNames(out[5]); strings.Join(got, ",") != "a.png,b.png" {
		t.Errorf("first step's pictures = %v, want [a.png b.png]", got)
	}
	if got := imageNames(out[8]); strings.Join(got, ",") != "c.png" {
		t.Errorf("second step's pictures = %v, want [c.png]", got)
	}
	for _, name := range []string{"a.png", "r1", "b.png", "r2"} {
		if !strings.Contains(out[5].Content, name) {
			t.Errorf("the pictures message %q does not name %s", out[5].Content, name)
		}
	}
	if after, _ := json.Marshal(history); string(after) != string(before) {
		t.Error("the history itself was changed")
	}
	// Built from the history alone: every request replays it byte for byte.
	again, _ := json.Marshal(withToolImages(history, true, noToolImageFile))
	if first, _ := json.Marshal(out); string(first) != string(again) {
		t.Error("two requests over the same history differ")
	}
}

func TestWithToolImagesLeavesAHistoryWithoutPicturesAlone(t *testing.T) {
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "look", ImageParts: []llm.ImagePart{imagePart("attached.png")}},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "g1", Name: "glob"}}},
		toolResultWith("g1"),
	}
	out := withToolImages(history, true, noToolImageFile)
	if len(out) != len(history) || &out[0] != &history[0] {
		t.Error("a history whose tool results carry no picture was copied")
	}
}

func TestWithToolImagesTellsAModelWithoutImagesThePicturesAreLeftOut(t *testing.T) {
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "look", ImageParts: []llm.ImagePart{imagePart("attached.png")}},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "r1", Name: "read"}}},
		toolResultWith("r1", imagePart("a.png")),
		{Role: llm.RoleAssistant, Content: "seen"},
	}
	before, _ := json.Marshal(history)
	out := withToolImages(history, false, noToolImageFile)
	for i, m := range out {
		if len(m.ImageParts) > 0 {
			t.Errorf("message %d (%s) still carries %d image(s) for a model that does not read them", i, m.Role, len(m.ImageParts))
		}
	}
	if len(out) != 5 || out[3].Role != llm.RoleUser || !strings.Contains(out[3].Content, "a.png") {
		t.Fatalf("no note after the results names the picture left out: %+v", out)
	}
	if !strings.HasPrefix(out[0].Content, "look") || !strings.Contains(out[0].Content, "attached.png") {
		t.Errorf("the prompt %q does not say its attachment is not shown", out[0].Content)
	}
	if len(history[0].ImageParts) != 1 || len(history[2].ImageParts) != 1 {
		t.Error("the history itself lost its pictures")
	}
	// The notes are written into the projection, never into the history, so
	// the next request carries each of them once, byte for byte the same.
	if after, _ := json.Marshal(history); string(after) != string(before) {
		t.Error("the history itself was changed")
	}
	again, _ := json.Marshal(withToolImages(history, false, noToolImageFile))
	if first, _ := json.Marshal(out); string(first) != string(again) {
		t.Error("two requests over the same history differ")
	}
}

func TestCallResultMessageKeepsThePicturesOfASuccessfulCallOnly(t *testing.T) {
	a := &Agent{}
	tc := llm.ToolCall{ID: "r1", Name: "read"}

	a.callImages = []llm.ImagePart{imagePart("a.png")}
	if msg := a.callResultMessage(tc, "ok", nil, ""); len(msg.ImageParts) != 1 {
		t.Errorf("a successful call's result carries %d image(s), want 1", len(msg.ImageParts))
	}
	if len(a.callImages) != 0 {
		t.Error("the pictures were not taken off the call")
	}

	a.callImages = []llm.ImagePart{imagePart("a.png")}
	if msg := a.callResultMessage(tc, "", errors.New("boom"), ""); len(msg.ImageParts) != 0 {
		t.Errorf("a failed call's result carries %d image(s)", len(msg.ImageParts))
	}
	if len(a.callImages) != 0 {
		t.Error("a failed call left its pictures for the next one")
	}
}

func TestEvictionDropsThePictureOfAReadItCollapses(t *testing.T) {
	cwd := t.TempDir()
	read := func(id, path string) llm.ToolCall {
		b, _ := json.Marshal(map[string]string{"path": path})
		return llm.ToolCall{ID: id, Name: "read", InputJSON: string(b)}
	}
	big := imagePart("old.png")
	big.DataURL = "data:image/png;base64," + strings.Repeat("A", 4096)
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "look"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{read("r1", "old.png")}},
		// The text of an image read is a line; the picture is what fills the context.
		{Role: llm.RoleTool, ToolCallID: "r1", Content: "old.png: PNG image", ImageParts: []llm.ImagePart{big}},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{read("r2", "new.txt")}},
		{Role: llm.RoleTool, ToolCallID: "r2", Content: strings.Repeat("x", 4096)},
	}
	out := pruneToolResults(history, resultEvictionOptions{Enabled: true, KeepRecent: 1, MinResultBytes: 1024, CWD: cwd})
	if !strings.HasPrefix(out[2].Content, "[evicted:") {
		t.Fatalf("the image read was not evicted: %q", out[2].Content)
	}
	if len(out[2].ImageParts) != 0 {
		t.Error("the evicted read still carries its picture")
	}
	if len(history[2].ImageParts) != 1 {
		t.Error("eviction changed the history itself")
	}
}

func TestReadOfAnImageIsRefusedForAModelThatDoesNotReadImages(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "shot.png"), testPNG(4, 3, color.Black), 0o644); err != nil {
		t.Fatal(err)
	}
	// Past the size one picture may take: the model is still told first that
	// it cannot see pictures, not how to make this one smaller.
	if err := os.WriteFile(filepath.Join(dir, "huge.png"), testPNG(4, 3, color.Black), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(filepath.Join(dir, "huge.png"), 8<<20); err != nil {
		t.Fatal(err)
	}
	sessionDir := filepath.Join(dir, ".session")
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/text", MaxTokens: 100, MaxContextTokens: 128000}},
		Agent:     config.Agent{Model: "fake/text"},
		Tools:     config.Tools{PermissionMode: config.PermModeBypass},
	}
	st := &session.State{ID: "sess_text_model", CWD: dir, Mode: session.ModeAgent, SessionDir: sessionDir}
	provider := &evScriptProvider{steps: []evStep{{calls: []llm.ToolCall{tcReadPath("r1", "shot.png"), tcReadPath("r2", "huge.png")}}, {text: "answer"}}}
	ag := NewAgent(cfg, st, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }
	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "look"}}); err != nil {
		t.Fatal(err)
	}

	last := provider.streamSeen[len(provider.streamSeen)-1]
	results := map[string]string{}
	for _, m := range last {
		if len(m.ImageParts) > 0 {
			t.Errorf("a %s message carries %d image(s) for a model that does not read them", m.Role, len(m.ImageParts))
		}
		if m.Role == llm.RoleTool {
			results[m.ToolCallID] = m.Content
		}
	}
	for _, id := range []string{"r1", "r2"} {
		if result := results[id]; !strings.Contains(result, "does not read images") || !strings.Contains(result, "fake/text") {
			t.Errorf("tool result %s %q does not say the model fake/text does not read images", id, result)
		}
	}
	if entries, _ := os.ReadDir(session.AssetsPath(sessionDir)); len(entries) > 0 {
		t.Errorf("a refused picture was saved with the assets: %v", entries)
	}
}

// Pictures stay in the history and go out with every request. A request
// carries only the newest of them, at most toolImagesMaxCount and
// toolImagesMaxBytes of data, so a session that read many screenshots keeps
// fitting what a provider takes (Anthropic: 32 MB a request, and 2000 pixels a
// side once it holds more than 20 pictures); the older ones are named as left
// out.
func TestWithToolImagesSendsOnlyTheNewestPicturesARequestCanHold(t *testing.T) {
	var history []llm.Message
	history = append(history, llm.Message{Role: llm.RoleUser, Content: "look"})
	for i := 0; i < toolImagesMaxCount+2; i++ {
		id := fmt.Sprintf("r%02d", i)
		history = append(history,
			llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "read"}}},
			toolResultWith(id, imagePart(fmt.Sprintf("p%02d.png", i))))
	}
	out := withToolImages(history, true, noToolImageFile)
	sent := 0
	var leftOut []string
	for _, m := range out {
		sent += len(m.ImageParts)
		if m.Role == llm.RoleUser && strings.Contains(m.Content, "left out") {
			leftOut = append(leftOut, m.Content)
		}
	}
	if sent != toolImagesMaxCount {
		t.Fatalf("the request carries %d pictures, want %d", sent, toolImagesMaxCount)
	}
	if len(leftOut) != 2 || !strings.Contains(leftOut[0], "p00.png") || !strings.Contains(leftOut[1], "p01.png") {
		t.Errorf("the two oldest pictures are not named as left out: %q", leftOut)
	}
	last := out[len(out)-1]
	if got := imageNames(last); len(got) != 1 || got[0] != fmt.Sprintf("p%02d.png", toolImagesMaxCount+1) {
		t.Errorf("the newest picture is not sent: %v", got)
	}

	big := func(name string) llm.ImagePart {
		p := imagePart(name)
		p.DataURL = "data:image/png;base64," + strings.Repeat("A", 8<<20)
		return p
	}
	heavy := []llm.Message{
		{Role: llm.RoleUser, Content: "look"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "a", Name: "read"}, {ID: "b", Name: "read"}, {ID: "c", Name: "read"}}},
		toolResultWith("a", big("a.png")),
		toolResultWith("b", big("b.png")),
		toolResultWith("c", big("c.png")),
	}
	out = withToolImages(heavy, true, noToolImageFile)
	pictures := out[len(out)-1]
	if got := imageNames(pictures); strings.Join(got, ",") != "b.png,c.png" {
		t.Errorf("under the byte budget the request carries %v, want the two newest", got)
	}
	if !strings.Contains(pictures.Content, "a.png") || !strings.Contains(pictures.Content, "left out") {
		t.Errorf("the step's message %q does not name a.png as left out", pictures.Content)
	}
}

// The pictures a person attached go out with the request too, so they share
// its budget with the tool pictures, newest first: the oldest attachment is
// the one left out, named in its own prompt, and a picture that does not fit
// the bytes left is skipped for an older one that does.
func TestWithToolImagesCountsTheAttachmentsAndSkipsWhatDoesNotFit(t *testing.T) {
	sized := func(name string, mb int) llm.ImagePart {
		p := imagePart(name)
		p.DataURL = "data:image/png;base64," + strings.Repeat("A", mb<<20)
		return p
	}
	var attached []llm.ImagePart
	for i := 0; i < toolImagesMaxCount-2; i++ {
		attached = append(attached, imagePart(fmt.Sprintf("u%02d.png", i)))
	}
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "look", ImageParts: attached},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "a", Name: "read"}, {ID: "b", Name: "read"}, {ID: "c", Name: "read"}}},
		toolResultWith("a", imagePart("a.png")),
		toolResultWith("b", imagePart("b.png")),
		toolResultWith("c", imagePart("c.png")),
	}
	out := withToolImages(history, true, noToolImageFile)
	if got := imageNames(out[len(out)-1]); strings.Join(got, ",") != "a.png,b.png,c.png" {
		t.Errorf("next to %d attachments the request carries %v, want every tool picture of the last step", len(attached), got)
	}
	if got := imageNames(out[0]); len(got) != len(attached)-1 || got[0] != "u01.png" {
		t.Errorf("the prompt goes with %v, want every attachment but the oldest", got)
	}
	if !strings.Contains(out[0].Content, "u00.png") || !strings.Contains(out[0].Content, "left out") {
		t.Errorf("the prompt %q does not name u00.png as left out", out[0].Content)
	}

	history = []llm.Message{
		{Role: llm.RoleUser, Content: "look"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "a", Name: "read"}, {ID: "b", Name: "read"}, {ID: "c", Name: "read"}}},
		toolResultWith("a", sized("small.png", 1)),
		toolResultWith("b", sized("middle.png", 9)),
		toolResultWith("c", sized("big.png", 12)),
	}
	out = withToolImages(history, true, noToolImageFile)
	last := out[len(out)-1]
	if got := imageNames(last); strings.Join(got, ",") != "small.png,big.png" {
		t.Errorf("under the byte budget the request carries %v, want the newest and the small one that still fits", got)
	}
	if !strings.Contains(last.Content, "middle.png") || !strings.Contains(last.Content, "left out") {
		t.Errorf("the message %q does not name middle.png as left out", last.Content)
	}
}

// A picture a read showed is stored once: its copy with the session's assets.
// The result in the history names that copy, its type and size, and the data
// URL is built from the copy when a request goes out, so a session that read
// many screenshots holds no base64 of them in memory or in messages.json. A
// copy that has gone missing is named as such instead of sent.
func TestAToolPictureIsKeptAsItsAssetAndSentFromIt(t *testing.T) {
	dir := t.TempDir()
	shot := testPNG(4, 3, color.NRGBA{B: 255, A: 255})
	if err := os.WriteFile(filepath.Join(dir, "shot.png"), shot, 0o644); err != nil {
		t.Fatal(err)
	}
	sessionDir := filepath.Join(dir, ".session")
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/eyes", MaxTokens: 100, MaxContextTokens: 128000, Multimodal: true}},
		Agent:     config.Agent{Model: "fake/eyes"},
		Tools:     config.Tools{PermissionMode: config.PermModeBypass},
	}
	st := &session.State{ID: "sess_asset_backed", CWD: dir, Mode: session.ModeAgent, SessionDir: sessionDir}
	provider := &evScriptProvider{steps: []evStep{
		{calls: []llm.ToolCall{tcReadPath("r1", "shot.png")}},
		{text: "blue"},
		{text: "still here"},
	}}
	ag := NewAgent(cfg, st, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }
	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "look"}}); err != nil {
		t.Fatal(err)
	}

	var stored llm.ImagePart
	for _, m := range st.GetMessages() {
		if m.Role == llm.RoleTool && len(m.ImageParts) == 1 {
			stored = m.ImageParts[0]
		}
	}
	if stored.DataURL != "" || stored.MIMEType != "image/png" || stored.Size != len(shot) || stored.FilePath == "" {
		t.Fatalf("stored part = {DataURL:%d bytes MIMEType:%q Size:%d FilePath:%q}, want the asset, its type and size and no data URL",
			len(stored.DataURL), stored.MIMEType, stored.Size, stored.FilePath)
	}
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(shot)
	sent := provider.streamSeen[1]
	var got string
	for _, m := range sent {
		for _, p := range m.ImageParts {
			got = p.DataURL
		}
	}
	if got != want {
		t.Fatalf("the request carried %.60q, want the picture built from its asset", got)
	}

	if err := os.Chmod(filepath.Dir(stored.FilePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(stored.FilePath); err != nil {
		t.Fatal(err)
	}
	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "again"}}); err != nil {
		t.Fatal(err)
	}
	last := provider.streamSeen[len(provider.streamSeen)-1]
	for _, m := range last {
		if len(m.ImageParts) > 0 {
			t.Errorf("a %s message carries a picture whose copy is gone", m.Role)
		}
		if m.Role == llm.RoleUser && strings.Contains(m.Content, "shot.png") && !strings.Contains(m.Content, "copy") {
			t.Errorf("the note %q does not say the saved copy is gone", m.Content)
		}
	}
}

// noToolImageFile is the loader of a history whose pictures all ride inline.
func noToolImageFile(p llm.ImagePart) (string, error) {
	return "", fmt.Errorf("no file for %s", p.Name)
}

// The context estimate counts the pictures a request carries, not only the
// text: a session of screenshots otherwise looks empty to result eviction,
// automatic compaction and the context ring, and runs past the window.
func TestConversationTokensCountThePicturesARequestCarries(t *testing.T) {
	text := []llm.Message{{Role: llm.RoleUser, Content: "look"}, toolResultWith("r1")}
	base := conversationTokens(text, true)

	withPictures := []llm.Message{
		{Role: llm.RoleUser, Content: "look", ImageParts: []llm.ImagePart{imagePart("attached.png")}},
		toolResultWith("r1", imagePart("a.png"), imagePart("b.png")),
	}
	withPictures[1].Content = text[1].Content
	if got, want := conversationTokens(withPictures, true), base+3*imageTokensEach; got < want-10 || got > want+10 {
		t.Errorf("three pictures: %d tokens, want about %d", got, want)
	}

	var many []llm.ImagePart
	for i := 0; i < toolImagesMaxCount+15; i++ {
		many = append(many, imagePart(fmt.Sprintf("p%02d.png", i)))
	}
	crowded := []llm.Message{{Role: llm.RoleUser, Content: "look"}, toolResultWith("r1", many...)}
	crowded[1].Content = text[1].Content
	if got, want := conversationTokens(crowded, true), base+toolImagesMaxCount*imageTokensEach; got < want-10 || got > want+10 {
		t.Errorf("%d tool pictures: %d tokens, want about %d (only %d go out)", len(many), got, want, toolImagesMaxCount)
	}
}

// Only pictures are held back from a model that cannot take them: a text file
// attached to a prompt still goes, the providers write it as a labelled block.
func TestWithToolImagesKeepsTheTextFilesOfAPromptForAModelWithoutImages(t *testing.T) {
	notes := llm.ImagePart{DataURL: "data:text/plain;base64,aGVsbG8=", Name: "notes.txt"}
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "look", ImageParts: []llm.ImagePart{imagePart("shot.png"), notes}},
	}
	out := withToolImages(history, false, noToolImageFile)
	if len(out[0].ImageParts) != 1 || out[0].ImageParts[0].Name != "notes.txt" {
		t.Fatalf("parts sent = %+v, want only notes.txt", out[0].ImageParts)
	}
	if !strings.Contains(out[0].Content, "shot.png") || strings.Contains(out[0].Content, "notes.txt") {
		t.Errorf("the note %q should name shot.png and not notes.txt", out[0].Content)
	}
}

// Text files attached to a prompt go as text and take no picture slot; the
// pictures attached to prompts are bounded with the rest, newest first, and
// an older one left out is named in its own message.
func TestTheRequestBudgetCoversEveryPictureAndOnlyPictures(t *testing.T) {
	var files []llm.ImagePart
	for i := 0; i < toolImagesMaxCount+5; i++ {
		files = append(files, llm.ImagePart{DataURL: "data:text/plain;base64,aGk=", Name: fmt.Sprintf("n%02d.txt", i)})
	}
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "notes", ImageParts: files},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "r1", Name: "read"}}},
		toolResultWith("r1", imagePart("shot.png")),
	}
	out := withToolImages(history, true, noToolImageFile)
	if got := imageNames(out[len(out)-1]); strings.Join(got, ",") != "shot.png" {
		t.Errorf("next to %d text files the request carries %v, want shot.png", len(files), got)
	}
	if n := conversationTokens(history, true) - conversationTokens([]llm.Message{history[0], history[1], {Role: llm.RoleTool, ToolCallID: "r1", Content: history[2].Content}}, true); n != imageTokensEach {
		t.Errorf("the pictures of the request cost %d tokens, want one picture's %d", n, imageTokensEach)
	}

	var older, newer []llm.ImagePart
	for i := 0; i < 15; i++ {
		older = append(older, imagePart(fmt.Sprintf("old%02d.png", i)))
		newer = append(newer, imagePart(fmt.Sprintf("new%02d.png", i)))
	}
	history = []llm.Message{
		{Role: llm.RoleUser, Content: "first", ImageParts: older},
		{Role: llm.RoleAssistant, Content: "ok"},
		{Role: llm.RoleUser, Content: "second", ImageParts: newer},
	}
	out = withToolImages(history, true, noToolImageFile)
	sent := len(out[0].ImageParts) + len(out[2].ImageParts)
	if sent != toolImagesMaxCount || len(out[2].ImageParts) != 15 {
		t.Fatalf("sent %d pictures (%d of the newer prompt), want %d with the newer prompt whole", sent, len(out[2].ImageParts), toolImagesMaxCount)
	}
	if !strings.Contains(out[0].Content, "old00.png") || !strings.Contains(out[0].Content, "left out") {
		t.Errorf("the first prompt %q does not name what was left out", out[0].Content)
	}
	if conversationTokens(history, false) != conversationTokens([]llm.Message{{Role: llm.RoleUser, Content: "first"}, {Role: llm.RoleAssistant, Content: "ok"}, {Role: llm.RoleUser, Content: "second"}}, false) {
		t.Error("a model without images is charged for pictures it is not sent")
	}
}

// The copy a request is built from must be the picture that was saved: a link
// planted under its name, or other bytes, count as a missing copy - named to
// the model, never sent - and take no slot from an older picture that fits.
func TestAToolPictureIsSentOnlyFromItsOwnCopy(t *testing.T) {
	sessionDir := t.TempDir()
	st := &session.State{ID: "sess_copies", CWD: t.TempDir(), SessionDir: sessionDir}
	a := &Agent{state: st}
	good := testPNG(3, 2, color.NRGBA{G: 255, A: 255})
	asset, _, err := session.SaveToolImageAsset(sessionDir, "good.png", "image/png", good)
	if err != nil {
		t.Fatal(err)
	}
	part := llm.ImagePart{Name: "good.png", MIMEType: "image/png", Size: len(good), FilePath: asset}
	if url, err := a.loadToolImage(part); err != nil || url != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(good) {
		t.Fatalf("the saved copy loads as %.40q (%v)", url, err)
	}

	secret := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secret, []byte("not for the model"), 0o600); err != nil {
		t.Fatal(err)
	}
	linked, _, _ := session.SaveToolImageAsset(sessionDir, "linked.png", "image/png", testPNG(2, 2, color.Black))
	_ = os.Remove(linked)
	if err := os.Symlink(secret, linked); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := a.loadToolImage(llm.ImagePart{Name: "linked.png", MIMEType: "image/png", Size: 70, FilePath: linked}); err == nil {
		t.Error("a link planted under a copy's name was read")
	}

	changed, _, _ := session.SaveToolImageAsset(sessionDir, "changed.png", "image/png", testPNG(2, 3, color.White))
	_ = os.Chmod(changed, 0o644)
	if err := os.WriteFile(changed, testPNG(2, 3, color.Black), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.loadToolImage(llm.ImagePart{Name: "changed.png", MIMEType: "image/png", FilePath: changed}); err == nil {
		t.Error("a copy whose bytes no longer match its name was read")
	}

	// A missing copy takes no slot: with the budget full but for one, the
	// older picture that fits still goes.
	history := []llm.Message{{Role: llm.RoleUser, Content: "look"}}
	for i := 0; i < toolImagesMaxCount-1; i++ {
		id := fmt.Sprintf("r%02d", i)
		history = append(history, llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "read"}}}, toolResultWith(id, imagePart(fmt.Sprintf("p%02d.png", i))))
	}
	history = append(history,
		llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "gone", Name: "read"}}},
		llm.Message{Role: llm.RoleTool, ToolCallID: "gone", Content: "x", ImageParts: []llm.ImagePart{{Name: "gone.png", MIMEType: "image/png", Size: 10, FilePath: filepath.Join(sessionDir, "assets", "gone-0000000000000000.png")}}})
	history = append([]llm.Message{history[0],
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "first", Name: "read"}}}, toolResultWith("first", imagePart("first.png"))}, history[1:]...)
	out := withToolImages(history, true, a.loadToolImage)
	sent := map[string]bool{}
	for _, m := range out {
		for _, p := range m.ImageParts {
			sent[p.Name] = true
		}
	}
	if !sent["first.png"] || sent["gone.png"] || len(sent) != toolImagesMaxCount {
		t.Errorf("sent %d pictures, first.png=%v gone.png=%v; want the missing copy skipped and the oldest picture in its slot", len(sent), sent["first.png"], sent["gone.png"])
	}
}

func TestWithToolImagesOmitsArtifactsFromProviderProjection(t *testing.T) {
	history := []llm.Message{{Role: llm.RoleTool, ToolCallID: "share-1", Content: "shared", Artifacts: []llm.Artifact{{ID: "artifact", Name: "report.txt", SHA256: "abc", Size: 7}}}}
	out := withToolImages(history, true, noToolImageFile)
	if len(out) != 1 || len(out[0].Artifacts) != 0 {
		t.Fatalf("provider projection leaked artifacts: %#v", out)
	}
	if len(history[0].Artifacts) != 1 {
		t.Fatal("provider projection changed persisted history")
	}
}
