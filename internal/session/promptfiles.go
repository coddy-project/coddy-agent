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
	"github.com/EvilFreelancer/coddy-agent/internal/textenc"
)

// MaxPromptAttachmentBytes caps how much of each file may be inlined into prompts.
const MaxPromptAttachmentBytes = 512 * 1024

// PromptFileAttachment mirrors optional JSON attachments on POST /v1/responses.
type PromptFileAttachment struct {
	Path   string                           `json:"path"`
	Source *PromptFileAttachmentSourceField `json:"source,omitempty"`
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
		if _, err := NormalizeWorkspaceRelativePath(rel); err != nil {
			return nil, err
		}
		if strings.HasSuffix(filepath.ToSlash(strings.TrimSpace(rel)), "/") {
			return nil, ErrFolderAttach
		}
		uri := filepath.ToSlash(strings.TrimSpace(rel))
		if a.Source != nil && strings.TrimSpace(a.Source.Literal) != "" {
			text := a.Source.Literal
			if !utf8.ValidString(text) {
				return nil, fmt.Errorf("literal attachment is not valid UTF-8")
			}
			// The body is whatever the client sent, so no line label is claimed for it.
			out = append(out, acp.ContentBlock{
				Type: "resource",
				Resource: &acp.Resource{
					URI:      uri,
					MimeType: "text/plain; charset=utf-8",
					Text:     text,
				},
			})
			continue
		}
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

// HydratePromptContentBlocks fills empty resource block text from disk and resolves file:// URIs under cwd.
func HydratePromptContentBlocks(cwdAbs string, blocks []acp.ContentBlock) ([]acp.ContentBlock, error) {
	cwdAbs, err := filepath.Abs(filepath.Clean(cwdAbs))
	if err != nil {
		return nil, err
	}
	out := make([]acp.ContentBlock, len(blocks))
	copy(out, blocks)
	for i := range out {
		if out[i].Type != "resource" || out[i].Resource == nil {
			continue
		}
		res := out[i].Resource
		if strings.TrimSpace(res.Text) != "" {
			continue
		}
		baseURI, startLine, endLine := SplitLineRangeURI(res.URI)
		rel, err := resourceURIWorkspaceRel(cwdAbs, baseURI)
		if err != nil {
			return nil, err
		}
		if rel == "" {
			return nil, fmt.Errorf("empty resource uri")
		}
		if strings.HasSuffix(filepath.ToSlash(rel), "/") {
			return nil, ErrFolderAttach
		}
		text, mime, err := ReadWorkspaceUTF8(cwdAbs, rel)
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
		out[i].Resource = &acp.Resource{
			URI:      res.URI,
			MimeType: mime,
			Text:     sliced,
		}
	}

	covered := make(map[string]struct{})
	for _, b := range out {
		if b.Type != "resource" || b.Resource == nil {
			continue
		}
		if strings.TrimSpace(b.Resource.Text) == "" {
			continue
		}
		key, err := normalizedResourceRelativeKey(cwdAbs, b.Resource.URI)
		if err != nil || key == "" {
			continue
		}
		covered[key] = struct{}{}
	}

	rebuilt := make([]acp.ContentBlock, 0, len(out)+4)
	for _, b := range out {
		rebuilt = append(rebuilt, b)
		if b.Type != "text" && b.Type != acp.ContentTypeText {
			continue
		}
		for _, ref := range ExtractAtFileRefsFromText(b.Text) {
			key := lineRangeURI(filepath.ToSlash(strings.TrimSpace(ref.Path)), ref.StartLine, ref.EndLine)
			if _, ok := covered[key]; ok {
				continue
			}
			covered[key] = struct{}{}
			textContent, mime, err := ReadWorkspaceUTF8(cwdAbs, ref.Path)
			if err != nil {
				// @tokens here are extracted heuristically from free text. One that does not
				// resolve to a readable workspace file (an @mention rule trigger, a username,
				// ordinary prose, or a name that happens to hit a binary file) is left as
				// text instead of failing the whole prompt.
				if errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrFolderAttach) || errors.Is(err, ErrNotDecodableText) {
					continue
				}
				return nil, err
			}
			sliced, err := sliceLines(textContent, ref.StartLine, ref.EndLine)
			if err != nil {
				// A typed range past the end of the file is as heuristic as the
				// token itself: it stays prose, and the model can still read the
				// file with its tools.
				continue
			}
			rebuilt = append(rebuilt, acp.ContentBlock{
				Type: "resource",
				Resource: &acp.Resource{
					URI:      key,
					MimeType: mime,
					Text:     sliced,
				},
			})
		}
	}
	return rebuilt, nil
}

func normalizedResourceRelativeKey(cwdAbs, uri string) (string, error) {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return "", nil
	}
	base, startLine, endLine := SplitLineRangeURI(uri)
	rel, err := resourceURIWorkspaceRel(cwdAbs, base)
	if err != nil {
		return "", err
	}
	return lineRangeURI(filepath.ToSlash(rel), startLine, endLine), nil
}

func resourceURIWorkspaceRel(cwdAbs, uri string) (string, error) {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return "", nil
	}
	if strings.HasPrefix(uri, "file:") {
		u, err := url.Parse(uri)
		if err != nil {
			return "", err
		}
		p := u.Path
		if p == "" {
			return "", fmt.Errorf("invalid file uri")
		}
		p, err = url.PathUnescape(p)
		if err != nil {
			return "", err
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", err
		}
		rel, err := filepath.Rel(cwdAbs, abs)
		if err != nil {
			return "", err
		}
		for _, seg := range strings.Split(rel, string(filepath.Separator)) {
			if seg == ".." {
				return "", ErrPathTraversal
			}
		}
		return rel, nil
	}
	return NormalizeWorkspaceRelativePath(uri)
}
