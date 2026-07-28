package skills

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
)

// Enable removes a skill from the disabled list so it will be loaded normally.
func Enable(cfg *config.Config, skillName string) error {
	name, err := sanitizeSkillName(skillName)
	if err != nil {
		return err
	}
	managedDir := cfg.Skills.ManagedDir(cfg.Paths.Home)
	if err := EnableSkill(managedDir, name); err != nil {
		return fmt.Errorf("enable skill: %w", err)
	}
	return nil
}

// Disable adds a skill to the disabled list so it is skipped during loading.
func Disable(cfg *config.Config, skillName string) error {
	name, err := sanitizeSkillName(skillName)
	if err != nil {
		return err
	}
	managedDir := cfg.Skills.ManagedDir(cfg.Paths.Home)
	if err := DisableSkill(managedDir, name); err != nil {
		return fmt.Errorf("disable skill: %w", err)
	}
	return nil
}

// List prints all skills found in configured directories.
func List(cfg *config.Config) error {
	managedDir := cfg.Skills.ManagedDir(cfg.Paths.Home)
	home := cfg.Paths.Home
	cwd := "."

	loader := NewLoader(cfg.Skills.Dirs)
	loaded, err := loader.LoadAll(cwd, home, managedDir)
	if err != nil {
		return err
	}

	printSkillSearchRoots(cfg.Skills.Dirs, cwd, home)

	if len(loaded) == 0 {
		fmt.Println("No skills found.")
		return nil
	}

	disabled := ReadDisabled(managedDir)
	remote := RemoteSources(cfg)

	fmt.Printf("%d skill(s):\n\n", len(loaded))
	renderSkillsTable(os.Stdout, loaded, disabled, remote)

	return nil
}

func renderSkillsTable(w io.Writer, loaded []*Skill, disabled map[string]struct{}, remote map[string]RemoteEntry) {
	nameW, descW := skillTableWidths()
	tw := table.NewWriter()
	tw.SetOutputMirror(w)
	tw.AppendHeader(table.Row{"SKILL", "VERSION", "STATUS", "DESCRIPTION"})
	for _, s := range loaded {
		name := sanitizeTableCell(displaySkillName(s))
		desc := sanitizeTableCell(skillDescriptionLine(s))
		version := sanitizeTableCell(InstalledVersion(remote, CanonicalCommandName(s), s))
		if version == "" {
			version = "-"
		}
		status := "enabled"
		if IsDisabled(disabled, name) {
			status = "disabled"
		}
		tw.AppendRow(table.Row{name, version, status, desc})
	}
	tw.SetColumnConfigs([]table.ColumnConfig{
		{Number: 1, Align: text.AlignLeft, AlignHeader: text.AlignCenter, WidthMax: nameW},
		{Number: 2, Align: text.AlignCenter, AlignHeader: text.AlignCenter, WidthMax: 12},
		{Number: 3, Align: text.AlignCenter, AlignHeader: text.AlignCenter, WidthMax: 10},
		{Number: 4, Align: text.AlignLeft, AlignHeader: text.AlignCenter, WidthMax: descW},
	})
	style := table.StyleRounded
	style.Format.Header = text.FormatUpper
	style.Color.Header = text.Colors{text.Bold, text.FgHiCyan}
	style.Options.SeparateHeader = true
	style.Options.SeparateRows = true
	tw.SetStyle(style)
	tw.Render()
	_, _ = fmt.Fprintln(w)
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
	fmt.Println()
}

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
	return FirstMarkdownBlurb(s.Content)
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

func sanitizeSkillName(skillName string) (string, error) {
	name := strings.TrimSpace(skillName)
	if name == "" {
		return "", fmt.Errorf("skill name is empty")
	}
	if name == "." || name == ".." {
		return "", fmt.Errorf("invalid skill name %q", skillName)
	}
	if filepath.Dir(name) != "." {
		return "", fmt.Errorf("skill name must be a single name, not a path (got %q)", skillName)
	}
	return name, nil
}
