//go:build desktop

package shell

import (
	"regexp"
	"strings"
	"testing"
)

func TestWindowButtonsFillTheTitleBar(t *testing.T) {
	css := shellSource(t, "shell.css")
	for selector, fragments := range map[string][]string{
		`.window-btns`:            {"align-items: stretch;", "gap: 0;", "margin-top: -5px;", "margin-right: -8px;"},
		`.window-btns .win`:       {"width: 38px;", "height: 38px;", "border-radius: 0;"},
		`.window-btns .win:hover`: {"color-mix(in srgb, var(--text) 14%, transparent)", "color: var(--text);"},
	} {
		rule := regexp.MustCompile(regexp.QuoteMeta(selector) + `\s*\{([^}]*)\}`).FindStringSubmatch(css)
		if len(rule) != 2 {
			t.Errorf("missing rule %s", selector)
			continue
		}
		for _, fragment := range fragments {
			if !strings.Contains(rule[1], fragment) {
				t.Errorf("%s is missing %q", selector, fragment)
			}
		}
	}
}
