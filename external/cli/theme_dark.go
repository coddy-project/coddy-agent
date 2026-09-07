//go:build cli

package cli

// darkColors maps theme roles to the coddy dark palette
// (external/ui/src/styles.css default theme), with tool/status tints kept in
// pi's neutral family where coddy has no equivalent.
var darkColors = map[string]string{
	roleAccent:       "#9333ea",
	roleBorder:       "#a585c9",
	roleBorderAccent: "#c4b5fd",
	roleBorderMuted:  "#3f3f46",
	roleSuccess:      "#3fb950",
	roleError:        "#f85149",
	roleWarning:      "#d29922",
	roleMuted:        "#9ca3af",
	roleDim:          "#6b7280",
	roleText:         "#ffffff",
	roleThinking:     "#9ca3af",
	roleSelectedBg:   "#2d2d2d",
	roleUserMsgBg:    "#2d2d2d",
	roleUserMsgText:  "#f4f4f5",
	roleToolPendBg:   "#26222e",
	roleToolOKBg:     "#243024",
	roleToolErrBg:    "#3c2828",
	roleToolTitle:    "#ffffff",
	roleToolOutput:   "#9ca3af",
	roleMdHeading:    "#c4b5fd",
	roleMdLink:       "#a78bfa",
	roleMdLinkURL:    "#6b7280",
	roleMdCode:       "#c4a7e7",
	roleMdCodeBlock:  "#b5bd68",
	roleMdCodeBorder: "#808080",
	roleMdQuote:      "#9ca3af",
	roleMdQuoteBd:    "#9ca3af",
	roleMdHr:         "#808080",
	roleMdBullet:     "#9333ea",
	roleBashMode:     "#3fb950",
}
