package docsgen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// AssetsDir holds every image and video the documentation embeds.
const AssetsDir = "docs/assets"

// Asset is one file under docs/assets with the files that reference it.
type Asset struct {
	Path   string // relative to docs/assets
	Size   int64
	UsedBy []string // repository-relative paths
}

// assetIndexFiles are the hand-written files inside docs/assets that are not assets.
var assetIndexFiles = map[string]bool{"INDEX.md": true, "README.md": true}

// referenceSources lists where an asset may legitimately be referenced from,
// besides the documentation itself: the Dockerfile copies the favicons into
// the image, and the capture scripts write the console series.
var referenceSources = []string{"README.md", "DESIGN.md", "AGENTS.md", "CONTRIBUTING.md", "Dockerfile"}

var referenceDirs = []string{"docs", "examples", "external/ui/src"}

// AssetInventory lists every asset with the files that mention it. An asset
// is referenced when a source contains "assets/<path>"; the index itself does
// not count.
func AssetInventory(root string) ([]Asset, error) {
	var assets []Asset
	base := filepath.Join(root, AssetsDir)
	err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(base, path)
		rel = filepath.ToSlash(rel)
		if assetIndexFiles[rel] {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		assets = append(assets, Asset{Path: rel, Size: info.Size()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sources, err := referenceFiles(root)
	if err != nil {
		return nil, err
	}
	// The hand-written part of the index (before the generated inventory)
	// counts too: the brand files it describes are kept on purpose.
	sources = append(sources, AssetsDir+"/INDEX.md")
	for _, src := range sources {
		data, err := os.ReadFile(filepath.Join(root, src))
		if err != nil {
			return nil, err
		}
		text := string(data)
		index := src == AssetsDir+"/INDEX.md"
		if index {
			if i := strings.Index(text, startMarker(MarkerAssets)); i >= 0 {
				text = text[:i]
			}
		}
		for i := range assets {
			// The index names its files bare, in backticks; every other source
			// reaches them through an assets/ path.
			if strings.Contains(text, "assets/"+assets[i].Path) || (index && strings.Contains(text, "`"+assets[i].Path+"`")) {
				assets[i].UsedBy = append(assets[i].UsedBy, src)
			}
		}
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].Path < assets[j].Path })
	return assets, nil
}

func referenceFiles(root string) ([]string, error) {
	var out []string
	for _, f := range referenceSources {
		if _, err := os.Stat(filepath.Join(root, f)); err == nil {
			out = append(out, f)
		}
	}
	for _, dir := range referenceDirs {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil // a missing optional directory is fine
			}
			if d.IsDir() {
				if d.Name() == "node_modules" || d.Name() == "dist" {
					return filepath.SkipDir
				}
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			if strings.HasPrefix(rel, AssetsDir+"/") && !assetIndexFiles[strings.TrimPrefix(rel, AssetsDir+"/")] {
				return nil
			}
			switch filepath.Ext(rel) {
			case ".md", ".py", ".sh", ".mjs", ".js", ".ts", ".tsx", ".html", ".yaml", ".yml":
				if rel == AssetsDir+"/INDEX.md" {
					return nil
				}
				out = append(out, rel)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// CheckAssets reports every asset that nothing references: a file nobody
// links is a file the repository does not need.
func CheckAssets(assets []Asset) []Problem {
	var problems []Problem
	for _, a := range assets {
		if len(a.UsedBy) == 0 {
			problems = append(problems, Problem{AssetsDir + "/" + a.Path, "not referenced by any page, script or the Dockerfile; delete it or use it"})
		}
	}
	return problems
}

// RenderAssetIndex renders the inventory table of docs/assets/INDEX.md, one
// section per folder.
func RenderAssetIndex(assets []Asset) string {
	byDir := map[string][]Asset{}
	var dirs []string
	for _, a := range assets {
		dir := "."
		if i := strings.LastIndex(a.Path, "/"); i >= 0 {
			dir = a.Path[:i]
		}
		if _, ok := byDir[dir]; !ok {
			dirs = append(dirs, dir)
		}
		byDir[dir] = append(byDir[dir], a)
	}
	sort.Strings(dirs)
	var b strings.Builder
	var total int64
	for _, a := range assets {
		total += a.Size
	}
	fmt.Fprintf(&b, "%d files, %s in total.\n", len(assets), humanSize(total))
	for _, dir := range dirs {
		title := "docs/assets"
		if dir != "." {
			title = "docs/assets/" + dir
		}
		fmt.Fprintf(&b, "\n### %s\n\n| File | Size | Used by |\n|------|------|---------|\n", title)
		for _, a := range byDir[dir] {
			name := a.Path
			if dir != "." {
				name = strings.TrimPrefix(a.Path, dir+"/")
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s |\n", name, humanSize(a.Size), strings.Join(a.UsedBy, ", "))
		}
	}
	return b.String()
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
