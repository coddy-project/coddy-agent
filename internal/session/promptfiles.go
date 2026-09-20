package session

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/mention"
	"github.com/EvilFreelancer/coddy-agent/internal/textenc"
)

// MaxPromptAttachmentBytes caps how much of each file may be inlined into prompts.
const MaxPromptAttachmentBytes = 512 * 1024

// PromptFileAttachment mirrors optional JSON attachments on POST /v1/responses.
type PromptFileAttachment struct {
	Path   string                           `json:"path"`
	Source *PromptFileAttachmentSourceField `json:"source,omitempty"`
	// Kind says what a literal body is when it is not a file's text. The one
	// value is mention.KindStdin: what was piped into a one-shot run under a
	// typed prompt (a remote console sends it that way).
	Kind string `json:"kind,omitempty"`
}

// StdinAttachmentPath names the attachment a one-shot run makes of its piped
// stdin.
const StdinAttachmentPath = "stdin"

// StdinAttachment is the block a one-shot run adds after its prompt for what
// was piped into it (`git diff | coddy -p "review"`). It is a resource, not
// text, so nothing scans it: no "@" in it reads a file or fetches a page, no
// "/command" in it runs, and the transcript shows mention.StdinLabel for it.
func StdinAttachment(text string) acp.ContentBlock {
	return acp.ContentBlock{Type: acp.ContentTypeResource, Resource: &acp.Resource{
		URI:      StdinAttachmentPath,
		MimeType: "text/plain; charset=utf-8",
		Text:     text,
		Mention:  &acp.ResourceMention{Kind: mention.KindStdin},
	}}
}

// PromptFileAttachmentSourceField selects how attachment body is sourced.
// Literal, byte offsets (Start/End into the decoded file) and a 1-based
// inclusive line range (StartLine/EndLine, what a ranged mention "@f.go:21-31"
// sends) are three source modes: literal wins, then byte offsets, then lines.
// The range labels the attachment (the "lines" attribute the model sees) only
// when the body really is that slice of the file; a literal or byte-offset body
// is sent without the label. A range the file cannot honour (zero, inverted, or
// starting past the last line) is rejected with ErrLineRange.
type PromptFileAttachmentSourceField struct {
	Literal   string `json:"literal,omitempty"`
	Start     int    `json:"start,omitempty"`
	End       int    `json:"end,omitempty"`
	StartLine int    `json:"startLine,omitempty"`
	EndLine   int    `json:"endLine,omitempty"`
}

// lineRangeURI appends the "#L<start>-<end>" fragment carrying a mention's line
// range through acp.Resource.URI without changing the ACP types.
func lineRangeURI(rel string, startLine, endLine int) string {
	if startLine < 1 || endLine < startLine {
		return rel
	}
	return fmt.Sprintf("%s#L%d-%d", rel, startLine, endLine)
}

// lineRangeFragmentRe matches the "#L<start>-<end>" fragment only as the whole
// tail of a URI, so "notes#L1-2.go" stays a file name.
var lineRangeFragmentRe = regexp.MustCompile(`^(.*)#L([0-9]{1,9})-([0-9]{1,9})$`)

// SplitLineRangeURI strips a "#L<start>-<end>" fragment from a resource URI and
// returns the base URI with the 1-based inclusive range; a URI without a
// well-formed fragment (1 <= start <= end, nothing after the digits) comes back
// unchanged with zero lines. It is the single parser of the fragment that
// lineRangeURI writes: prompt hydration and the attachment XML both use it.
func SplitLineRangeURI(uri string) (base string, startLine, endLine int) {
	m := lineRangeFragmentRe.FindStringSubmatch(uri)
	if m == nil {
		return uri, 0, 0
	}
	s := atoiDigits(m[2])
	e := atoiDigits(m[3])
	if s < 1 || e < s {
		return uri, 0, 0
	}
	return m[1], s, e
}

// atoiDigits converts an all-digit string; callers bound its length first.
func atoiDigits(s string) int {
	v := 0
	for i := 0; i < len(s); i++ {
		v = v*10 + int(s[i]-'0')
	}
	return v
}

