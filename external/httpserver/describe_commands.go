//go:build http

package httpserver

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
)

// describeMaxTokens is the output cap of the naming call: a title is a short
// phrase and tags are one line, so neither route lets the model write an essay
// for a label. The default path clamps to the same number inline.
const describeMaxTokens = 96

func describeTokenLimit(cfg *config.Config, model string) int {
	if entry := cfg.FindModelEntry(model); entry != nil && entry.MaxTokens > 0 && entry.MaxTokens < describeMaxTokens {
		return entry.MaxTokens
	}
	return describeMaxTokens
}

// describeProvider names the conversation with the model the chat itself talks
// to, so a first message is titled by the model its operator chose. Choosing
// from the configuration alone (providerFactory) takes the first model of the
// list, and that one's credential is the least likely to have been refreshed:
// one dead token used to leave every new conversation unlabelled, with no title
// and no tags, while the chats went on working on their own models.
//
// The default answers for a request that names no session, and for a session
// whose model cannot be built: naming a chat is a convenience, so it costs
// another try rather than the label.
func (s *Server) describeProvider(r *http.Request, requestedModel, text string) (llm.Provider, error) {
	cfg := s.activeCfg()
	if cfg == nil {
		return nil, fmt.Errorf("config unavailable")
	}
	if line, err := session.ParseSettingsCommands(text); err == nil {
		if line.Session.Model != nil && cfg.FindModelEntry(*line.Session.Model) != nil {
			requestedModel = *line.Session.Model
		}
		for _, change := range line.Turns {
			if change.Model != nil && cfg.FindModelEntry(*change.Model) != nil {
				requestedModel = *change.Model
			}
		}
	}
	model := strings.TrimSpace(requestedModel)
	if cfg.ModelRunsACPAgent(model) {
		model = cfg.FirstNonACPModelID()
	}
	if model != "" {
		maxTok := describeTokenLimit(cfg, model)
		if p, err := s.makeLLMFromYAML(cfg, model, llm.RequestOptions{MaxTokens: &maxTok}); err == nil {
			return p, nil
		} else {
			s.log.Debug("describe requested model", "model", model, "error", err)
		}
	}
	if p, ok := s.describeSessionProvider(r, cfg); ok {
		return p, nil
	}
	return s.providerFactory(cfg)
}

// describeSessionProvider builds the provider of the session named by the
// request's header, reporting whether one was usable. An acp agent answers
// inside a session only and would take the naming request for a task, so such
// a model borrows one of another type the way the default does.
func (s *Server) describeSessionProvider(r *http.Request, cfg *config.Config) (llm.Provider, bool) {
	sessionID := strings.TrimSpace(r.Header.Get("X-Coddy-Session-ID"))
	if sessionID == "" || s.mgr == nil {
		return nil, false
	}
	if err := session.ValidateFolderSessionID(sessionID); err != nil {
		return nil, false
	}
	st := s.mgr.SessionByID(sessionID)
	if st == nil {
		return nil, false
	}
	modelID := effectiveYAMLModel(cfg, st)
	if modelID == "" {
		return nil, false
	}
	if cfg.ModelRunsACPAgent(modelID) {
		if modelID = cfg.FirstNonACPModelID(); modelID == "" {
			return nil, false
		}
	}
	// A name and its tags are one short line: the same cap the default path
	// sends, so neither route lets the model write an essay for a title.
	maxTok := describeTokenLimit(cfg, modelID)
	provider, err := s.makeLLMFromYAML(cfg, modelID, llm.RequestOptions{MaxTokens: &maxTok})
	if err != nil {
		// A model the session selected but the configuration no longer
		// holds: the default gets the turn rather than the chat going
		// unlabelled, and the journal says which model was asked for.
		s.log.Debug("describe session model", "session", sessionID, "model", modelID, "error", err)
		return nil, false
	}
	return provider, true
}

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
	"for filing the conversation - one or two words each, lower case, in English. Output nothing else.\n\n" +
	describeRequestGuidance

// describeRequestGuidance tells the title model what it does not see: a first
// message may come with images or files, and only its words reach this call.
// A model left to guess answers a question like "What is in this picture?" -
// or complains that no picture came - instead of naming it.
const describeRequestGuidance = "You see only the words of the message: images, files and other attachments sent with it are not shown to you. " +
	"Do not answer, refuse or comment on the message, and never say that something is missing - " +
	"name the request from its words, for example \"What is in this picture?\" becomes \"What the picture shows\"."

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
		abs := filepath.Clean(raw)                             // nosemgrep: go.lang.security.filepath-clean-misuse.filepath-clean-misuse
		if fi, err := os.Stat(abs); err == nil && fi.IsDir() { //nolint:gosec // G703: the cwd query names any folder on purpose, read for its skill catalog as /coddy/slash-commands reads it
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
	// Quotes, backticks and emphasis wrap the word, punctuation follows it, in
	// any order ("`/x`:", "\"/x\","): both are taken off until neither is
	// left. An underscore is not among them, since a command name may end in
	// one.
	w := word
	for {
		next := strings.TrimRight(strings.Trim(w, "`\"'*"), ",.;:!?")
		if next == w {
			break
		}
		w = next
	}
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
		rest := words[lead:]
		if lead > 0 {
			// The separator a model puts after the command it led with
			// ("/x - ...", "/x: ...") goes with it.
			for len(rest) > 0 && strings.Trim(rest[0], "-–—:|") == "" {
				rest = rest[1:]
			}
		}
		switch {
		case lead == 0:
			kept = append(kept, line)
		case len(rest) > 0:
			kept = append(kept, strings.Join(rest, " "))
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
