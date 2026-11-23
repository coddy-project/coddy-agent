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

func TestSystemWindowChrome(t *testing.T) {
	css := shellSource(t, "shell.css")
	for selector, fragments := range map[string][]string{
		`:root[data-frame="system"] .window-btns,
:root[data-frame="system"] #resize-edges`: {"display: none;"},
		`:root[data-frame="system"] body`:                 {"--window-radius: 0;"},
		`:root[data-frame="system"][data-framed="false"]`: {"--strip-start: max(0px, calc(var(--window-left-inset, 0px) - 7px));"},
		`.tabs`: {"padding: 5px 8px 0 var(--strip-start, 8px);"},
		`.menu`: {"left: var(--strip-start, 8px);"},
	} {
		rule := regexp.MustCompile(regexp.QuoteMeta(selector) + `\s*\{([^}]*)\}`).FindStringSubmatch(css)
		if len(rule) != 2 {
			t.Errorf("missing system chrome rule %s", selector)
			continue
		}
		for _, fragment := range fragments {
			if !strings.Contains(rule[1], fragment) {
				t.Errorf("%s missing %s", selector, fragment)
			}
		}
	}
}

// Platform attributes must not change the selectors used by the X11 strip.
func TestNativeChromeRulesOnlySelectSystemFrames(t *testing.T) {
	css := shellSource(t, "shell.css")
	native := css[strings.Index(css, `:root[data-frame="system"]`):]
	for _, selector := range []string{`.window-btns`, `#resize-edges`, `body`, `.tabs`} {
		rules := regexp.MustCompile(`([^{}]+)\{[^{}]*\}`).FindAllStringSubmatch(native, -1)
		for _, rule := range rules {
			if strings.Contains(rule[1], selector) && !strings.Contains(rule[1], `:root[data-frame="system"]`) {
				t.Errorf("native chrome selector reaches an ordinary strip: %s", rule[1])
			}
		}
	}
}