// ErrLineRange marks a line range an attachment cannot honour: zero or inverted
// bounds, or a start past the last line of the file. The range never widens
// into the whole file, since the label would then describe lines the body does
// not hold.
var ErrLineRange = errors.New("invalid attachment line range")

// sliceLines returns the 1-based inclusive line range of text. No range (both
// bounds zero) returns the text untouched; EndLine past the last line clamps;
// anything else the file cannot honour is ErrLineRange. Lines split on "\n" so
// CRLF content keeps its "\r" intact mid-range.
func sliceLines(text string, startLine, endLine int) (string, error) {
	if startLine == 0 && endLine == 0 {
		return text, nil
	}
	if startLine < 1 || endLine < startLine {
		return "", fmt.Errorf("%w %d-%d", ErrLineRange, startLine, endLine)
	}
	lines := strings.Split(text, "\n")
	// A trailing newline yields an empty last element; it is not a line.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if startLine > len(lines) {
		return "", fmt.Errorf("%w %d-%d: the file has %d lines", ErrLineRange, startLine, endLine, len(lines))
	}
	if endLine > len(lines) {
		endLine = len(lines)
	}
	out := strings.Join(lines[startLine-1:endLine], "\n")
	return strings.TrimSuffix(out, "\r"), nil
}

// ErrFolderAttach means a path refers to a directory; only file content may be attached.
var ErrFolderAttach = errors.New("folder paths cannot be attached as file content")

// ErrNotDecodableText means a file is binary, or text in an encoding that could
// not be identified, so there is nothing meaningful to inline into a prompt.
var ErrNotDecodableText = errors.New("file is not text: it is binary or its encoding could not be detected (UTF-8 and common legacy encodings are supported)")

// ReadWorkspaceUTF8 reads a workspace text file under cwdAbs and returns it as
// UTF-8. A file stored in another detectable encoding (Windows-1251 and other
// legacy charsets) is converted; only undecodable content is rejected.
func ReadWorkspaceUTF8(cwdAbs, relPath string) (content string, mime string, err error) {
	normRel, err := NormalizeWorkspaceRelativePath(relPath)
	if err != nil {
		return "", "", err
	}
	if normRel == "" {
		return "", "", fmt.Errorf("empty path")
	}
	abs, err := AbsPathUnderWorkspaceRoot(cwdAbs, normRel)
	if err != nil {
		return "", "", err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", "", err
	}
	if st.IsDir() {
		return "", "", ErrFolderAttach
	}
	if st.Size() > MaxPromptAttachmentBytes {
		return "", "", fmt.Errorf("file too large (max %d bytes)", MaxPromptAttachmentBytes)
	}
	f, err := os.Open(abs)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, MaxPromptAttachmentBytes+1))
	if err != nil {
		return "", "", err
	}
	if len(data) > MaxPromptAttachmentBytes {
		return "", "", fmt.Errorf("file too large (max %d bytes)", MaxPromptAttachmentBytes)
	}
	decoded, _, err := textenc.DecodeToUTF8(data)
	if err != nil {
		return "", "", fmt.Errorf("%w: %s", ErrNotDecodableText, normRel)
	}
	return decoded, "text/plain; charset=utf-8", nil
}

