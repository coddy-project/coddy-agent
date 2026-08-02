package session

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
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
type PromptFileAttachmentSourceField struct {
	Literal string `json:"literal,omitempty"`
	Start   int    `json:"start,omitempty"`
	End     int    `json:"end,omitempty"`
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
		rel, err := resourceURIWorkspaceRel(cwdAbs, res.URI)
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
		out[i].Resource = &acp.Resource{
			URI:      res.URI,
			MimeType: mime,
			Text:     text,
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
		for _, relPath := range ExtractAtFilePathsFromText(b.Text) {
			key := filepath.ToSlash(strings.TrimSpace(relPath))
			if _, ok := covered[key]; ok {
				continue
			}
			covered[key] = struct{}{}
			textContent, mime, err := ReadWorkspaceUTF8(cwdAbs, relPath)
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
			rebuilt = append(rebuilt, acp.ContentBlock{
				Type: "resource",
				Resource: &acp.Resource{
					URI:      key,
					MimeType: mime,
					Text:     textContent,
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
	rel, err := resourceURIWorkspaceRel(cwdAbs, uri)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
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
