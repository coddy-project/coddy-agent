package skills

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
)

// Install installs a skill from a local directory path or a GitHub URL.
//
// Supported source formats:
//   - /local/path/to/skill-dir      - copy the directory into install_dir
//   - /local/path/to/SKILL.md       - copy single file into install_dir/<name>/SKILL.md
//   - github.com/user/repo          - clone the repo root as a skill
//   - github.com/user/repo/path/to  - install a subdirectory from a GitHub repo
//   - https://raw.githubusercontent.com/.../SKILL.md - download single file
func Install(cfg *config.Config, src string) error {
	installDir := resolveInstallDir(cfg)

	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return fmt.Errorf("create install dir %s: %w", installDir, err)
	}

	switch {
	case strings.HasPrefix(src, "https://") || strings.HasPrefix(src, "http://"):
		return installFromURL(src, installDir)
	case strings.HasPrefix(src, "github.com/"):
		return installFromGitHub(src, installDir)
	default:
		return installFromLocalPath(src, installDir)
	}
}

// List prints all skills found in configured directories.
func List(cfg *config.Config) error {
	dirs := make([]string, len(cfg.Skills.Dirs))
	copy(dirs, cfg.Skills.Dirs)

	installDir := resolveInstallDir(cfg)
	dirs = append([]string{installDir}, dirs...)

	loader := NewLoader(dirs)
	home := cfg.Paths.Home
	loaded, err := loader.LoadAll(".", home)
	if err != nil {
		return err
	}

	cwd := "."
	installExpanded := filepath.Clean(ExpandConfiguredPath(installDir, cwd, home))

	printSkillsInstallAndSearchRoots(installExpanded, dirs, cwd, home)

	if len(loaded) == 0 {
		fmt.Println("No skills found.")
		return nil
	}

	fmt.Printf("%d skill(s):\n\n", len(loaded))
	renderSkillsTable(os.Stdout, loaded)

	return nil
}

// renderSkillsTable prints NAME and DESCRIPTION using go-pretty with wrapping.
func renderSkillsTable(w io.Writer, loaded []*Skill) {
	nameW, descW := skillTableWidths()
	tw := table.NewWriter()
	tw.SetOutputMirror(w)
	tw.AppendHeader(table.Row{"SKILL", "DESCRIPTION"})
	for _, s := range loaded {
		name := sanitizeTableCell(displaySkillName(s))
		desc := sanitizeTableCell(skillDescriptionLine(s))
		tw.AppendRow(table.Row{name, desc})
	}
	tw.SetColumnConfigs([]table.ColumnConfig{
		{Number: 1, Align: text.AlignLeft, AlignHeader: text.AlignCenter, WidthMax: nameW},
		{Number: 2, Align: text.AlignLeft, AlignHeader: text.AlignCenter, WidthMax: descW},
	})
	style := table.StyleRounded
	style.Format.Header = text.FormatUpper
	style.Color.Header = text.Colors{text.Bold, text.FgHiCyan}
	style.Options.SeparateHeader = true
	style.Options.SeparateRows = true
	tw.SetStyle(style)
	tw.Render()
	fmt.Fprintln(w)
}

func skillTableWidths() (nameCol, descCol int) {
	term := stdoutCols()
	const bordersPadding = 7
	inner := term - bordersPadding
	if inner < 50 {
		inner = 50
	}
	nameCol = 24
	descCol = inner - nameCol - 3
	if descCol < 32 {
		descCol = 32
		nameCol = inner - descCol - 3
		if nameCol < 14 {
			nameCol = 14
		}
	}
	return nameCol, descCol
}

func stdoutCols() int {
	c := strings.TrimSpace(os.Getenv("COLUMNS"))
	if c == "" {
		return 100
	}
	n, err := strconv.Atoi(c)
	if err != nil || n < 40 {
		return 100
	}
	return n
}

func printSkillsInstallAndSearchRoots(installExpanded string, skillDirs []string, cwd, home string) {
	fmt.Println("Skills install dir:")
	fmt.Printf("  %s\n\n", installExpanded)
	printSkillSearchRoots(skillDirs, cwd, home)
	fmt.Println()
}

func printSkillSearchRoots(skillDirs []string, cwd, home string) {
	fmt.Println("Search roots:")
	seen := make(map[string]bool)
	for _, d := range skillDirs {
		p := filepath.Clean(ExpandConfiguredPath(d, cwd, home))
		if seen[p] {
			continue
		}
		seen[p] = true
		fmt.Printf("  %s\n", p)
	}
}

// displaySkillName returns a stable id for SKILL.md layouts (folder name instead of "SKILL").
func displaySkillName(s *Skill) string {
	base := filepath.Base(s.FilePath)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if strings.EqualFold(stem, "SKILL") {
		return filepath.Base(filepath.Dir(s.FilePath))
	}
	return stem
}

