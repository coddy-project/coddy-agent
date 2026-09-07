//go:build cli

package cli

// lightColors maps theme roles to the coddy light palette
// (external/ui/src/styles.css [data-theme="light"]).
var lightColors = map[string]string{
	roleAccent:       "#7c3aed",
	roleBorder:       "#7c3aed",
	roleBorderAccent: "#6d28d9",
	roleBorderMuted:  "#d4d4d8",
	roleSuccess:      "#2ea043",
	roleError:        "#cf222e",
	roleWarning:      "#9a6700",
	roleMuted:        "#52525b",
	roleDim:          "#71717a",
	roleText:         "#18181b",
	roleThinking:     "#52525b",
	roleSelectedBg:   "#e4e4e7",
	roleUserMsgBg:    "#e4e4e7",
	roleUserMsgText:  "#18181b",
	roleToolPendBg:   "#ececf4",
	roleToolOKBg:     "#e8f0e8",
	roleToolErrBg:    "#f0e8e8",
	roleToolTitle:    "#18181b",
	roleToolOutput:   "#52525b",
	roleMdHeading:    "#6d28d9",
	roleMdLink:       "#7c3aed",
	roleMdLinkURL:    "#71717a",
	roleMdCode:       "#6d28d9",
	roleMdCodeBlock:  "#588458",
	roleMdCodeBorder: "#6c6c6c",
	roleMdQuote:      "#52525b",
	roleMdQuoteBd:    "#52525b",
	roleMdHr:         "#6c6c6c",
	roleMdBullet:     "#7c3aed",
	roleBashMode:     "#2ea043",
}
