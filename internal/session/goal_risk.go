package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// The supervisor's rules over the evidence of a goal's turns, in one table:
// each kind is advisory - the check weighs its red flag - or hard - it blocks
// the session's goal right after the turn that did it, before anything else
// reads the turn (goalRun.gate). The digest's red flags and the gate read the
// same table, so a rule is named the same way in both.
//
// The evidence of the session's goal is the rows stored while it was active,
// which carry its id (llm.Message.Goal): a rewind takes rows away, its undo
// brings them back with what they carry, and a new goal has a new id. Rows of
// a goal set by an older binary carry none and are not its evidence. The
// implicit goal of supervisor.enable reads the rows from the latest request on
// and is never blocked.

// goalRiskSeverity is what a rule's finding does.
type goalRiskSeverity int

const (
	// goalRiskAdvisory is listed for the check, which weighs it.
	goalRiskAdvisory goalRiskSeverity = iota
	// goalRiskHard blocks the session's goal until /goal resume accepts it;
	// the implicit goal of supervisor.enable only lists it.
	goalRiskHard
)

// goalRiskKind is one rule.
type goalRiskKind int

const (
	// testFilesChanged: a write tool changed tests, test data or CI files.
	testFilesChanged goalRiskKind = iota
	// skipMarkerAdded: a write tool added a skip or suppression marker.
	skipMarkerAdded
	// dockerMutation: a run_command call that ran changes docker state
	// (dockerMutates).
	dockerMutation
	// migrationChanged: a migration file that existed in the goal's start
	// commit differs from its state when the goal was set, however it was
	// changed (gitws.ChangedSince).
	migrationChanged
)

// goalRiskRules is the table: what each kind does and the flag line that
// names it, %s being what it saw.
var goalRiskRules = [...]struct {
	severity goalRiskSeverity
	flag     string
}{
	testFilesChanged: {goalRiskAdvisory, "tests, test data or CI files were changed: %s - check that the change serves the objective rather than loosening a check"},
	skipMarkerAdded:  {goalRiskAdvisory, "calls add a skip or suppression marker: %s"},
	dockerMutation:   {goalRiskHard, "run_command changed docker containers or volumes: %s"},
	migrationChanged: {goalRiskHard, "migration files that existed when the goal started were changed: %s"},
}

const (
	// goalRiskQuote bounds a command or a call's arguments a flag quotes.
	goalRiskQuote = 200
	// goalRiskListed bounds how many findings one flag line names; the rest
	// is counted.
	goalRiskListed = 20
	// goalGitTimeout bounds each git look at the workspace: the start record
	// when a goal is set, and the migration rule after a goal turn and at
	// /goal resume.
	goalGitTimeout = 10 * time.Second
)

// migrationFolders are the folders of migrationChanged, matched at any depth.
var migrationFolders = []string{"migrations", "alembic/versions", "db/migrate", "db/changelog"}

// dockerVerbs are the words that make a docker command a mutation.
var dockerVerbs = []string{"up", "down", "rm", "stop", "restart", "create", "run", "kill", "prune"}

