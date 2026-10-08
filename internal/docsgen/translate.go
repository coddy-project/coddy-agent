package docsgen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/EvilFreelancer/coddy-agent/internal/docs"
)

// The translations of the documentation. docs/<lang>/<path> is the
// translation of docs/<path>, for every page of the map under docs/ and for
// the hub; docs/<lang>/nav.yaml carries the translated titles and summaries,
// docs/<lang>/config-descriptions.yaml the translated descriptions of the
// configuration schema. The English page is the source: a translation records
// the digest of the English page it follows (its stamp), and a change to the
// English page makes the translation stale until it is translated and
// restamped (docs/contributing/documentation.md, Translations).

// Translations lists every language the documentation is translated into.
var Translations = docs.Languages[1:]

// TranslationDir is the folder of a translation, relative to the repository.
func TranslationDir(lang string) string { return "docs/" + lang + "/" }

// TranslatedNavFile is the map of a translation.
func TranslatedNavFile(lang string) string { return TranslationDir(lang) + "nav.yaml" }

// ConfigDescriptionsFile holds the translated descriptions of the schema.
func ConfigDescriptionsFile(lang string) string {
	return TranslationDir(lang) + "config-descriptions.yaml"
}

// translationMeta are the files of a translation that are not pages.
func translationMeta(lang string) map[string]bool {
	return map[string]bool{TranslatedNavFile(lang): true, ConfigDescriptionsFile(lang): true}
}

// TranslatedText is the translation of one title and summary, with the
// English text it translates.
type TranslatedText struct {
	Title   string `yaml:"title"`
	Summary string `yaml:"summary"`
	En      struct {
		Title   string `yaml:"title"`
		Summary string `yaml:"summary"`
	} `yaml:"en"`
}

// TranslatedNav is docs/<lang>/nav.yaml: titles and summaries keyed by group
// id and page path. Order and membership come from docs/nav.yaml.
type TranslatedNav struct {
	Groups map[string]TranslatedText `yaml:"groups"`
	Pages  map[string]TranslatedText `yaml:"pages"`
}

// LoadTranslatedNav reads the map of a translation.
func LoadTranslatedNav(root, lang string) (*TranslatedNav, error) {
	data, err := os.ReadFile(filepath.Join(root, TranslatedNavFile(lang)))
	if err != nil {
		return nil, err
	}
	var tr TranslatedNav
	if err := yaml.Unmarshal(data, &tr); err != nil {
		return nil, fmt.Errorf("%s: %w", TranslatedNavFile(lang), err)
	}
	return &tr, nil
}

// localize returns the map with the translation's titles and summaries, the
// English ones where the translation has none. A page outside docs/ is linked
// from the translation's folder, one level deeper.
func (t *TranslatedNav) localize(nav *Nav) *Nav {
	out := &Nav{}
	for _, g := range nav.Groups {
		lg := Group{ID: g.ID, Title: g.Title, Summary: g.Summary}
		if tg, ok := t.Groups[g.ID]; ok {
			lg.Title, lg.Summary = orEnglish(tg.Title, g.Title), orEnglish(tg.Summary, g.Summary)
		}
		for _, p := range g.Pages {
			lp := p
			if tp, ok := t.Pages[p.Path]; ok {
				lp.Title, lp.Summary = orEnglish(tp.Title, p.Title), orEnglish(tp.Summary, p.Summary)
			}
			lg.Pages = append(lg.Pages, lp)
		}
		out.Groups = append(out.Groups, lg)
	}
	return out
}

func orEnglish(s, en string) string {
	if strings.TrimSpace(s) == "" {
		return en
	}
	return s
}

// RenderHubLang renders the navigation block of a translated hub
// (docs/<lang>/README.md): the translated titles, page links relative to
// the translation's folder, root pages one level up.
func RenderHubLang(nav *Nav, tr *TranslatedNav) string {
	local := tr.localize(nav)
	for gi := range local.Groups {
		for pi, p := range local.Groups[gi].Pages {
			if strings.HasPrefix(p.Path, "../") {
				local.Groups[gi].Pages[pi].Path = "../" + p.Path
			}
		}
	}
	return RenderHub(local)
}