// BuildHydratedComposerPrompt returns one text block plus resource blocks for attachments.
func BuildHydratedComposerPrompt(cwdAbs, input string, attachments []PromptFileAttachment) ([]acp.ContentBlock, error) {
	out := []acp.ContentBlock{{Type: acp.ContentTypeText, Text: input}}
	for _, a := range attachments {
		rel := strings.TrimSpace(a.Path)
		if rel == "" {
			return nil, fmt.Errorf("attachment path is empty")
		}
		literal := a.Source != nil && strings.TrimSpace(a.Source.Literal) != ""
		switch kind := strings.TrimSpace(a.Kind); {
		case kind == "":
		case kind != mention.KindStdin:
			return nil, fmt.Errorf("invalid attachment kind %q: the one kind is %s", kind, mention.KindStdin)
		case !literal:
			return nil, fmt.Errorf("invalid attachment: kind %s needs its body in source.literal", kind)
		default:
			if !utf8.ValidString(a.Source.Literal) {
				return nil, fmt.Errorf("literal attachment is not valid UTF-8")
			}
			out = append(out, StdinAttachment(a.Source.Literal))
			continue
		}
		if literal {
			text := a.Source.Literal
			if !utf8.ValidString(text) {
				return nil, fmt.Errorf("literal attachment is not valid UTF-8")
			}
			// The body is whatever the client sent, so no line label is
			// claimed for it, and its path only names it: an editor's file://
			// URI or a path outside the workspace reads nothing from disk. A
			// file:// one is named the way a mention of that file would be.
			res := &acp.Resource{URI: filepath.ToSlash(rel), MimeType: "text/plain; charset=utf-8", Text: text}
			if base, _, _ := splitClientURI(rel); strings.HasPrefix(base, "file:") {
				if loc, ok := mention.Resolve(cwdAbs, mention.HomeDir(), base); ok {
					res.URI = loc.Display
					res.Mention = &acp.ResourceMention{Kind: mention.KindFile, Path: loc.Abs}
				}
			}
			out = append(out, acp.ContentBlock{Type: acp.ContentTypeResource, Resource: res})
			continue
		}
		if _, err := NormalizeWorkspaceRelativePath(rel); err != nil {
			return nil, err
		}
		if strings.HasSuffix(filepath.ToSlash(strings.TrimSpace(rel)), "/") {
			return nil, ErrFolderAttach
		}
		uri := filepath.ToSlash(strings.TrimSpace(rel))
		text, mime, err := ReadWorkspaceUTF8(cwdAbs, rel)
		if err != nil {
			return nil, err
		}
		if a.Source != nil && a.Source.End > a.Source.Start {
			start, end := a.Source.Start, a.Source.End
			if start < 0 || end > len(text) || start > end {
				return nil, fmt.Errorf("invalid attachment source range")
			}
			text = text[start:end]
		} else if a.Source != nil {
			// Only a body that really is the slice carries the "#L" label.
			sliced, err := sliceLines(text, a.Source.StartLine, a.Source.EndLine)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", uri, err)
			}
			text = sliced
			uri = lineRangeURI(uri, a.Source.StartLine, a.Source.EndLine)
		}
		out = append(out, acp.ContentBlock{
			Type: "resource",
			Resource: &acp.Resource{
				URI:      uri,
				MimeType: mime,
				Text:     text,
			},
		})
	}
	return out, nil
}

// HydratePromptContentBlocks prepares a prompt without a session behind it:
// the resources a client sent are filled from disk (hydrateClientResources)
// and the "@" references of the text are resolved to files and folders
// (ResolveMentionsInDir). A session turn goes through the manager instead,
// which also resolves the references that need a session.
func HydratePromptContentBlocks(cwdAbs string, blocks []acp.ContentBlock) ([]acp.ContentBlock, error) {
	out, err := hydrateClientResources(cwdAbs, blocks)
	if err != nil {
		return nil, err
	}
	return ResolveMentionsInDir(cwdAbs, out), nil
}

