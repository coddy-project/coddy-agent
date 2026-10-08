package config_test

// The capability-reader guard. What a model can do - the images it reads, the
// reasoning levels it offers, the default level, the off switch - is decided in
// one place per fact: Config.ModelMultimodal and the four Config reasoning
// methods (ReasoningLevelsFor, ReasoningOffOffered, ReasoningChoicesFor,
// DefaultReasoningLevelFor). For a model of a remote Coddy they consult the
// remote's listing, so a surface that reads the raw key of a row instead
// (ModelEntry.Multimodal, .AllowReasoningOff, .ReasoningDefault, or the
// row-level ResolvedReasoningLevels / DefaultReasoningLevel) shows or sends
// something other than what the session really does for that model.
//
// The scan runs over every production .go file of the repository (the pattern of
// internal/llm/proxy_guard_test.go) and fails with file and line for a read
// outside the files that are allowed to have one. A match preceded by "config."
// is the constant config.ReasoningDefault ("default"), which ten production
// sites compare a requested level with, not a field.

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var capabilityReaderPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{".Multimodal", regexp.MustCompile(`\.Multimodal\b`)},
	{".AllowReasoningOff", regexp.MustCompile(`\.AllowReasoningOff\b`)},
	{".ReasoningDefault", regexp.MustCompile(`\.ReasoningDefault\b`)},
	{"ResolvedReasoningLevels(", regexp.MustCompile(`\bResolvedReasoningLevels\(`)},
	{"DefaultReasoningLevel(", regexp.MustCompile(`\bDefaultReasoningLevel\(`)},
}

// capabilityReaderExempt lists the production files that may name these fields
// because the value they read is not a config.ModelEntry's key, with the reason
// of each. internal/config itself is exempt as a whole: it is where the keys
// are resolved.
var capabilityReaderExempt = map[string]string{
	"internal/llm/coddy.go":        "the coddy provider's own view of a listing row (llm.ModelEntry)",
	"internal/llm/coddy_models.go": "fills llm.ModelEntry from the wire row",
	"internal/llm/model_list.go":   "declares llm.ModelEntry",
	"internal/remote/rest.go":      "remote console: wire rows of another process's /v1/models",
	"internal/remote/handler.go":   "remote console: wire rows of another process's /v1/models",
	"internal/session/capability_listing.go": "the listing source: maps the cache's llm.ModelEntry record " +
		"to config.ListedModel",
	"external/httpserver/shared_models_http.go": "the wire row (llm.WireModelRow) of a shared model; " +
		"the values it carries come from the readers",
	// The agent's one-record view of a coddy request (plan 3.3) must know whether
	// the row WRITES levels or `allow_reasoning_off`: an inherited level falls back
	// to the listing's default, a written one is sent as it is. That is a read of
	// whether the key is written, not of its value.
	"internal/agent/coddy_provider.go": "coddyRequestView: whether the row writes reasoning_levels or allow_reasoning_off",
	// The dry run compares the keys a row writes with the remote's listing
	// (plan 3.7): reading the raw key is its purpose.
	"internal/dryrun/coddy_keys.go": "--dry-run compares the written keys with the remote's listing",
	// The console footer shows a non-empty local reasoning_default as the operator
	// wrote it, which needs the raw key; only an EMPTY key asks
	// DefaultReasoningLevelFor (plan 3.4, corrected after the console stage).
	"external/cli/app.go": "the footer shows a written reasoning_default as written (app.go:464)",
}

// capabilityReaderHits returns "path:line: pattern" for every forbidden read in
// one file's text.
func capabilityReaderHits(path, text string) []string {
	var hits []string
	for i, line := range strings.Split(text, "\n") {
		code := goCodeOf(line)
		if strings.TrimSpace(code) == "" {
			continue
		}
		for _, p := range capabilityReaderPatterns {
			for _, loc := range p.re.FindAllStringIndex(code, -1) {
				if strings.HasSuffix(code[:loc[0]], "config") {
					continue // config.ReasoningDefault, the constant
				}
				hits = append(hits, path+":"+strconv.Itoa(i+1)+": "+p.name)
			}
		}
	}
	return hits
}