var (
	stampRE          = regexp.MustCompile(`(?m)^<!-- docsgen:source sha256=([0-9a-f]+) -->[ \t]*$`)
	generatedBlockRE = regexp.MustCompile(`(?s)<!-- docsgen:([a-z]+):start -->.*?<!-- docsgen:[a-z]+:end -->`)
	trailingBlankRE  = regexp.MustCompile(`(?m)[ \t]+$`)
)

// normalizeSource is a page as a translation follows it: LF line ends, the
// stamp and the bodies of generated blocks removed, trailing blanks trimmed.
func normalizeSource(text string) string {
	s := strings.ReplaceAll(text, "\r\n", "\n")
	s = stampRE.ReplaceAllString(s, "")
	s = generatedBlockRE.ReplaceAllString(s, "<!-- docsgen:$1 -->")
	s = trailingBlankRE.ReplaceAllString(s, "")
	return strings.TrimRight(s, "\n")
}

// SourceDigest is the digest of an English page a translation records in its
// stamp: 16 hex characters of the SHA-256 of the normalised page, so a
// regenerated table or a CRLF checkout does not age a translation.
func SourceDigest(text string) string {
	sum := sha256.Sum256([]byte(normalizeSource(text)))
	return hex.EncodeToString(sum[:])[:16]
}

// StampLine is the last line of a translated page.
func StampLine(digest string) string { return "<!-- docsgen:source sha256=" + digest + " -->" }

// ReadStamp returns the digest a translated page records.
func ReadStamp(text string) (string, bool) {
	m := stampRE.FindStringSubmatch(strings.ReplaceAll(text, "\r\n", "\n"))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// SetStamp replaces the stamp of a translated page, or adds it as the last line.
func SetStamp(text, digest string) string {
	s := stampRE.ReplaceAllString(strings.ReplaceAll(text, "\r\n", "\n"), "")
	return strings.TrimRight(s, "\n") + "\n\n" + StampLine(digest) + "\n"
}

// sourceOf is the English file a translated file follows, "" for a file that
// is not a translation.
func sourceOf(rel string) (lang, source string) {
	rel = filepath.ToSlash(rel)
	for _, l := range Translations {
		if rest, ok := strings.CutPrefix(rel, TranslationDir(l)); ok && strings.HasSuffix(rest, ".md") {
			return l, "docs/" + rest
		}
	}
	return "", ""
}

// gitCmd runs git in root and returns what it printed on stdout: a warning
// on stderr must not become part of a file git shows.
func gitCmd(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.Output()
	return string(out), err
}

// sourceCommit is the newest commit whose version of an English page has the
// digest a stamp records: where a translation last caught up. Empty when
// git, the history (a shallow clone) or such a version is missing.
func sourceCommit(root, source, digest string) string {
	out, err := gitCmd(root, "log", "--format=%H", "-n", "200", "--", source)
	if err != nil {
		return ""
	}
	for _, c := range strings.Fields(out) {
		text, err := gitCmd(root, "show", c+":"+source)
		if err == nil && SourceDigest(text) == digest {
			return c
		}
	}
	return ""
}

// Stamp writes the stamp of each translated page: the digest of its English
// page as it is now. It refuses a page whose translation (stamp and generated
// blocks aside) is what HEAD has, since an English change then went
// untranslated, unless unchanged says the English change needs no
// translation. It returns a line per page for the person running it.
func Stamp(root string, files []string, unchanged bool) ([]string, error) {
	var report []string
	for _, f := range files {
		rel := filepath.ToSlash(filepath.Clean(f))
		lang, source := sourceOf(rel)
		if lang == "" {
			return report, fmt.Errorf("%s is not a page of a translation (docs/<lang>/...)", f)
		}
		ru, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return report, err
		}
		en, err := os.ReadFile(filepath.Join(root, source))
		if err != nil {
			return report, fmt.Errorf("%s: no English page %s", rel, source)
		}
		digest := SourceDigest(string(en))
		if old, ok := ReadStamp(string(ru)); ok && old == digest {
			report = append(report, rel+": already current")
			continue
		}
		if head, err := gitCmd(root, "show", "HEAD:"+rel); err == nil && normalizeSource(head) == normalizeSource(string(ru)) {
			if !unchanged {
				return report, fmt.Errorf("%s: the translation is the same as at HEAD while %s changed; translate the change first, or pass -unchanged when the English change needs no translation", rel, source)
			}
			line := rel + ": stamped unchanged"
			if old, ok := ReadStamp(string(ru)); ok {
				if c := sourceCommit(root, source, old); c != "" {
					line += "; the English change it accepts: git diff " + c[:12] + " -- " + source
				}
			}
			report = append(report, line)
		} else {
			report = append(report, rel+": stamped")
		}
		if err := os.WriteFile(filepath.Join(root, rel), []byte(SetStamp(string(ru), digest)), 0o644); err != nil {
			return report, err
		}
	}
	return report, nil
}