// The results the agent records for a call that never ran: refused by a
// person, a hook, the session's mode, toolset or turn restriction, or a
// subagent's tool set, refused by the decisions check of bypass mode, or
// skipped when its turn stopped. internal/agent writes them from these
// constants, so the rules read the spelling it writes, and the texts are sent
// to the model as they stand: changing one moves the provider's prompt cache.
// A call that ran and failed - a tool's own error included - is none of them,
// and neither is a call whose result was never recorded or was written by the
// tool-pairing repair ("the call may or may not have run"). The loop guard's
// nudge stands in a result's place for a repeated call it did not execute.
//
// The formats take what the text names (the tool, the toolset, the path). The
// subagent and turn-restriction texts are errors the agent returns, which the
// transcript records behind the "error: " every returned error gets. A new
// text joins the table below with the constant the agent writes it from. A
// command that prints one of these texts as its own output is taken for a call
// that did not run.
const (
	ToolResultDeniedByUser           = "permission denied by user"
	ToolResultNotGrantedPrefix       = "permission not granted: "
	ToolResultHookBlockedPrefix      = "blocked by hook: "
	ToolResultNotExecutedPrefix      = "not executed: "
	ToolResultCancelledBeforeRun     = "cancelled before the tool ran"
	ToolResultUnsafePrefix           = "command rejected as unsafe: "
	ToolResultCommandNotRunPrefix    = "command not executed: "
	ToolResultHookChangedArguments   = "cancelled: a hook changed the approved arguments after the approval; run the call again"
	ToolResultArgsNotPersistedPrefix = "cancelled: the arguments could not be persisted before the permission prompt: "
	ToolResultLoopGuardNudge         = "You have requested the same tool call with identical arguments several times in a row, so it was not executed again. Repeating it will not produce a different result. Use what you already have: try a different tool or different arguments, or answer the user with the information you have."

	// The tool name (%q) of a call the session's mode does not allow.
	ToolResultPlanModeFormat = "error: tool %q is not available in Plan mode because it is outside the plan tool set; the user can switch to Agent mode to use it"
	ToolResultAskModeFormat  = "error: tool %q is not available in Ask mode because it is not read-only"
	// The tool name and the toolset (%q, %q) the session runs with.
	ToolResultToolsetFormat = "error: tool %q is not offered: this session runs with the toolset %q. Use one of the tools you were given."
	// The tool name (%s) of a call outside a subagent's tool set.
	ToolResultSubagentToolFormat = "tool %s is not available to this subagent"
	// What a turn restriction tells the model about a refused call, after the
	// tool name: the note it gives when the restriction has none of its own,
	// and the admin-only note of a messenger bot's non-admin turn. A surface
	// that sets another Note adds it to the table.
	ToolResultTurnNoteDefault   = "this tool is not available in this turn"
	ToolResultTurnNoteAdminOnly = "only the bot's admins can do this"
	// The tool name, the note and the path (%s, %s, %s) of a call that reads
	// outside the working directory of a confined turn.
	ToolResultOutsideWorkspaceFormat = "%s: %s (%s is outside the session's working directory)"
)

// toolErrorLead opens the record of an error a tool call returned.
const toolErrorLead = "error: "

// What toolCallRan reads: a result that is exactly one of the texts, starts
// with one of the prefixes, or is what one of the formats prints for some
// arguments.
var (
	notRunTexts = []string{
		ToolResultDeniedByUser, ToolResultCancelledBeforeRun, ToolResultHookChangedArguments, ToolResultLoopGuardNudge,
	}
	notRunPrefixes = []string{
		ToolResultNotGrantedPrefix, ToolResultHookBlockedPrefix, ToolResultNotExecutedPrefix,
		ToolResultUnsafePrefix, ToolResultCommandNotRunPrefix, ToolResultArgsNotPersistedPrefix,
	}
	notRunFormats = []string{
		ToolResultPlanModeFormat, ToolResultAskModeFormat, ToolResultToolsetFormat,
		toolErrorLead + ToolResultSubagentToolFormat,
		toolErrorLead + "%s: " + ToolResultTurnNoteDefault,
		toolErrorLead + "%s: " + ToolResultTurnNoteAdminOnly,
		toolErrorLead + ToolResultOutsideWorkspaceFormat,
	}
)

// toolCallRan reports whether a call's recorded result says it ran.
func toolCallRan(result string) bool {
	r := strings.TrimSpace(result)
	if slices.Contains(notRunTexts, r) {
		return false
	}
	for _, prefix := range notRunPrefixes {
		if strings.HasPrefix(r, prefix) {
			return false
		}
	}
	for _, format := range notRunFormats {
		if matchesFormat(r, format) {
			return false
		}
	}
	return true
}

// matchesFormat reports whether s is what a format of %s and %q verbs prints
// for some arguments: the text before the first verb at its start, the text
// after the last at its end, and the text between the verbs in order inside.
func matchesFormat(s, format string) bool {
	var parts []string
	for {
		i := strings.IndexByte(format, '%')
		if i < 0 {
			parts = append(parts, format)
			break
		}
		parts = append(parts, format[:i])
		format = format[i+2:]
	}
	first, last := parts[0], parts[len(parts)-1]
	if len(s) < len(first)+len(last) || !strings.HasPrefix(s, first) || !strings.HasSuffix(s, last) {
		return false
	}
	s = s[len(first) : len(s)-len(last)]
	for _, mid := range parts[1 : len(parts)-1] {
		i := strings.Index(s, mid)
		if i < 0 {
			return false
		}
		s = s[i+len(mid):]
	}
	return true
}