// goCodeOf returns the code of one source line: a line comment is cut off, and
// the inside of a string or rune literal is blanked so a quoted name never
// matches. A raw (backtick) string that spans lines is not tracked; no
// production file names these fields inside one.
func goCodeOf(line string) string {
	var b strings.Builder
	var quote byte // the quote that opened the literal being skipped, 0 outside one
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if quote != 0 {
			switch {
			case quote != '`' && ch == '\\':
				i++
			case ch == quote:
				quote = 0
			}
			b.WriteByte(' ')
			continue
		}
		switch ch {
		case '"', '`', '\'':
			quote = ch
			b.WriteByte(' ')
		case '/':
			if i+1 < len(line) && line[i+1] == '/' {
				return b.String()
			}
			b.WriteByte(ch)
		default:
			b.WriteByte(ch)
		}
	}
	return b.String()
}

// productionGoFiles walks the repository for non-test .go files, skipping the
// folders that hold no production Go code (hidden folders, node_modules, build
// output, a nested checkout).
func productionGoFiles(t *testing.T, visit func(rel, text string)) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "build" ||
				name == "dist" || name == "testdata" || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		visit(filepath.ToSlash(rel), string(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCapabilityReadersAreTheOnlyReaders(t *testing.T) {
	var hits []string
	seenExempt := make(map[string]bool)
	scanned := 0
	productionGoFiles(t, func(rel, text string) {
		scanned++
		if strings.HasPrefix(rel, "internal/config/") {
			return
		}
		if _, ok := capabilityReaderExempt[rel]; ok {
			seenExempt[rel] = true
			return
		}
		hits = append(hits, capabilityReaderHits(rel, text)...)
	})
	if scanned < 100 {
		t.Fatalf("the scan saw only %d production files: the repository root was not found", scanned)
	}
	if len(hits) > 0 {
		t.Fatalf("a capability key of a model row is read outside Config.ModelMultimodal and the four Config reasoning methods "+
			"(they consult a remote Coddy's listing, a raw read does not):\n%s", strings.Join(hits, "\n"))
	}
	// The files the exemption names must exist, or the list rots; the dry run's
	// file arrives with stage S6 and the cli one is removed by S8.
	for rel := range capabilityReaderExempt {
		if rel == "internal/dryrun/coddy_keys.go" {
			continue
		}
		if !seenExempt[rel] {
			t.Errorf("the exemption for %s names a file the scan did not see: remove the entry", rel)
		}
	}
}

// The scanner itself: what it flags and what it leaves alone.
func TestCapabilityReaderScannerFlagsRawReadsOnly(t *testing.T) {
	flagged := []string{
		`	ok := ent.Multimodal`,
		`	if m.AllowReasoningOff != nil {`,
		`	lvl = entry.ReasoningDefault`,
		`	levels := ent.ResolvedReasoningLevels()`,
		`	d := ent.DefaultReasoningLevel()`,
		`	x := cfg.Models[0].Multimodal // trailing comment`,
	}
	for _, line := range flagged {
		if got := capabilityReaderHits("f.go", line); len(got) != 1 {
			t.Errorf("not flagged: %q -> %v", line, got)
		}
	}
	clean := []string{
		`	if req.Reasoning != config.ReasoningDefault {`,
		`	v = config.ReasoningDefault`,
		`	lv := cfg.DefaultReasoningLevelFor(ent)`,
		`	// ent.Multimodal in a comment`,
		`	msg := "models[].multimodal and ent.Multimodal in a string"`,
		`	ok := cfg.ModelMultimodal(ent)`,
		`	x := 1 // ent.AllowReasoningOff`,
	}
	for _, line := range clean {
		if got := capabilityReaderHits("f.go", line); len(got) != 0 {
			t.Errorf("flagged but allowed: %q -> %v", line, got)
		}
	}
}