// translatedPages lists the files of a translation in map order: the hub,
// then the translation of every page of the map under docs/.
func translatedPages(nav *Nav, lang string) []string {
	out := []string{TranslationDir(lang) + "README.md"}
	for _, p := range nav.Pages() {
		if !strings.HasPrefix(p.Path, "../") {
			out = append(out, TranslationDir(lang)+filepath.ToSlash(filepath.Clean(p.Path)))
		}
	}
	return out
}

// staleMarker starts the message of a translation whose English page moved;
// the pre-commit hook reports these as warnings.
const staleMarker = "translates an older "

// IsStaleTranslation reports a problem that only says a translation is behind.
func IsStaleTranslation(p Problem) bool { return strings.Contains(p.Message, staleMarker) }

// CheckTranslation checks the translation of the documentation into lang
// against the English tree: every page and the hub translated, nothing else
// in the folder, the translated map in step with the English one, the same
// headings at the same levels, the stamp current, and the typography of the
// language. read returns a file as it will be after this run (generated
// content included); nil reads the disk.
func CheckTranslation(root string, nav *Nav, lang string, read func(rel string) (string, error)) []Problem {
	if read == nil {
		read = func(rel string) (string, error) {
			b, err := os.ReadFile(filepath.Join(root, rel))
			return string(b), err
		}
	}
	dir := TranslationDir(lang)
	var problems []Problem
	tr, err := LoadTranslatedNav(root, lang)
	if err != nil {
		problems = append(problems, Problem{TranslatedNavFile(lang), err.Error()})
		tr = &TranslatedNav{}
	} else {
		problems = append(problems, checkTranslatedNav(nav, tr, lang)...)
	}

	expected := map[string]string{dir + "README.md": HubFile} // translation -> source
	// A page whose English H1 is its title in the map has the translated
	// title as its H1; a page with a longer H1 of its own keeps that one,
	// translated.
	type titlePair struct{ en, tr string }
	titles := map[string]titlePair{}
	for _, p := range nav.Pages() {
		if strings.HasPrefix(p.Path, "../") {
			continue
		}
		rel := dir + filepath.ToSlash(filepath.Clean(p.Path))
		expected[rel] = RepoPath(p.Path)
		if t, ok := tr.Pages[p.Path]; ok && strings.TrimSpace(t.Title) != "" {
			titles[rel] = titlePair{p.Title, t.Title}
		}
	}
	var rels []string
	for rel := range expected {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		source := expected[rel]
		text, err := read(rel)
		if err != nil {
			problems = append(problems, Problem{rel, "missing: the " + lang + " translation of " + source})
			continue
		}
		en, err := read(source)
		if err != nil {
			continue // CheckNav reports the English page
		}
		startsWith := func(s, h1 string) bool {
			return strings.HasPrefix(strings.ReplaceAll(s, "\r\n", "\n"), "# "+h1+"\n")
		}
		if t, ok := titles[rel]; ok && startsWith(en, t.en) && !startsWith(text, t.tr) {
			problems = append(problems, Problem{rel, fmt.Sprintf("must start with \"# %s\", its title in %s", t.tr, TranslatedNavFile(lang))})
		} else if !strings.HasPrefix(text, "# ") {
			problems = append(problems, Problem{rel, "must start with an H1 title"})
		}
		problems = append(problems, checkHeadings(rel, source, text, en)...)
		if source == ChangelogFile {
			continue // generated from the English changelog, release notes as published
		}
		if stamp, ok := ReadStamp(text); !ok {
			problems = append(problems, Problem{rel, "no stamp: translate " + source + ", then stamp the page with make docs-stamp"})
		} else if digest := SourceDigest(en); stamp != digest {
			hint := " (the version it follows is not in the local history)"
			if c := sourceCommit(root, source, stamp); c != "" {
				hint = " (what changed since: git diff " + c[:12] + " -- " + source + ")"
			}
			problems = append(problems, Problem{rel, staleMarker + source + ": translate what changed in it" + hint})
		}
		problems = append(problems, lintTranslation(rel, lang, text)...)
	}

	// Nothing but translations and the translation's own files.
	_ = filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		switch {
		case translationMeta(lang)[rel]:
		case strings.HasSuffix(rel, ".md"):
			if _, ok := expected[rel]; !ok {
				problems = append(problems, Problem{rel, "no English page in " + NavFile + " for this translation"})
			}
		default:
			problems = append(problems, Problem{rel, "does not belong in " + dir + ": only translated pages, nav.yaml and config-descriptions.yaml"})
		}
		return nil
	})
	return problems
}