// hydrateClientResources fills the resources an ACP client sent without their
// text from disk, and turns each "resource_link" into what it names: a file's
// text, a folder's listing, or - for a link the agent cannot open itself, such
// as an editor-internal URI - a line naming it, so the model at least knows
// what the user pointed at. A resource the client sent is explicit, so one that
// cannot be read (a missing file, lines past the end) fails the prompt; a link
// that names nothing readable does not.
func hydrateClientResources(cwdAbs string, blocks []acp.ContentBlock) ([]acp.ContentBlock, error) {
	cwdAbs, err := filepath.Abs(filepath.Clean(cwdAbs))
	if err != nil {
		return nil, err
	}
	home := mention.HomeDir()
	r := &mentionResolver{cwd: cwdAbs, home: home, seen: map[string]bool{}}
	out := make([]acp.ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		switch {
		case b.Type == acp.ContentTypeResourceLink:
			out = append(out, r.resourceLinkBlock(b))
			continue
		case b.Type != acp.ContentTypeResource || b.Resource == nil:
			out = append(out, b)
			continue
		}
		res := b.Resource
		baseURI, startLine, endLine := splitClientURI(res.URI)
		if strings.TrimSpace(res.Text) != "" {
			// Embedded by the client (Zed reads the buffer itself, unsaved
			// edits included): the text stays as sent, and a file:// one is
			// named the way a mention of that file would be.
			if named := nameEmbeddedFile(cwdAbs, home, res, baseURI, startLine, endLine); named != nil {
				b.Resource = named
			}
			out = append(out, b)
			continue
		}
		if !isLocalResourceURI(baseURI) {
			// Nothing to read here (an editor-internal URI with no text).
			out = append(out, r.resourceLinkBlock(acp.ContentBlock{Type: acp.ContentTypeResourceLink, URI: res.URI}))
			continue
		}
		loc, ok := mention.Resolve(cwdAbs, home, baseURI)
		if !ok {
			return nil, fmt.Errorf("empty resource uri")
		}
		info, err := os.Stat(loc.Abs)
		if err != nil {
			return nil, err
		}
		uri := res.URI
		if strings.HasPrefix(baseURI, "file:") {
			uri = lineRangeURI(loc.Display, startLine, endLine)
		}
		filled := &acp.Resource{URI: uri, MimeType: "text/plain; charset=utf-8", Mention: res.Mention}
		if info.IsDir() {
			display := strings.TrimSuffix(loc.Display, "/") + "/"
			if loc.Display == "./" {
				display = "./"
			}
			filled.Text = r.listFolder(loc, display)
			filled.Mention = &acp.ResourceMention{Kind: mention.KindDirectory, Path: loc.Abs}
			out = append(out, acp.ContentBlock{Type: acp.ContentTypeResource, Resource: filled})
			continue
		}
		text, mime, err := readClientResource(loc, info)
		if err != nil {
			return nil, err
		}
		// A client that asks for lines the file does not have gets the same
		// answer as one that asks for a file that is not there: an error, not a
		// silently widened attachment.
		sliced, err := sliceLines(text, startLine, endLine)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", res.URI, err)
		}
		filled.Text, filled.MimeType = sliced, mime
		if filled.Mention == nil {
			filled.Mention = &acp.ResourceMention{Kind: mention.KindFile, Path: loc.Abs}
		}
		out = append(out, acp.ContentBlock{Type: acp.ContentTypeResource, Resource: filled})
	}
	return out, nil
}

// clientLineFragmentRe reads the line fragment of a file:// URI in the forms
// editors write: "L10", "L10-20", "L10-L20" and Zed's "L10:20".
var clientLineFragmentRe = regexp.MustCompile(`^L([0-9]{1,9})(?:[-:]L?([0-9]{1,9}))?$`)

// splitClientURI separates what a client's resource URI names from the lines
// it narrows to. A file:// URI is parsed as a URI: its fragment is a line
// range when it reads as one (and is dropped otherwise, as an anchor that
// names the whole file), and a query such as Zed's "?symbol=" is dropped. A
// bare path keeps Coddy's own strict "#L<start>-<end>" (SplitLineRangeURI),
// so a file literally named "f.go#L5" still resolves.
func splitClientURI(uri string) (string, int, int) {
	uri = strings.TrimSpace(uri)
	if !strings.HasPrefix(uri, "file:") {
		return SplitLineRangeURI(uri)
	}
	u, err := url.Parse(uri)
	if err != nil {
		return SplitLineRangeURI(uri)
	}
	start, end := 0, 0
	if m := clientLineFragmentRe.FindStringSubmatch(u.Fragment); m != nil {
		start = atoiDigits(m[1])
		end = start
		if m[2] != "" {
			end = atoiDigits(m[2])
		}
		if start < 1 || end < start {
			start, end = 0, 0
		}
	}
	u.Fragment, u.RawFragment, u.RawQuery, u.ForceQuery = "", "", "", false
	return u.String(), start, end
}