// dockerMutates reports whether a command line runs a docker step that
// changes containers, volumes, images or networks. The reading is lexical and
// deliberately coarse: case, quote characters and backslashes are dropped (a
// backslash at the end of a line joins it to the next), the line is cut into
// commands at ; & | and line breaks, and a command matches when one of its
// words is docker or docker-compose and another is one of dockerVerbs. A word
// is a run of letters, digits and - _ . /, read from its last /: /usr/bin/docker,
// >/tmp/x docker and (docker each hold the word docker, while docker/tmp and
// docker-compose.yml do not. Quoted text matches like a command,
// --help and --dry-run exempt nothing, and what a script, an alias, a
// variable or a sh -c string runs is read only as far as its words show.
func dockerMutates(line string) bool {
	line = dockerLineCleaner.Replace(strings.ToLower(line))
	for _, command := range strings.FieldsFunc(line, func(r rune) bool { return strings.ContainsRune(";&|\n", r) }) {
		words := strings.FieldsFunc(command, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("-_./", r)
		})
		for i, w := range words {
			words[i] = w[strings.LastIndexByte(w, '/')+1:]
		}
		if (slices.Contains(words, "docker") || slices.Contains(words, "docker-compose")) &&
			slices.ContainsFunc(words, func(w string) bool { return slices.Contains(dockerVerbs, w) }) {
			return true
		}
	}
	return false
}