func checkTranslatedNav(nav *Nav, tr *TranslatedNav, lang string) []Problem {
	file := TranslatedNavFile(lang)
	var problems []Problem
	check := func(what string, t TranslatedText, title, summary string) {
		switch {
		case strings.TrimSpace(t.Title) == "" || strings.TrimSpace(t.Summary) == "":
			problems = append(problems, Problem{file, what + ": needs a title and a summary"})
		case strings.Contains(t.Title, "\n") || strings.Contains(t.Summary, "\n"):
			problems = append(problems, Problem{file, what + ": the title and the summary are one line each"})
		case t.En.Title != title || t.En.Summary != summary:
			problems = append(problems, Problem{file, fmt.Sprintf("%s: the English title or summary changed (now %q / %q): translate it and update en:", what, title, summary)})
		}
	}
	groups, pages := map[string]bool{}, map[string]bool{}
	for _, g := range nav.Groups {
		groups[g.ID] = true
		if t, ok := tr.Groups[g.ID]; ok {
			check("group "+g.ID, t, g.Title, g.Summary)
		} else {
			problems = append(problems, Problem{file, "no entry for group " + g.ID})
		}
		for _, p := range g.Pages {
			pages[p.Path] = true
			if t, ok := tr.Pages[p.Path]; ok {
				check("page "+p.Path, t, p.Title, p.Summary)
			} else {
				problems = append(problems, Problem{file, "no entry for page " + p.Path})
			}
		}
	}
	for id := range tr.Groups {
		if !groups[id] {
			problems = append(problems, Problem{file, "group " + id + " is not in " + NavFile})
		}
	}
	for p := range tr.Pages {
		if !pages[p] {
			problems = append(problems, Problem{file, "page " + p + " is not in " + NavFile})
		}
	}
	sort.Slice(problems, func(i, j int) bool { return problems[i].Message < problems[j].Message })
	return problems
}

