package fs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/textenc"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

func relPathHasHiddenSegment(rel string) bool {
	if rel == "" || rel == "." {
		return false
	}
	for _, seg := range strings.Split(rel, string(filepath.Separator)) {
		if seg == "" || seg == "." || seg == ".." {
			continue
		}
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

// ReadTool returns the read built-in: file contents with optional line window, or directory listing when the path is a directory.
func ReadTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        "read",
			Description: "Read a file as text, or list a directory's entries. Text in another encoding (UTF-16, a legacy code page) is converted to UTF-8; a binary file (an image, a PDF, an archive) is refused with its type and size instead of its bytes. For files, optional offset and limit select a 1-based line range (offset defaults to 1). For directories, list immediate children or recurse with recursive. Output is capped by tools.output_limits.read; if it is truncated, page with offset/limit. Read results are ephemeral: once you move on, an unmarked page collapses to a placeholder and is dropped as stale after you write to that file. Set keep:true (or call keep_result) to pin a page whose contents you will need later.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Path to a file or directory (absolute or relative to working directory)",
					},
					"offset": map[string]interface{}{
						"type":        "integer",
						"description": "For files: 1-based start line (optional)",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "For files: maximum number of lines to read from offset (optional)",
					},
					"recursive": map[string]interface{}{
						"type":        "boolean",
						"description": "For directories: include subdirectories recursively (default: false)",
					},
					"show_hidden": map[string]interface{}{
						"type":        "boolean",
						"description": "For directories: include dotfiles and dot-directories (default: false)",
					},
					"keep": map[string]interface{}{
						"type":        "boolean",
						"description": "Pin this page in context: it survives eviction until you write to the file (default: false).",
					},
				},
				"required": []string{"path"},
			},
		},
		Execute: executeRead,
	}
}

type readArgs struct {
	Path       string `json:"path"`
	Offset     int    `json:"offset"`
	Limit      int    `json:"limit"`
	Recursive  bool   `json:"recursive"`
	ShowHidden bool   `json:"show_hidden"`
	// Keep pins this page against context eviction. It is consumed by the agent's
	// context-projection pass (internal/agent), not by executeRead.
	Keep bool `json:"keep"`
}

func executeRead(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[readArgs](argsJSON)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Path) == "" {
		return "", fmt.Errorf("read: path is required")
	}

	path := ResolvePath(args.Path, env.CWD)

	st, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}

	if st.IsDir() {
		return listDirContent(path, args.Recursive, args.ShowHidden)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}

	content, err := decodeText(data)
	if err != nil {
		return "", fmt.Errorf("read: %s %w (%s, %d bytes); read shows text files only",
			args.Path, err, sniffKind(data), len(data))
	}
	startLine := args.Offset
	if startLine < 1 {
		startLine = 1
	}
	endLine := 0
	if args.Limit > 0 {
		endLine = startLine + args.Limit - 1
	}
	if startLine > 1 || endLine > 0 {
		content, err = sliceLines(content, startLine, endLine)
		if err != nil {
			return "", fmt.Errorf("read: %w", err)
		}
	}

	return content, nil
}

// binarySniffLen is how much of a file decodeText looks for a NUL byte in,
// the same window git uses to tell binary from text.
const binarySniffLen = 8000

var (
	errBinaryFile  = errors.New("is a binary file, not text")
	errUndecodable = errors.New("is binary, or text in an encoding that could not be detected")
)

// decodeText returns a file's content as text. A signature the content
// sniffer knows as something other than text (an image, a PDF, an archive) is
// refused; the rest is decoded to UTF-8 the way mentions are (a BOM, UTF-16,
// a legacy code page), so a PNG never reaches the model as pages of noise.
// Content no decoder identifies is refused when it carries a NUL byte and
// returned as it stands otherwise: the providers replace what is not UTF-8.
func decodeText(data []byte) (string, error) {
	if kind := sniffKind(data); !strings.HasPrefix(kind, "text/") && kind != "application/octet-stream" {
		return "", errBinaryFile
	}
	text, _, err := textenc.DecodeToUTF8(data)
	if err == nil {
		return text, nil
	}
	head := data
	if len(head) > binarySniffLen {
		head = head[:binarySniffLen]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return "", errUndecodable
	}
	return string(data), nil
}

// sniffKind names the sniffed content type of a file, without parameters.
func sniffKind(data []byte) string {
	kind := http.DetectContentType(data)
	if i := strings.IndexByte(kind, ';'); i >= 0 {
		kind = kind[:i]
	}
	return kind
}

func listDirContent(dirPath string, recursive, showHidden bool) (string, error) {
	var entries []string
	var err error
	if recursive {
		err = filepath.Walk(dirPath, func(p string, info os.FileInfo, errWalk error) error {
			if errWalk != nil {
				return nil
			}
			rel, relErr := filepath.Rel(dirPath, p)
			if relErr != nil || rel == "." {
				return nil
			}
			if !showHidden && relPathHasHiddenSegment(rel) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if info.IsDir() {
				entries = append(entries, rel+"/")
			} else {
				entries = append(entries, rel)
			}
			return nil
		})
	} else {
		var des []os.DirEntry
		des, err = os.ReadDir(dirPath)
		for _, de := range des {
			if !showHidden && strings.HasPrefix(de.Name(), ".") {
				continue
			}
			if de.IsDir() {
				entries = append(entries, de.Name()+"/")
			} else {
				entries = append(entries, de.Name())
			}
		}
	}

	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}

	return strings.Join(entries, "\n"), nil
}
