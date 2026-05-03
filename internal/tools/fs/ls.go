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

// ListDirTool returns the list_dir built-in tool.
func ListDirTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        "list_dir",
			Description: "List files and subdirectories at the given path.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Directory path (default: working directory)",
					},
					"recursive": map[string]interface{}{
						"type":        "boolean",
						"description": "Include all subdirectories recursively (default: false)",
					},
					"show_hidden": map[string]interface{}{
						"type":        "boolean",
						"description": "Include dotfiles and dot-directories (names starting with '.', default: false)",
					},
				},
			},
		},
		AllowedInPlanMode: true,
		Execute:           executeListDir,
	}
}

type listDirArgs struct {
	Path       string `json:"path"`
	Recursive  bool   `json:"recursive"`
	ShowHidden bool   `json:"show_hidden"`
}

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

func executeListDir(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[listDirArgs](argsJSON)
	if err != nil {
		return "", err
	}

	dirPath := env.CWD
	if args.Path != "" {
		dirPath = ResolvePath(args.Path, env.CWD)
	}

	if env.RestrictToCWD {
		if err := CheckInsideCWD(dirPath, env.CWD); err != nil {
			return "", err
		}
	}

	var entries []string
	if args.Recursive {
		err = filepath.Walk(dirPath, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			rel, relErr := filepath.Rel(dirPath, p)
			if relErr != nil || rel == "." {
				return nil
			}
			if !args.ShowHidden && relPathHasHiddenSegment(rel) {
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
		des, readErr := os.ReadDir(dirPath)
		err = readErr
		for _, de := range des {
			if !args.ShowHidden && strings.HasPrefix(de.Name(), ".") {
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
		return "", fmt.Errorf("list_dir: %w", err)
	}

	return strings.Join(entries, "\n"), nil
}
