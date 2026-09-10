//go:build http && ui

package ui

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

// Rendering and clipboard behavior are covered by Markdown.test.tsx. This
// contract keeps every appearance wired to the shared syntax token styles.
func TestSyntaxThemesFeature(t *testing.T) {
	css, err := os.ReadFile("src/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	var theme string
	roles := []string{"comment", "keyword", "string", "number", "title", "attribute", "type", "meta", "deletion"}
	suite := godog.TestSuite{
		Name: "ui-syntax-themes",
		ScenarioInitializer: func(ctx *godog.ScenarioContext) {
			ctx.Step(`^the UI theme is "([^"]+)"$`, func(value string) { theme = value })
			ctx.Step(`^code syntax colors are supplied by that theme$`, func() error {
				block := regexp.MustCompile(`\[data-theme="` + regexp.QuoteMeta(theme) + `"\]\s*\{[^}]*\}`).FindString(string(css))
				for _, role := range roles {
					if !strings.Contains(block, "--syntax-"+role+":") {
						return fmt.Errorf("theme %s is missing syntax color %s", theme, role)
					}
				}
				return nil
			})
			ctx.Step(`^code token styles use the theme palette$`, func() error {
				for _, role := range roles {
					if !strings.Contains(string(css), "color: var(--syntax-"+role+")") {
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
