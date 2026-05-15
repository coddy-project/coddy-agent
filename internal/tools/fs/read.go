package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
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
			Description: "Read a file as text, or list a directory's entries. For files, optional offset and limit select a 1-based line range (offset defaults to 1). For directories, list immediate children or recurse with recursive.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"filePath": map[string]interface{}{
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
				},
				"required": []string{"filePath"},
			},
		},
		Execute: executeRead,
	}
}

type readArgs struct {
	FilePath    string `json:"filePath"`
	Offset      int    `json:"offset"`
	Limit       int    `json:"limit"`
	Recursive   bool   `json:"recursive"`
	ShowHidden  bool   `json:"show_hidden"`
}

func executeRead(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[readArgs](argsJSON)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(args.FilePath) == "" {
		return "", fmt.Errorf("read: filePath is required")
	}

	path := ResolvePath(args.FilePath, env.CWD)
	if env.RestrictToCWD {
		if err := CheckInsideCWD(path, env.CWD); err != nil {
			return "", err
		}
	}

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

	content := string(data)
	startLine := args.Offset
	if startLine < 1 {
		startLine = 1
	}
	endLine := 0
	if args.Limit > 0 {
		endLine = startLine + args.Limit - 1
	}
	if startLine > 1 || endLine > 0 {
		content = sliceLines(content, startLine, endLine)
	}

	return content, nil
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
