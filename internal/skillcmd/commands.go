// Package skillcmd implements CLI subcommands for managing skills.
package skillcmd

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/prompts"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
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

	// Prepend install_dir so it shows up first.
	installDir := resolveInstallDir(cfg)
	dirs = append([]string{installDir}, dirs...)

	loader := skills.NewLoader(dirs, cfg.Skills.ExtraFiles)
	loaded, err := loader.LoadAll(".")
	if err != nil {
		return err
	}

	if len(loaded) == 0 {
		fmt.Println("No skills found.")
		fmt.Printf("Install skills with: coddy install-skill <path-or-github-url>\n")
		fmt.Printf("Install directory: %s\n", installDir)
		return nil
	}

	fmt.Printf("Found %d skill(s):\n\n", len(loaded))
	for _, s := range loaded {
		desc := s.Description
		if desc == "" {
			desc = "(no description)"
		}
		globStr := ""
		if len(s.Globs) > 0 {
			globStr = fmt.Sprintf(" [globs: %s]", strings.Join(s.Globs, ", "))
		}
		always := ""
		if s.AlwaysApply {
			always = " [always]"
		}
		fmt.Printf("  %-30s %s%s%s\n", s.Name, desc, globStr, always)
		fmt.Printf("  %s\n\n", s.FilePath)
	}

	fmt.Printf("Install directory: %s\n", installDir)
	return nil
}

// ShowPrompt prints the rendered system prompt for the given mode.
func ShowPrompt(cfg *config.Config, mode string) error {
	if mode != "agent" && mode != "plan" {
		return fmt.Errorf("unknown mode %q - must be 'agent' or 'plan'", mode)
	}

	var customFile, extra string
	switch mode {
	case "plan":
		customFile = cfg.Prompts.PlanFile
		extra = cfg.Prompts.PlanExtra
	default:
		customFile = cfg.Prompts.AgentFile
		extra = cfg.Prompts.AgentExtra
	}

	rendered, err := prompts.Render(mode, customFile, prompts.TemplateData{
		CWD:               "/path/to/project",
		ExtraInstructions: extra,
	})
	if err != nil {
		return fmt.Errorf("render prompt: %w", err)
	}

	fmt.Printf("=== System prompt for mode: %s ===\n\n", mode)
	fmt.Println(rendered)
	return nil
}

// ---- install helpers ----

func installFromLocalPath(src, installDir string) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat %s: %w", src, err)
	}

	if !info.IsDir() {
		// Single .md file - place it into installDir/<name>/SKILL.md.
		name := strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
		destDir := filepath.Join(installDir, name)
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			return err
		}
		return copyFile(src, filepath.Join(destDir, "SKILL.md"))
	}

	// Directory - copy into installDir/<dirname>.
	name := filepath.Base(src)
	destDir := filepath.Join(installDir, name)
	if err := copyDir(src, destDir); err != nil {
		return err
	}

	fmt.Printf("Installed skill %q to %s\n", name, destDir)
	return nil
}

func installFromURL(rawURL, installDir string) error {
	// Only support raw SKILL.md file downloads.
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

	// Derive skill name from URL path.
	parts := strings.Split(strings.TrimRight(rawURL, "/"), "/")
	name := strings.TrimSuffix(parts[len(parts)-1], ".md")
	if len(parts) >= 2 {
		// Use parent dir as name if file is SKILL.md.
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
	// Convert github.com/user/repo[/path] to raw download URL.
	// We download the SKILL.md from the specified path.
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

	// Try to download SKILL.md from the path.
	skillFile := "SKILL.md"
	if subPath != "" {
		skillFile = subPath + "/SKILL.md"
	}

	rawURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/main/%s", user, repo, skillFile)
	if err := installFromURL(rawURL, installDir); err != nil {
		// Try master branch as fallback.
		rawURL = fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/master/%s", user, repo, skillFile)
		if err2 := installFromURL(rawURL, installDir); err2 != nil {
			return fmt.Errorf("could not fetch SKILL.md from %s (tried main and master branches): %w", ghPath, err)
		}
	}

	return nil
}

// ---- file copy helpers ----

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

// resolveInstallDir expands ~ in the install dir path.
func resolveInstallDir(cfg *config.Config) string {
	dir := cfg.Skills.InstallDir
	if dir == "" {
		dir = "~/.config/coddy-agent/skills"
	}
	if strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			dir = filepath.Join(home, dir[2:])
		}
	}
	return dir
}