// checkHeadings holds a translation to the outline of its source: the same
// sequence of heading levels, and no heading whose own anchor is the shared
// anchor of another one.
func checkHeadings(rel, source, text, en string) []Problem {
	local, shared := docs.ParseHeadings(text), docs.ParseHeadings(en)
	if len(local) != len(shared) {
		return []Problem{{rel, fmt.Sprintf("%d headings here, %d in %s: a translation keeps every heading of its source", len(local), len(shared), source)}}
	}
	var problems []Problem
	sharedAt := map[string]int{}
	for i, h := range shared {
		sharedAt[h.Anchor] = i
	}
	for i := range local {
		if local[i].Level != shared[i].Level {
			problems = append(problems, Problem{rel, fmt.Sprintf("heading %d (%q) is level %d, level %d in %s", i+1, local[i].Text, local[i].Level, shared[i].Level, source)})
			break
		}
		if j, ok := sharedAt[local[i].Local]; ok && j != i {
			problems = append(problems, Problem{rel, fmt.Sprintf("heading %d (%q): its own anchor #%s is the shared anchor of heading %d of %s, so it could not be named", i+1, local[i].Text, local[i].Local, j+1, source)})
		}
	}
	return problems
}

var (
	// russianForbiddenRE are the words the Russian wording rule forbids
	// (.claude/rules/russian-wording.md).
	russianForbiddenRE = regexp.MustCompile(`(?i)агентск\p{L}*|сабагент\p{L}*|рабоч\p{L}* дерев\p{L}*`)
	hrRE               = regexp.MustCompile(`^\s*(-{3,}|\*{3,}|_{3,})\s*$`)
	htmlCommentRE      = regexp.MustCompile(`<!--.*?-->`)
	typographyChars    = map[rune]string{
		'—': "an em dash", '–': "an en dash", '«': "a guillemet", '»': "a guillemet",
		'„': "a curly quote", '“': "a curly quote", '”': "a curly quote",
	}
)

// lintRussian checks one piece of Russian prose (no code in it) and returns
// what is wrong with it.
func lintRussian(prose string) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range prose {
		if what, ok := typographyChars[r]; ok && !seen[what] {
			seen[what] = true
			out = append(out, what+" (write a hyphen and straight quotes)")
		}
	}
	for _, w := range russianForbiddenRE.FindAllString(prose, -1) {
		out = append(out, fmt.Sprintf("%q: the Russian wording rule forbids it", w))
	}
	return out
}

// lintTranslation runs the typography check of a language over the prose of
// a translated page: code, comments and generated blocks aside.
func lintTranslation(rel, lang, text string) []Problem {
	if lang != docs.Russian {
		return nil
	}
	var problems []Problem
	text = generatedBlockRE.ReplaceAllStringFunc(text, func(block string) string {
		return strings.Repeat("\n", strings.Count(block, "\n"))
	})
	fence := false
	for i, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fence = !fence
			continue
		}
		if fence {
			continue
		}
		if hrRE.MatchString(line) {
			problems = append(problems, Problem{rel, fmt.Sprintf("line %d: a horizontal rule", i+1)})
			continue
		}
		prose := htmlCommentRE.ReplaceAllString(inlineCodeRE.ReplaceAllString(line, ""), "")
		for _, msg := range lintRussian(prose) {
			problems = append(problems, Problem{rel, fmt.Sprintf("line %d: %s", i+1, msg)})
		}
	}
	return problems
}

// configDescription is one entry of docs/<lang>/config-descriptions.yaml.
type configDescription struct {
	En string `yaml:"en"`
	Tr string `yaml:"ru"`
}

// LoadConfigDescriptions reads the translated descriptions of the schema,
// keyed like the rows of the configuration reference.
func LoadConfigDescriptions(root, lang string) (map[string]string, error) {
	entries, err := loadConfigDescriptionEntries(root, lang)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for k, e := range entries {
		if strings.TrimSpace(e.Tr) != "" {
			out[k] = e.Tr
		}
	}
	return out, nil
}

