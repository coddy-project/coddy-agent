package docs

import (
	"strings"
	"sync"
	"unicode"

	docsfs "github.com/EvilFreelancer/coddy-agent/docs"
	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

// The languages the documentation is written in. English is the source: its
// map orders every library, and a page a translation lacks is read in English.
const (
	English = "en"
	Russian = "ru"
)

// Languages lists every language of the documentation, the source first.
var Languages = []string{English, Russian}

// Lang is the documentation language of a locale tag, whatever its spelling:
// "ru", "ru-RU", "ru_RU.UTF-8" are Russian. A language without a translation,
// an empty tag and the C locale are English.
func Lang(tag string) string {
	t := strings.ToLower(strings.TrimSpace(tag))
	if i := strings.IndexAny(t, "-_.@"); i >= 0 {
		t = t[:i]
	}
	for _, l := range Languages {
		if t == l {
			return l
		}
	}
	return English
}

// LangEnv names Coddy's own override of the documentation language, read
// before the locale variables: the way to choose on a system that sets none
// of them (Windows), or to read the documentation in another language than
// the terminal speaks.
const LangEnv = "CODDY_LANG"

// LangFromEnv is the documentation language of a terminal: CODDY_LANG when it
// is set, then the first of LC_ALL, LC_MESSAGES and LANG that is set, the order
// POSIX gives them.
func LangFromEnv(getenv func(string) string) string {
	for _, key := range []string{LangEnv, "LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return Lang(v)
		}
	}
	return English
}

// LangOfText is the documentation language a piece of text is written in: a
// text with a Cyrillic letter is Russian, anything else English. It decides
// for a search query and a prompt whose surface named no language.
func LangOfText(s string) string {
	for _, r := range s {
		if unicode.Is(unicode.Cyrillic, r) {
			return Russian
		}
	}
	return English
}

// translatedNav is <lang>/nav.yaml: the titles and summaries of a translation,
// keyed by group id and page path. Order and membership come from the source
// map alone.
type translatedNav struct {
	Groups map[string]navText `yaml:"groups"`
	Pages  map[string]navText `yaml:"pages"`
}

type navText struct {
	Title   string `yaml:"title"`
	Summary string `yaml:"summary"`
}

var (
	libsMu sync.Mutex
	libs   = map[string]*libraryOnce{}
)

type libraryOnce struct {
	once sync.Once
	lib  *Library
	err  error
}

// For is the documentation embedded in this binary in a language (Lang
// applied to it), each library loaded once.
func For(lang string) (*Library, error) {
	lang = Lang(lang)
	libsMu.Lock()
	lo := libs[lang]
	if lo == nil {
		lo = &libraryOnce{}
		libs[lang] = lo
	}
	libsMu.Unlock()
	lo.once.Do(func() {
		lo.lib, lo.err = LoadLang(docsfs.FS, version.Get(), lang)
	})
	return lo.lib, lo.err
}

// siteBase is the public address of the pages of a language.
func siteBase(lang string) string {
	if lang == English {
		return SiteBase
	}
	return "https://coddy.dev/" + lang + "/docs/"
}
