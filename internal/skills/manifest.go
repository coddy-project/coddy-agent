package skills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Marketplace is an agents-standard marketplace manifest, discovered at
// .agents/plugins/marketplace.json or .claude-plugin/marketplace.json.
// Unknown top-level fields ($schema, owner, interface, ...) are ignored so
// both observed shapes parse.
type Marketplace struct {
	Name     string              `json:"name"`
	Metadata MarketplaceMetadata `json:"metadata"`
	Plugins  []MarketplacePlugin `json:"plugins"`
}

// MarketplaceMetadata carries collection-level fields; only the version is used
// (as a fallback when a plugin entry omits its own version).
type MarketplaceMetadata struct {
	Version     string `json:"version"`
	Description string `json:"description"`
}

// MarketplacePlugin is one entry in a marketplace manifest. Version is an
// optional semantic version used for display and update detection.
type MarketplacePlugin struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Version     string       `json:"version"`
	Source      PluginSource `json:"source"`
}

// PluginSource locates a plugin. In the agents standard the `source` field is
// either an object ({"source":"github","repo":"owner/repo"},
// {"source":"url","url":"...","ref":"main"} or, in the shape Claude Code reads,
// {"source":"archive","url":"https://.../plugin.zip","sha256":"..."}) or a bare
// string ("./plugins/foo" relative in-repo path, or a git URL). A plugin is an
// archive only when its object says so: a url ending in .zip is still a git URL.
type PluginSource struct {
	Kind   string // "github" | "url" | "path" | "archive"
	Repo   string // owner/repo (github)
	URL    string // git/http URL (url), zip archive URL (archive)
	Ref    string // branch or tag
	Path   string // relative path inside the marketplace repo (path)
	SHA256 string // expected sha256 of the archive, hex (archive, optional)
}

// UnmarshalJSON accepts both the object and string forms of `source`.
func (ps *PluginSource) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "\"") {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*ps = classifySourceString(s)
		return nil
	}

	var obj struct {
		Source string `json:"source"`
		Repo   string `json:"repo"`
		URL    string `json:"url"`
		Ref    string `json:"ref"`
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	ps.Kind = strings.TrimSpace(obj.Source)
	ps.Repo = strings.TrimSpace(obj.Repo)
	ps.URL = strings.TrimSpace(obj.URL)
	ps.Ref = strings.TrimSpace(obj.Ref)
	ps.Path = strings.TrimSpace(obj.Path)
	ps.SHA256 = strings.TrimSpace(obj.SHA256)
	if ps.Kind == "" {
		switch {
		case ps.Repo != "":
			ps.Kind = "github"
		case ps.URL != "":
			ps.Kind = "url"
		case ps.Path != "":
			ps.Kind = "path"
		}
	}
	return nil
}

// classifySourceString maps a bare string source to a PluginSource. A value
// with a scheme (or scp-style git@host:path, or a .git suffix) is a git URL;
// anything else is treated as a relative in-repo path.
func classifySourceString(s string) PluginSource {
	s = strings.TrimSpace(s)
	if looksLikeGitURL(s) {
		return PluginSource{Kind: "url", URL: s}
	}
	return PluginSource{Kind: "path", Path: s}
}

func looksLikeGitURL(s string) bool {
	if strings.Contains(s, "://") {
		return true
	}
	if strings.HasPrefix(s, "git@") {
		return true
	}
	return strings.HasSuffix(s, ".git")
}

// pluginJSON is the optional .claude-plugin/plugin.json metadata; used only as a
// fallback for name/description when SKILL.md frontmatter is absent.
type pluginJSON struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
}

// marketplaceFileNames lists candidate manifest paths, most-preferred first.
var marketplaceFileNames = []string{
	filepath.Join(".agents", "plugins", "marketplace.json"),
	filepath.Join(".claude-plugin", "marketplace.json"),
}

// findMarketplaceFile returns the manifest path inside root, or "" if none.
func findMarketplaceFile(root string) string {
	for _, rel := range marketplaceFileNames {
		p := filepath.Join(root, rel)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

// parseMarketplace reads and decodes a marketplace manifest file.
func parseMarketplace(path string) (*Marketplace, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var mf Marketplace
	if err := json.Unmarshal(data, &mf); err != nil {
		return nil, err
	}
	return &mf, nil
}

// readPluginJSON reads .claude-plugin/plugin.json under dir, if present.
func readPluginJSON(dir string) *pluginJSON {
	data, err := os.ReadFile(filepath.Join(dir, ".claude-plugin", "plugin.json"))
	if err != nil {
		return nil
	}
	var pj pluginJSON
	if err := json.Unmarshal(data, &pj); err != nil {
		return nil
	}
	return &pj
}
