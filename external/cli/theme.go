//go:build cli

// Package cli implements the interactive console surface: a terminal UI over
// the session manager and agent runner, visually modeled on the pi coding
// agent's TUI (github.com/badlogic/pi-mono, commit b1efcf7d7, MIT license,
// Mario Zechner) with colors from the coddy SPA palette.
package cli

import (
	"os"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
)

// Theme role names follow pi's theme schema (theme/dark.json).
const (
	roleAccent       = "accent"
	roleBorder       = "border"
	roleBorderAccent = "borderAccent"
	roleBorderMuted  = "borderMuted"
	roleSuccess      = "success"
	roleError        = "error"
	roleWarning      = "warning"
	roleMuted        = "muted"
	roleDim          = "dim"
	roleText         = "text"
	roleThinking     = "thinkingText"
	roleSelectedBg   = "selectedBg"
	roleUserMsgBg    = "userMessageBg"
	roleUserMsgText  = "userMessageText"
	roleToolPendBg   = "toolPendingBg"
	roleToolOKBg     = "toolSuccessBg"
	roleToolErrBg    = "toolErrorBg"
	roleToolTitle    = "toolTitle"
	roleToolOutput   = "toolOutput"
	roleMdHeading    = "mdHeading"
	roleMdLink       = "mdLink"
	roleMdLinkURL    = "mdLinkUrl"
	roleMdCode       = "mdCode"
	roleMdCodeBlock  = "mdCodeBlock"
	roleMdCodeBorder = "mdCodeBlockBorder"
	roleMdQuote      = "mdQuote"
	roleMdQuoteBd    = "mdQuoteBorder"
	roleMdHr         = "mdHr"
	roleMdBullet     = "mdListBullet"
	roleBashMode     = "bashMode"
)

// newTheme builds a tui.Theme for the named variant ("dark" or "light").
func newTheme(name string) *tui.Theme {
	colors := darkColors
	if name == "light" {
		colors = lightColors
	}
	return &tui.Theme{Colors: colors, TrueColor: detectTrueColor()}
}

func detectTrueColor() bool {
	ct := strings.ToLower(os.Getenv("COLORTERM"))
	return strings.Contains(ct, "truecolor") || strings.Contains(ct, "24bit")
}

// markdownTheme adapts a tui.Theme to the markdown renderer roles.
func markdownTheme(t *tui.Theme, hyperlinks bool) tui.MarkdownTheme {
	md := tui.MarkdownTheme{
		Heading:         t.FgFn(roleMdHeading),
		Code:            t.FgFn(roleMdCode),
		CodeBlock:       t.FgFn(roleMdCodeBlock),
		CodeBlockBorder: t.FgFn(roleMdCodeBorder),
		Quote:           t.FgFn(roleMdQuote),
		QuoteBorder:     t.FgFn(roleMdQuoteBd),
		Hr:              t.FgFn(roleMdHr),
		Link:            t.FgFn(roleMdLink),
		LinkURL:         t.FgFn(roleMdLinkURL),
		ListBullet:      t.FgFn(roleMdBullet),
	}
	if hyperlinks {
		md.Hyperlink = func(text, url string) string {
			if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
				return text
			}
			return "\x1b]8;;" + url + "\x07" + text + "\x1b]8;;\x07"
		}
	}
	return md
}

// selectListTheme adapts a tui.Theme to select-list styling (pi
// getSelectListTheme: selected accent, description muted).
func selectListTheme(t *tui.Theme) tui.SelectListTheme {
	return tui.SelectListTheme{
		SelectedText: t.FgFn(roleAccent),
		Description:  t.FgFn(roleMuted),
		ScrollInfo:   t.FgFn(roleDim),
		NoMatch:      t.FgFn(roleWarning),
	}
}
