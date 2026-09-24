//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

func TestStructuredToolCardsFeature(t *testing.T) {
	const testFile = "src/ui/messages/StructuredToolCard.test.tsx"
	const viewportCSS = "src/ui/chat/permissionPreviewViewportCss.test.ts"
	steps := map[string][2]string{
		`^the model switch card shows the model, reasoning and lifetime$`:                   {testFile, "switch_model names the choice and lifetime"},
		`^the HTTP card separates the request and response and masks credentials$`:          {testFile, "http_request separates response status, headers and body and masks credentials"},
		`^the HTTP card names the real address, the proxy and the certificate check$`:       {testFile, "http_request shows every setting that changes where it goes and what it trusts"},
		`^the background task card lists task ids and states$`:                              {testFile, "background_list separates each task's id, state and detail"},
		`^the preview server card links to its address$`:                                    {testFile, "preview_server exposes its address as a link"},
		`^the documentation search card lists matching sections$`:                           {testFile, "documentation search displays references as rows"},
		`^the documentation read card renders the section with a link to the reader$`:       {testFile, "documentation read renders the section and links to the reader"},
		`^the plan cards show the list, document and saved identity$`:                       {testFile, "plan list, read and write have plan-specific bodies"},
		`^the session filing card shows the title, the tags and what changed$`:              {testFile, "session_describe shows the title, the tags and what changed"},
		`^the configuration card shows its answer as fields$`:                               {testFile, "config tools read their JSON answer as fields, with secrets as the server redacted them"},
		`^the memory cards render notes and list hits$`:                                     {testFile, "memory notes render as Markdown and search hits as rows"},
		`^the MCP card shows its arguments and a JSON answer as fields$`:                    {testFile, "an MCP call shows its arguments as fields and a JSON answer as fields"},
		`^the MCP card renders a Markdown answer as a document$`:                            {testFile, "an MCP answer written in Markdown renders as a document"},
		`^the MCP card shows an id past 2\^53 and a repeated key as the server wrote them$`: {testFile, "an MCP answer shows every number as the server wrote it"},
		`^the MCP card shows a JSON array answer as the server's own text, indented$`:       {testFile, "an MCP answer that is a JSON array is the server's text, indented"},
		`^the phone cap never reaches a command block$`:                                     {viewportCSS, "the phone cap never reaches a static viewport"},
	}
	suite := godog.TestSuite{
		Name: "structured_tool_cards",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			for pattern, target := range steps {
				file, name := target[0], target[1]
				sc.Step(pattern, func() error { return runVitestScenario(file, name) })
			}
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/structured_tool_cards.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("structured tool cards feature failed")
	}
}