func skillDescriptionLine(s *Skill) string {
	if d := strings.TrimSpace(s.Description); d != "" {
		return d
	}
	return firstMarkdownBlurb(s.Content)
}

func firstMarkdownBlurb(content string) string {
	lines := strings.Split(content, "\n")
	inFence := false
	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		lineTrim := strings.TrimSpace(line)
		if strings.HasPrefix(lineTrim, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if lineTrim == "" || lineTrim == "---" {
			continue
		}
		if strings.HasPrefix(lineTrim, "#") {
			continue
		}
		if len(lineTrim) > 160 {
			return lineTrim[:157] + "..."
		}
		return lineTrim
	}
	return "(no description)"
}

func sanitizeTableCell(s string) string {
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

// Uninstall removes a skill directory from the configured install_dir only.
// skillName must be a single path segment (no slashes, no "..").
func Uninstall(cfg *config.Config, skillName string) error {
	name, err := sanitizeInstallSkillName(skillName)
	if err != nil {
		return err
	}

	installDir := filepath.Clean(resolveInstallDir(cfg))
	target := filepath.Join(installDir, name)
	target = filepath.Clean(target)
	if !pathWithinInstallDir(installDir, target) {
		return fmt.Errorf("refusing to delete path outside install dir")
	}

	fi, err := os.Lstat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("skill %q is not installed under %s", name, installDir)
		}
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("not a skill directory: %s", target)
	}

	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("remove %s: %w", target, err)
	}

	fmt.Printf("Removed skill %q from %s\n", name, installDir)
	return nil
}

func sanitizeInstallSkillName(skillName string) (string, error) {
	name := strings.TrimSpace(skillName)
	if name == "" {
		return "", fmt.Errorf("skill name is empty")
	}
	if name == "." || name == ".." {
		return "", fmt.Errorf("invalid skill name %q", skillName)
	}
	if filepath.Dir(name) != "." {
		return "", fmt.Errorf("skill name must be a single directory name, not a path (got %q)", skillName)
	}
	return name, nil
}

func pathWithinInstallDir(installDir, target string) bool {
	rel, err := filepath.Rel(installDir, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func installFromLocalPath(src, installDir string) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat %s: %w", src, err)
	}

	if !info.IsDir() {
		name := strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
		destDir := filepath.Join(installDir, name)
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			return err
		}
		return copyFile(src, filepath.Join(destDir, "SKILL.md"))
	}

	name := filepath.Base(src)
	destDir := filepath.Join(installDir, name)
	if err := copyDir(src, destDir); err != nil {
		return err
	}

	fmt.Printf("Installed skill %q to %s\n", name, destDir)
	return nil
}

func installFromURL(rawURL, installDir string) error {
	if !strings.HasSuffix(rawURL, ".md") {
		return fmt.Errorf("URL must point to a .md file (got: %s)", rawURL)
	}

	resp, err := http.Get(rawURL) //nolint:noctx
	if err != nil {
		return fmt.Errorf("download %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", rawURL, resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	parts := strings.Split(strings.TrimRight(rawURL, "/"), "/")
	name := strings.TrimSuffix(parts[len(parts)-1], ".md")
	if len(parts) >= 2 {
		if parts[len(parts)-1] == "SKILL.md" {
			name = parts[len(parts)-2]
		}
	}

	destDir := filepath.Join(installDir, name)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}

	destFile := filepath.Join(destDir, "SKILL.md")
	if err := os.WriteFile(destFile, data, 0o644); err != nil {
		return fmt.Errorf("write skill file: %w", err)
	}

	fmt.Printf("Installed skill %q to %s\n", name, destFile)
	return nil
}

func installFromGitHub(ghPath, installDir string) error {
	parts := strings.SplitN(ghPath, "/", 4)
	if len(parts) < 3 {
		return fmt.Errorf("invalid GitHub path %q - expected github.com/user/repo[/path]", ghPath)
	}

	user := parts[1]
	repo := parts[2]
	subPath := ""
	if len(parts) == 4 {
		subPath = parts[3]
	}

	skillFile := "SKILL.md"
	if subPath != "" {
		skillFile = subPath + "/SKILL.md"
	}

	rawURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/main/%s", user, repo, skillFile)
	if err := installFromURL(rawURL, installDir); err != nil {
		rawURL = fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/master/%s", user, repo, skillFile)
		if err2 := installFromURL(rawURL, installDir); err2 != nil {
			return fmt.Errorf("could not fetch SKILL.md from %s (tried main and master branches): %w", ghPath, err)
		}
	}

	return nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)

		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}

		return copyFile(path, target)
	})
}

func resolveInstallDir(cfg *config.Config) string {
	dir := strings.TrimSpace(cfg.Skills.InstallDir)
	home := cfg.Paths.Home
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(h, ".coddy")
		}
	}
	if dir == "" {
		if home != "" {
			return filepath.Join(home, "skills")
		}
		return ".coddy/skills"
	}
	return filepath.Clean(ExpandConfiguredPath(dir, ".", home))
}
