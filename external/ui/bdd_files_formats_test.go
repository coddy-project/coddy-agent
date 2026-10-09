//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

// What the Files window shows for a file is rendered components, so each step
// runs the Vitest test that drives it: the colouring of source over its whole
// text, a PDF in the browser's own viewer, an SVG as a picture and an HTML page
// in a sandbox, each switched to its source from the window's menu, and the
// menu a right click opens on any picture of the app.
func TestWebUIFilesFormatsFeature(t *testing.T) {
	steps := []struct{ step, file, name string }{
		{`^a block comment is coloured on every line of an open file$`,
			"src/ui/files/FilesView.test.tsx",
			"a block comment is coloured on every line of an open file"},
		{`^a file in a language the chat's code blocks know is coloured in the window too$`,
			"src/ui/files/FilesView.test.tsx",
			"a file in a language the chat's code blocks know is coloured in the window too"},
		{`^a PDF opens in the browser's own viewer where the browser has one$`,
			"src/ui/files/FilesView.test.tsx",
			"a PDF opens in the browser's own viewer where the browser has one"},
		{`^an SVG opens as a picture, and Preview in the menu shows its source$`,
			"src/ui/files/FilesView.test.tsx",
			"an SVG opens as a picture, and Preview in the menu shows its source"},
		{`^the choice of Preview is remembered for every file of its kind$`,
			"src/ui/files/FilesView.test.tsx",
			"the choice of Preview is remembered for every file of its kind"},
		{`^an HTML file opens as its source, and Preview shows it in a sandbox where nothing runs$`,
			"src/ui/files/FilesView.test.tsx",
			"an HTML file opens as its source, and Preview shows it in a sandbox where nothing runs"},
		{`^a right click on a picture of the app offers to copy it and to save it$`,
			"src/ui/components/ImageMenu.test.tsx",
			"a right click on a picture of the app offers to copy it and to save it"},
		{`^Copy image puts the picture on the clipboard as a PNG$`,
			"src/ui/components/ImageMenu.test.tsx",
			"Copy image puts the picture on the clipboard as a PNG"},
		{`^Save image downloads the picture under its name$`,
			"src/ui/components/ImageMenu.test.tsx",
			"Save image downloads the picture under its name"},
		{`^in the full-screen viewer Escape puts the menu away and leaves the picture open$`,
			"src/ui/components/ImageMenu.test.tsx",
			"in the full-screen viewer Escape puts the menu away and leaves the picture open"},
	}
	suite := godog.TestSuite{
		Name: "web_ui_files_formats",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			for _, s := range steps {
				file, name := s.file, s.name
				sc.Step(s.step, func() error { return runVitestScenario(file, name) })
			}
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/web_ui_files_formats.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("web UI files formats feature failed")
	}
}