// nameEmbeddedFile renames a file:// resource a client embedded with its text
// to the path a mention of the file would carry, so rules scoped to that path
// see it and the attachment reads the same from every surface.
func nameEmbeddedFile(cwd, home string, res *acp.Resource, baseURI string, startLine, endLine int) *acp.Resource {
	if !strings.HasPrefix(baseURI, "file:") {
		return nil
	}
	loc, ok := mention.Resolve(cwd, home, baseURI)
	if !ok {
		return nil
	}
	kind := mention.KindFile
	display := loc.Display
	if strings.HasSuffix(baseURI, "/") {
		kind = mention.KindDirectory
		display = strings.TrimSuffix(display, "/") + "/"
	}
	named := *res
	named.URI = lineRangeURI(display, startLine, endLine)
	named.Mention = &acp.ResourceMention{Kind: kind, Path: loc.Abs}
	if res.Mention != nil {
		named.Mention.Name, named.Mention.Typed = res.Mention.Name, res.Mention.Typed
	}
	return &named
}

// readClientResource reads a file a client named, anywhere on disk: the same
// size cap and encoding detection a workspace attachment has.
func readClientResource(loc mention.Location, info os.FileInfo) (string, string, error) {
	if info.Size() > MaxPromptAttachmentBytes {
		return "", "", fmt.Errorf("file too large (max %d bytes)", MaxPromptAttachmentBytes)
	}
	data, err := os.ReadFile(loc.Abs)
	if err != nil {
		return "", "", err
	}
	decoded, _, err := textenc.DecodeToUTF8(data)
	if err != nil {
		return "", "", fmt.Errorf("%w: %s", ErrNotDecodableText, loc.Display)
	}
	return decoded, "text/plain; charset=utf-8", nil
}

// resourceLinkBlock resolves one ACP resource_link. A file or folder link is
// read like a mention of it; anything else becomes a line naming the link.
func (r *mentionResolver) resourceLinkBlock(b acp.ContentBlock) acp.ContentBlock {
	uri := strings.TrimSpace(b.URI)
	label := strings.TrimSpace(b.Title)
	if label == "" {
		label = strings.TrimSpace(b.Name)
	}
	if isLocalResourceURI(uri) {
		baseURI, startLine, endLine := splitClientURI(uri)
		reading := mention.PathReading{Path: baseURI, Range: mention.Range{Start: startLine, End: endLine}}
		if res, ok, _ := r.resolvePath(reading, label); ok && res != nil {
			if res.Mention != nil && res.Mention.Typed == "" {
				res.Mention.Typed = label
			}
			return acp.ContentBlock{Type: acp.ContentTypeResource, Resource: res}
		}
	}
	var line strings.Builder
	line.WriteString("[Linked resource")
	if label != "" {
		line.WriteString(": ")
		line.WriteString(label)
	}
	if uri != "" {
		line.WriteString(" <")
		line.WriteString(uri)
		line.WriteString(">")
	}
	if d := strings.TrimSpace(b.Description); d != "" {
		line.WriteString(" - ")
		line.WriteString(d)
	}
	line.WriteString("]")
	return acp.ContentBlock{Type: acp.ContentTypeText, Text: line.String()}
}

// isLocalResourceURI reports whether a link names a local path: a file:// URI
// or a scheme-less path.
func isLocalResourceURI(uri string) bool {
	if uri == "" {
		return false
	}
	if strings.HasPrefix(uri, "file:") {
		return true
	}
	if i := strings.Index(uri, "://"); i > 0 {
		return false
	}
	// "C:\x" is a Windows path, not a scheme.
	if i := strings.IndexByte(uri, ':'); i > 1 && !strings.ContainsAny(uri[:i], `/\`) {
		return false
	}
	return true
}
