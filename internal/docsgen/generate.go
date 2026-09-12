package docsgen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// Options configures one generation run.
type Options struct {
	Root    string // repository root
	Binary  string // coddy binary for the CLI reference; built when empty
	Tags    string // build tags for that build
	RawBase string // base URL of the Markdown twins for llms.txt
	SkipCLI bool   // leave the CLI reference as it is (no binary needed)
	SiteDir string // checkout of coddy-project.github.io; renders its docs layer when set
}

// Result holds the generated files (repository-relative path to content),
// the files of the site layer (relative to the site checkout) and the
// problems the checks found.
type Result struct {
	Files     map[string]string
	SiteFiles map[string]string
	Problems  []Problem
}

// Generated files that carry a spliced block.
const (
	HubFile       = "docs/README.md"
	LLMSFile      = "docs/llms.txt"
	LLMSFullFile  = "docs/llms-full.txt"
	ConfigRefFile = "docs/reference/config.md"
	CLIRefFile    = "docs/reference/cli.md"
	AssetIndex    = "docs/assets/INDEX.md"
	ChangelogFile = "docs/getting-started/changelog.md"
)

// Generate renders every generated file into memory and runs the checks.
func Generate(o Options) (*Result, error) {
	if o.RawBase == "" {
		o.RawBase = DefaultRawBase
	}
	res := &Result{Files: map[string]string{}}
	read := func(rel string) (string, error) {
		if s, ok := res.Files[rel]; ok {
			return s, nil
		}
		b, err := os.ReadFile(filepath.Join(o.Root, rel))
		return string(b), err
	}
	splice := func(rel, marker, body string) error {
		doc, err := read(rel)
		if err != nil {
			return err
		}
		out, err := Splice(doc, marker, body)
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		res.Files[rel] = out
		return nil
	}

	nav, err := LoadNav(o.Root)
	if err != nil {
		return nil, err
	}
	if err := splice(HubFile, MarkerNav, RenderHub(nav)); err != nil {
		return nil, err
	}
	hub := res.Files[HubFile]
	res.Files[LLMSFile] = RenderLLMSIndex(nav, hub, o.RawBase)

	// The config reference follows the embedded schema and the code's defaults.
	defaults, err := FlattenDefaults(config.DocDefaults(config.Paths{Home: "~/.coddy", CWD: "${CWD}"}))
	if err != nil {
		return nil, err
	}
	configRef, err := ConfigReference(config.ConfigSchemaJSON(), defaults)
	if err != nil {
		return nil, err
	}
	if err := splice(ConfigRefFile, MarkerConfig, configRef); err != nil {
		return nil, err
	}

	if !o.SkipCLI {
		binary := o.Binary
		if binary == "" {
			dir, err := os.MkdirTemp("", "docsgen-")
			if err != nil {
				return nil, err
			}
			defer func() { _ = os.RemoveAll(dir) }()
			binary, err = BuildCoddy(o.Root, dir, o.Tags)
			if err != nil {
				return nil, err
			}
		}
		cliRef, err := CLIReference(binary, o.Tags)
		if err != nil {
			return nil, err
		}
		if err := splice(CLIRefFile, MarkerCLI, cliRef); err != nil {
			return nil, err
		}
	}

	assets, err := AssetInventory(o.Root)
	if err != nil {
		return nil, err
	}
	if err := splice(AssetIndex, MarkerAssets, RenderAssetIndex(assets)); err != nil {
		return nil, err
	}

	// llms-full.txt concatenates the pages as they will be after this run.
	full, err := renderFullWithOverlay(nav, o, res.Files)
	if err != nil {
		return nil, err
	}
	res.Files[LLMSFullFile] = full

	if o.SiteDir != "" {
		site, err := RenderSite(nav, o.Root, res.Files)
		if err != nil {
			return nil, err
		}
		site["llms.txt"] = res.Files[LLMSFile]
		site["llms-full.txt"] = res.Files[LLMSFullFile]
		res.SiteFiles = site
	}

	res.Problems = append(res.Problems, CheckNav(o.Root, nav)...)
	res.Problems = append(res.Problems, CheckAssets(assets)...)
	linkFiles, err := markdownToCheck(o.Root)
	if err != nil {
		return nil, err
	}
	res.Problems = append(res.Problems, CheckLinks(o.Root, linkFiles)...)
	return res, nil
}

func renderFullWithOverlay(nav *Nav, o Options, overlay map[string]string) (string, error) {
	dir, err := os.MkdirTemp("", "docsgen-full-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	// Materialise the overlay next to the real files: RenderLLMSFull reads from a root.
	for rel, content := range overlay {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return "", err
		}
	}
	for _, p := range nav.Pages() {
		rel := RepoPath(p.Path)
		if _, ok := overlay[rel]; ok {
			continue
		}
		data, err := os.ReadFile(filepath.Join(o.Root, rel))
		if os.IsNotExist(err) {
			continue // CheckNav reports the missing page
		}
		if err != nil {
			return "", err
		}
		dst := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return "", err
		}
	}
	return RenderLLMSFull(nav, dir, o.RawBase)
}

// markdownToCheck lists the markdown files whose links are verified: the
// documentation tree, stubs included, and the root documents.
func markdownToCheck(root string) ([]string, error) {
	files, err := DocsMarkdown(root, false)
	if err != nil {
		return nil, err
	}
	for _, f := range []string{"README.md", "AGENTS.md", "DESIGN.md", "CONTRIBUTING.md", AssetIndex} {
		if _, err := os.Stat(filepath.Join(root, f)); err == nil {
			files = append(files, f)
		}
	}
	sort.Strings(files)
	return files, nil
}

// Write stores the generated files.
func (r *Result) Write(root string) error {
	for rel, content := range r.Files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Stale reports the generated files whose content on disk differs from the
// generated one.
func (r *Result) Stale(root string) []Problem {
	var out []Problem
	for rel, content := range r.Files {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil || strings.TrimRight(string(data), "\n") != strings.TrimRight(content, "\n") {
			out = append(out, Problem{rel, "generated content is stale, run make docs"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out
}
