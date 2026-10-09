//go:build http

package httpserver

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
)

// A first message that invokes a slash command carries no words to name a
// conversation by: "/rpa-init" alone is a token the model can only repeat or
// guess at (issue #435). The describe call therefore tells the model what each
// command the message invokes does, from the catalog of the chat's own
// workspace, and keeps a command token out of the title it picks.

const (
	// describeCommandsHeading opens the list of invoked commands in the title
	// prompt.
	describeCommandsHeading = "Commands the text invokes:"
	// describeMaxCommands bounds how many invoked commands the prompt lists.
	describeMaxCommands = 5
	// describeCommandDescriptionRunes bounds each command's description: a
	// skill's is written for the agent that picks the skill and can run long,
	// and the title needs its gist.
	describeCommandDescriptionRunes = 280
)

// describeBasePrompt is the title prompt every describe call starts from.
const describeBasePrompt = "You generate short descriptions for chat titles and command labels. " +
	"Return exactly one short phrase (3 to 8 words) describing what the user's text is about. " +
	"Match the user's language when possible. " +
	"No quotes, no preamble, no headings, no numbering. " +
	"Then, on a second line, write " + describeTagsPrefix + " followed by 1 to 3 comma separated topic labels " +
	"for filing the conversation - one or two words each, lower case, in English. Output nothing else."

// describeCommandsGuidance precedes the list of invoked commands.
const describeCommandsGuidance = "The text invokes slash commands: a word written as /name runs that command and is not the topic. " +
	"Name the conversation by what the commands do, as described below, together with the user's own words around them. " +
	"Never answer with a command name."

// describedCommand is one command a text invokes, as the catalog of its
// workspace describes it.
type describedCommand struct {
	Name        string
	Description string
}

// describePromptText is the part of a first message that says what the work
// is: the settings commands at its start (/model, /plan, ...) configure the
// session, never reach the history and say nothing about the work, so they are
// taken off. ok is false for a text made of nothing else - there is nothing to
// name.
func describePromptText(raw string) (text string, ok bool) {
	line, err := session.ParseSettingsCommands(raw)
	if err != nil || line.Empty() {
		return raw, true
	}
	rest := strings.TrimSpace(line.Rest)
	return rest, rest != ""
}

