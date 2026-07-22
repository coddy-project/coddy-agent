package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

const (
	printTreeDefaultDepth = 3
	printTreeMaxDepth     = 8
	printTreeMaxEntries   = 300
)

// printTreeSkipDirs are noise directories omitted from the tree output.
var printTreeSkipDirs = map[string]struct{}{
	".git":         {},
	"node_modules": {},
}

// PrintTreeTool prints a directory tree (like the `tree` command), listing files
// and subdirectories under a path up to a bounded depth.
func PrintTreeTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: "print_tree",
			Description: "Print a directory tree (like the `tree` command): files and subdirectories under `path`, " +
				"indented, up to `depth` levels deep. Skips .git and node_modules. Prefer this over `ls -R` or " +
				"`run_command` to explore a repository's layout.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Directory to print (default: working directory).",
					},
					"depth": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum depth to descend (default 3, max 8).",
					},
				},
			},
		},
		Execute: executePrintTree,
	}
}

type printTreeArgs struct {
	Path  string `json:"path"`
	Depth int    `json:"depth"`
}

func executePrintTree(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[printTreeArgs](argsJSON)
	if err != nil {
		return "", err
	}
	root := env.CWD
	if strings.TrimSpace(args.Path) != "" {
		root = ResolvePath(args.Path, env.CWD)
	}
	st, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("print_tree: %w", err)
	}
	if !st.IsDir() {
		return "", fmt.Errorf("print_tree: path must be a directory: %s", root)
	}

	depth := args.Depth
	if depth <= 0 {
		depth = printTreeDefaultDepth
	}
	if depth > printTreeMaxDepth {
		depth = printTreeMaxDepth
	}

	store := sessionStoreRoot(env.SessionDir)
	var b strings.Builder
	b.WriteString(root)
	b.WriteByte('\n')
	count := 0
	truncated := walkTree(&b, root, "", depth, store, &count)
	out := strings.TrimRight(b.String(), "\n")
	if truncated {
		out += fmt.Sprintf("\n\n(truncated at %d entries)", printTreeMaxEntries)
	}
	return out, nil
}

// walkTree renders the children of dir with the given line prefix, descending up
// to depthLeft more levels. It returns true when the entry cap was reached.
func walkTree(b *strings.Builder, dir, prefix string, depthLeft int, store string, count *int) bool {
	if depthLeft <= 0 {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}

	// Hide the Coddy session store and skip noise directories.
	var kept []os.DirEntry
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		if store != "" && isWithinDir(full, store) {
			continue
		}
		if e.IsDir() {
			if _, skip := printTreeSkipDirs[e.Name()]; skip {
				continue
			}
		}
		kept = append(kept, e)
	}
	// Directories first, then files; each group alphabetical.
	sort.Slice(kept, func(i, j int) bool {
		di, dj := kept[i].IsDir(), kept[j].IsDir()
		if di != dj {
			return di
		}
		return kept[i].Name() < kept[j].Name()
	})

	for i, e := range kept {
		if *count >= printTreeMaxEntries {
			return true
		}
		*count++
		last := i == len(kept)-1
		branch, childPrefix := "├── ", prefix+"│   "
		if last {
			branch, childPrefix = "└── ", prefix+"    "
		}
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		b.WriteString(prefix)
		b.WriteString(branch)
		b.WriteString(name)
		b.WriteByte('\n')
		if e.IsDir() {
			if walkTree(b, filepath.Join(dir, e.Name()), childPrefix, depthLeft-1, store, count) {
				return true
			}
		}
	}
	return false
}