func loadConfigDescriptionEntries(root, lang string) (map[string]configDescription, error) {
	data, err := os.ReadFile(filepath.Join(root, ConfigDescriptionsFile(lang)))
	if err != nil {
		return nil, err
	}
	var entries map[string]configDescription
	if err := yaml.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("%s: %w", ConfigDescriptionsFile(lang), err)
	}
	return entries, nil
}

// CheckConfigDescriptions holds the translated descriptions to the schema:
// one entry for every description, its en: equal to the English text.
func CheckConfigDescriptions(root, lang string, schemaJSON []byte) []Problem {
	file := ConfigDescriptionsFile(lang)
	want, err := schemaDescriptions(schemaJSON)
	if err != nil {
		return []Problem{{file, err.Error()}}
	}
	entries, err := loadConfigDescriptionEntries(root, lang)
	if err != nil {
		return []Problem{{file, err.Error()}}
	}
	var problems []Problem
	for _, k := range want.keys {
		e, ok := entries[k]
		switch {
		case !ok || strings.TrimSpace(e.Tr) == "":
			problems = append(problems, Problem{file, k + ": no Russian description"})
		case e.En != want.text[k]:
			problems = append(problems, Problem{file, fmt.Sprintf("%s: the English description changed (now %q): translate it and update en:", k, want.text[k])})
		default:
			if lang == docs.Russian {
				for _, msg := range lintRussian(inlineCodeRE.ReplaceAllString(e.Tr, "")) {
					problems = append(problems, Problem{file, k + ": " + msg})
				}
			}
		}
	}
	var extra []string
	for k := range entries {
		if _, ok := want.text[k]; !ok {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		problems = append(problems, Problem{file, k + ": not a key of the schema"})
	}
	return problems
}

type descriptionSet struct {
	keys []string
	text map[string]string
}

// schemaDescriptions lists every description the configuration reference
// prints, keyed like its rows: a top-level section by its key, a field by its
// dotted path ("providers[].name").
func schemaDescriptions(schemaJSON []byte) (descriptionSet, error) {
	set := descriptionSet{text: map[string]string{}}
	var root schemaNode
	if err := json.Unmarshal(schemaJSON, &root); err != nil {
		return set, fmt.Errorf("schema: %w", err)
	}
	add := func(k, d string) {
		if d = strings.TrimSpace(d); d != "" {
			if _, dup := set.text[k]; !dup {
				set.keys = append(set.keys, k)
			}
			set.text[k] = d
		}
	}
	for _, key := range orderedTopLevel(root.Properties.Keys) {
		node := root.Properties.Nodes[key]
		add(key, node.Description)
		for _, r := range fieldRows(key, node, nil, true) {
			add(r.Key, r.Description)
		}
	}
	return set, nil
}

// changelogHeaders are the title and introduction of a translated changelog;
// the release notes under them are copied as published.
var changelogHeaders = map[string]string{
	docs.Russian: "# История изменений\n\nЗаметки к каждой опубликованной версии, собранные из [GitHub Releases](%s) репозитория командой `make docs-changelog`. `coddy update` печатает заметки версий, через которые перескакивает обновление, так что ответ на вопрос \"что изменилось\" после обновления виден и на экране. Тексты релизов приводятся так, как они опубликованы, на английском.\n",
}

var releasesURLRE = regexp.MustCompile(`https://github\.com/[^/\s)]+/[^/\s)]+/releases`)

// RussianChangelog derives the Russian changelog from the English one: a
// Russian title and introduction over the release notes as published.
func RussianChangelog(en string) string { return translatedChangelog(docs.Russian, en) }

func translatedChangelog(lang, en string) string {
	en = strings.ReplaceAll(en, "\r\n", "\n")
	url := "https://github.com/coddy-project/coddy-agent/releases"
	if m := releasesURLRE.FindString(en); m != "" {
		url = m
	}
	header := fmt.Sprintf(changelogHeaders[lang], url)
	if i := strings.Index(en, "\n## "); i >= 0 {
		return header + en[i:]
	}
	return header
}
