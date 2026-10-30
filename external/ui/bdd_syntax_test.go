//go:build http && ui

package ui

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

// Rendering and clipboard behavior are covered by Markdown.test.tsx. This
// contract keeps every appearance wired to the shared syntax token styles.
// A theme's palette is its own file under src/styles/themes; the token styles
// may sit in any partial of the stylesheet.
func TestSyntaxThemesFeature(t *testing.T) {
	var all strings.Builder
	err := filepath.WalkDir("src/styles", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".scss" {
			return err
		}
		b, err := os.ReadFile(path)
		all.Write(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	css := all.String()
	var theme string
	roles := []string{"comment", "keyword", "string", "number", "title", "attribute", "type", "meta", "deletion"}
	suite := godog.TestSuite{
		Name: "ui-syntax-themes",
		ScenarioInitializer: func(ctx *godog.ScenarioContext) {
			ctx.Step(`^the UI theme is "([^"]+)"$`, func(value string) { theme = value })
			ctx.Step(`^code syntax colors are supplied by that theme$`, func() error {
				palette, err := os.ReadFile(filepath.Join("src", "styles", "themes", "_"+theme+".scss"))
				if err != nil {
					return fmt.Errorf("theme %s has no palette file: %w", theme, err)
				}
				// A theme file holds one rule, its palette (stylesModules.test.ts).
				for _, role := range roles {
					if !strings.Contains(string(palette), "--syntax-"+role+":") {
						return fmt.Errorf("theme %s is missing syntax color %s", theme, role)
					}
				}
				return nil
			})
			ctx.Step(`^code token styles use the theme palette$`, func() error {
				for _, role := range roles {
					if !strings.Contains(css, "color: var(--syntax-"+role+")") {
						return fmt.Errorf("syntax color %s is unused", role)
					}
				}
				return nil
			})
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/ui_syntax_themes.feature"}, TestingT: t},
	}
	if suite.Run() != 0 {
		t.Fatal("syntax theme scenarios failed")
	}
}