var dockerLineCleaner = strings.NewReplacer("\\\r\n", " ", "\\\n", " ", `\`, "", `"`, "", `'`, "")

// dockerCommands are the commands of the run_command calls of the assistant
// row msgs[i] that change docker state and ran, clipped for a flag. A call
// whose recorded result says it never ran does not count; one without a
// result may have run before its turn broke off, and does.
func dockerCommands(msgs []llm.Message, i int) []string {
	if msgs[i].Role != llm.RoleAssistant {
		return nil
	}
	var commands []string
	var results map[string]string
	for _, call := range msgs[i].ToolCalls {
		var in struct {
			Command string `json:"command"`
		}
		if call.Name != "run_command" || json.Unmarshal([]byte(call.InputJSON), &in) != nil || !dockerMutates(in.Command) {
			continue
		}
		if results == nil {
			results = callResults(msgs, i)
		}
		if result, ok := results[call.ID]; ok && !toolCallRan(result) {
			continue
		}
		commands = append(commands, clipRunes(compactSpace(in.Command), goalRiskQuote))
	}
	return commands
}

// callResults maps the calls of the assistant message at i to the tool
// results recorded right after it: a provider may reuse a call id in later
// turns, so a result is read only next to its own call. A row only the UI
// shows (the plan snapshot plan_write leaves) can sit between them and is
// skipped; any other row ends the run of results.
func callResults(msgs []llm.Message, i int) map[string]string {
	out := map[string]string{}
	for _, m := range msgs[i+1:] {
		if isUIOnlyRow(m) {
			continue
		}
		if m.Role != llm.RoleTool {
			break
		}
		if _, seen := out[m.ToolCallID]; !seen {
			out[m.ToolCallID] = m.Content
		}
	}
	return out
}

// GoalStart is the workspace a goal was set in: the folder, the commit HEAD
// named, and the migration files the working copy already held changed then,
// so the operator's own pending edits are not taken for the goal's. A folder
// that was no git checkout is the folder alone, with no commit: the migration
// rule does not apply to it.
type GoalStart struct {
	Dir string `json:"dir"`
	gitws.FileBaseline
}

// goalStartFor records the workspace a new goal starts from: nil when git
// cannot say (no git binary, no commit yet, no answer in time), and the
// migration rule is reported unavailable for that goal.
func goalStartFor(dir string) *GoalStart {
	ctx, cancel := context.WithTimeout(context.Background(), goalGitTimeout)
	defer cancel()
	base, err := gitws.Baseline(ctx, dir, migrationFolders)
	switch {
	case errors.Is(err, gitws.ErrNotRepository):
		return &GoalStart{Dir: dir}
	case err != nil:
		return nil
	}
	return &GoalStart{Dir: dir, FileBaseline: base}
}

// goalFiles is what the migration rule found: each changed file with the hash
// of its content now ("" when it is gone), or why it could not look.
type goalFiles struct {
	changed     map[string]string
	unavailable string
}

// goalMigrationChanges compares the workspace the session's goal was set in
// with the goal's start through git, without the state lock and bounded by
// goalGitTimeout. A goal set outside a git checkout has nothing to compare; a
// goal without a start record or a comparison that fails is reported
// unavailable, never as no change.
func goalMigrationChanges(ctx context.Context, goal GoalState) goalFiles {
	switch {
	case goal.Start == nil:
		return goalFiles{unavailable: "no start commit was recorded when the goal was set"}
	case goal.Start.Head == "":
		return goalFiles{}
	}
	ctx, cancel := context.WithTimeout(ctx, goalGitTimeout)
	defer cancel()
	changed, err := gitws.ChangedSince(ctx, goal.Start.Dir, goal.Start.FileBaseline, migrationFolders)
	if err != nil {
		return goalFiles{unavailable: err.Error()}
	}
	return goalFiles{changed: changed}
}

// goalFinding is one thing a rule saw: subject is what the flag quotes (a
// path, a call's arguments, a command), acknowledged that /goal resume
// accepted it.
type goalFinding struct {
	kind         goalRiskKind
	subject      string
	acknowledged bool
}

// goalFindings applies the rules to the goal's rows of msgs and to what the
// migration rule found, and lists the paths the write tools named, sorted.
func goalFindings(msgs []llm.Message, rows []int, goal GoalState, files goalFiles) (changed []string, findings []goalFinding) {
	for _, i := range rows {
		for _, call := range msgs[i].ToolCalls {
			if !writeTools[call.Name] {
				continue
			}
			for _, p := range callPaths(call.InputJSON) {
				if !slices.Contains(changed, p) {
					changed = append(changed, p)
				}
			}
			if skipMarkers.MatchString(call.InputJSON) {
				findings = append(findings, goalFinding{kind: skipMarkerAdded, subject: call.Name + " `" + clipRunes(compactSpace(call.InputJSON), goalRiskQuote) + "`"})
			}
		}
		for _, command := range dockerCommands(msgs, i) {
			findings = append(findings, goalFinding{kind: dockerMutation, subject: "`" + command + "`", acknowledged: msgs[i].RiskAcknowledged})
		}
	}
	slices.Sort(changed)
	for _, p := range changed {
		if testPathPattern.MatchString(filepath.ToSlash(p)) {
			findings = append(findings, goalFinding{kind: testFilesChanged, subject: p})
		}
	}
	paths := make([]string, 0, len(files.changed))
	for p := range files.changed {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		accepted, ok := goal.AcceptedFiles[p]
		findings = append(findings, goalFinding{kind: migrationChanged, subject: p, acknowledged: ok && accepted == files.changed[p]})
	}
	return changed, findings
}

// flagLines renders findings with the table's lines, one per kind in the
// table's order; marked adds what /goal resume acknowledged.
func flagLines(findings []goalFinding, marked bool) []string {
	var lines []string
	for kind, rule := range goalRiskRules {
		var subjects []string
		for _, f := range findings {
			if f.kind != goalRiskKind(kind) {
				continue
			}
			s := f.subject
			if marked && f.acknowledged {
				s += " (acknowledged with /goal resume)"
			}
			if !slices.Contains(subjects, s) {
				subjects = append(subjects, s)
			}
		}
		if len(subjects) == 0 {
			continue
		}
		if extra := len(subjects) - goalRiskListed; extra > 0 {
			subjects = append(subjects[:goalRiskListed], fmt.Sprintf("and %d more", extra))
		}
		lines = append(lines, fmt.Sprintf(rule.flag, strings.Join(subjects, ", ")))
	}
	return lines
}

// stampGoalLocked gives a row stored while the session's goal is active that
// goal's id. The caller holds s.mu.
func (s *State) stampGoalLocked(msg *llm.Message) {
	if s.goal.Active() {
		msg.Goal = s.goal.ID
	}
}

// acceptGoalRisksLocked accepts what the risk gate blocks goal on: it marks
// the goal's rows whose docker steps count and records each changed migration
// file at its content now. The caller holds s.mu; goal is the copy the caller
// is about to install.
func (s *State) acceptGoalRisksLocked(goal *GoalState, files goalFiles) {
	for i := range s.Messages {
		if goal.ID != "" && s.Messages[i].Goal == goal.ID && !s.Messages[i].RiskAcknowledged && len(dockerCommands(s.Messages, i)) > 0 {
			s.Messages[i].RiskAcknowledged = true
			s.markMessagesEdited()
		}
	}
	for p, h := range files.changed {
		if goal.AcceptedFiles == nil {
			goal.AcceptedFiles = map[string]string{}
		}
		goal.AcceptedFiles[p] = h
	}
}