// describeWorkspace names the folder whose commands a describe call reads: the
// session the header names when the server holds it, else the folder the cwd
// query names (a new chat's pick, before its session exists on the server),
// else the server's default. Unlike a listing it never refuses: the commands
// only help the model name the chat, and a folder the server cannot use is no
// reason to leave the chat unnamed.
func (s *Server) describeWorkspace(r *http.Request) string {
	if sid := strings.TrimSpace(r.Header.Get("X-Coddy-Session-ID")); sid != "" && s.mgr != nil {
		if session.ValidateFolderSessionID(sid) == nil {
			if st := s.mgr.SessionByID(sid); st != nil {
				if abs, err := filepath.Abs(st.GetCWD()); err == nil {
					return abs
				}
			}
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("cwd")); raw != "" && filepath.IsAbs(raw) {
		// Clean only normalises the spelling; the folder is read for its skill
		// catalog, the same reach /coddy/slash-commands has.
		abs := filepath.Clean(raw) // nosemgrep: go.lang.security.filepath-clean-misuse.filepath-clean-misuse
		if fi, err := os.Stat(abs); err == nil && fi.IsDir() {
			return abs
		}
	}
	cwd, err := session.EffectiveSessionCWD("", s.defaultCWD)
	if err != nil {
		return ""
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}
	return abs
}

// describeInvokedCommands resolves the /name words of text to the commands
// the workspace knows - the built-in actions first, as the turn dispatches
// them, then the skills - in the order they appear. A slash word that names
// nothing in the catalog (a path, an unknown name) is prose and is left out.
func (s *Server) describeInvokedCommands(r *http.Request, text string) []describedCommand {
	names := skills.ParseInvokedCommandNames(text)
	if len(names) == 0 {
		return nil
	}
	known := make(map[string]string)
	cwd := s.describeWorkspace(r)
	if cwd != "" {
		sums, err := s.listSkillSummariesCached(cwd)
		if err != nil {
			s.log.Debug("describe skill catalog", "cwd", cwd, "error", err)
		}
		for _, sum := range sums {
			known[sum.Name] = sum.Description
		}
	}
	for _, row := range session.ActionCommandRows(s.activeCfg()) {
		known[row.Name] = row.Description
	}
	out := make([]describedCommand, 0, min(len(names), describeMaxCommands))
	for _, n := range names {
		desc, ok := known[n]
		if !ok {
			continue
		}
		out = append(out, describedCommand{Name: n, Description: describeClampDescription(desc)})
		if len(out) == describeMaxCommands {
			break
		}
	}
	return out
}

// describeClampDescription folds a description onto one line and cuts it to
// describeCommandDescriptionRunes.
func describeClampDescription(desc string) string {
	one := strings.Join(strings.Fields(desc), " ")
	if utf8.RuneCountInString(one) <= describeCommandDescriptionRunes {
		return one
	}
	return strings.TrimSpace(string([]rune(one)[:describeCommandDescriptionRunes])) + "..."
}

// describeSystemPrompt is the title prompt, with the invoked commands listed
// after it when the text names any.
func describeSystemPrompt(commands []describedCommand) string {
	if len(commands) == 0 {
		return describeBasePrompt
	}
	var b strings.Builder
	b.WriteString(describeBasePrompt)
	b.WriteString("\n\n")
	b.WriteString(describeCommandsGuidance)
	b.WriteString("\n")
	b.WriteString(describeCommandsHeading)
	for _, c := range commands {
		b.WriteString("\n- /")
		b.WriteString(c.Name)
		b.WriteString(": ")
		b.WriteString(c.Description)
	}
	return b.String()
}

// describePickLinkRE is the picker's legacy insertion form of a command,
// [/name](coddy-skill:name).
var describePickLinkRE = regexp.MustCompile(`^\[/([a-zA-Z0-9][a-zA-Z0-9_-]*)\]\(coddy-skill:([a-zA-Z0-9][a-zA-Z0-9_-]*)\)$`)

// describeIsCommandWord reports whether a word of the text, or of a model's
// answer, is one of the invoked commands: written as /name in any case,
// quoted or in backticks, in the picker's link form, punctuation after it
// allowed.
func describeIsCommandWord(word string, commands []describedCommand) bool {
	w := strings.Trim(word, "`\"'*_")
	w = strings.TrimRight(w, ",.;:!?")
	name := ""
	if m := describePickLinkRE.FindStringSubmatch(w); m != nil && m[1] == m[2] {
		name = m[1]
	} else if w = strings.TrimRight(w, ")"); strings.HasPrefix(w, "/") {
		name = w[1:]
	}
	if name == "" {
		return false
	}
	for _, c := range commands {
		if strings.EqualFold(c.Name, name) {
			return true
		}
	}
	return false
}

// describeDropCommandEchoes takes the invoked commands off the head of every
// line of a model answer, and drops a line that was nothing else: a model
// that answers "/rpa-init" has named nothing, and one that answers
// "/rpa-init Repository onboarding" named the chat by the words after it.
func describeDropCommandEchoes(answer string, commands []describedCommand) string {
	if len(commands) == 0 {
		return answer
	}
	kept := make([]string, 0, 2)
	for _, line := range strings.Split(answer, "\n") {
		words := strings.Fields(describeStripLineNoise(line))
		lead := 0
		for lead < len(words) && describeIsCommandWord(words[lead], commands) {
			lead++
		}
		switch {
		case lead == 0:
			kept = append(kept, line)
		case lead < len(words):
			kept = append(kept, strings.Join(words[lead:], " "))
		}
	}
	return strings.Join(kept, "\n")
}

// describeFallbackWords are the words a title falls back to when the model
// gives nothing usable: the user's own words around the invoked commands, or,
// for a command typed alone, the command as typed - the last thing left.
func describeFallbackWords(text string, commands []describedCommand) []string {
	all := strings.Fields(text)
	if len(commands) == 0 {
		return all
	}
	rest := make([]string, 0, len(all))
	for _, w := range all {
		if !describeIsCommandWord(w, commands) {
			rest = append(rest, w)
		}
	}
	if len(rest) == 0 {
		return all
	}
	return rest
}
