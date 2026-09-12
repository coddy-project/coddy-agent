package config

// The -t / --test-config check: the file the loader would read, validated
// against the embedded JSON Schema and then against the loader's own rules,
// with every problem located in the file and explained. Nothing is written.
// A normal load falls back to config.yaml.bak and rewrites the file when the
// document is broken; a check that did the same would hide the very problem
// it is asked to report.

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Severity tells whether a finding fails the check.
type Severity int

const (
	// SeverityError is a problem the loader rejects or silently gets wrong;
	// one of these makes the check fail.
	SeverityError Severity = iota
	// SeverityWarning is a spelling the loader accepts but editors flag, or
	// a hint that costs nothing to follow; it never fails the check.
	SeverityWarning
)

// Finding is one problem in a config file.
type Finding struct {
	Severity Severity
	// Line and Column locate the problem in the file (1-based); 0 when the
	// finding is about the file as a whole.
	Line, Column int
	// Path is the dotted key the finding is about ("httpserver.enabled",
	// "providers[1].type"); empty for a whole-file finding or when Message
	// already carries it.
	Path string
	// Message says what is wrong.
	Message string
	// Fix says how to correct it; empty when the message is the fix.
	Fix string
	// Doc is the schema description of the field, when one helps.
	Doc string
}

// CheckReport is the outcome of checking one config file.
type CheckReport struct {
	// File is the config file that was checked, resolved as the loader would.
	File     string
	Findings []Finding
}

// Errors counts the findings that fail the check.
func (r *CheckReport) Errors() int { return r.count(SeverityError) }

// Warnings counts the findings that do not.
func (r *CheckReport) Warnings() int { return r.count(SeverityWarning) }

// Valid reports whether the file has no errors.
func (r *CheckReport) Valid() bool { return r.Errors() == 0 }

func (r *CheckReport) count(sev Severity) int {
	n := 0
	for _, f := range r.Findings {
		if f.Severity == sev {
			n++
		}
	}
	return n
}

// Write renders the report as compiler-style lines an editor can jump to:
// file:line:col: path: message, then an indented fix and doc line, and a
// summary line last.
func (r *CheckReport) Write(w io.Writer) {
	for _, f := range r.Findings {
		loc := r.File
		if f.Line > 0 {
			loc += ":" + strconv.Itoa(f.Line)
			if f.Column > 0 {
				loc += ":" + strconv.Itoa(f.Column)
			}
		}
		head := loc + ": "
		if f.Severity == SeverityWarning {
			head += "warning: "
		}
		if f.Path != "" {
			head += f.Path + ": "
		}
		_, _ = fmt.Fprintln(w, head+f.Message)
		if f.Fix != "" {
			_, _ = fmt.Fprintln(w, "    fix: "+f.Fix)
		}
		if f.Doc != "" {
			_, _ = fmt.Fprintln(w, "    doc: "+f.Doc)
		}
	}
	summary := r.File + ": "
	if r.Valid() {
		summary += "valid"
		if n := r.Warnings(); n > 0 {
			summary += ", " + plural(n, "warning")
		}
	} else {
		summary += plural(r.Errors(), "error")
		if n := r.Warnings(); n > 0 {
			summary += ", " + plural(n, "warning")
		}
	}
	_, _ = fmt.Fprintln(w, summary)
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

// The flag every entrypoint offers for checking config.yaml without starting
// anything: `coddy -t`, `coddy cli -t`, `coddy acp -t`, `coddy serve -t`.
const (
	CheckFlagName  = "test-config"
	CheckFlagShort = "t"
	CheckFlagUsage = "check config.yaml against the schema and the loader's rules, print every problem with its line and how to fix it, then exit without starting anything"
)

// AddCheckFlag registers -t / --test-config on fs.
func AddCheckFlag(fs *flag.FlagSet) *bool {
	v := fs.Bool(CheckFlagName, false, CheckFlagUsage)
	fs.BoolVar(v, CheckFlagShort, false, "alias of --"+CheckFlagName)
	return v
}

// Check validates the config file the given flags select - the same file
// LoadFromCLI would read, <home>/.env loaded first so ${VAR} references see
// its values - without loading it: no backup is written and none is restored.
// The error is only about resolving the paths; problems in the file are
// findings.
func Check(cli CLIPaths) (*CheckReport, error) {
	paths, err := resolveConfigFile(cli)
	if err != nil {
		return nil, err
	}
	rep := &CheckReport{File: paths.ConfigPath}
	data, err := os.ReadFile(paths.ConfigPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			rep.Findings = append(rep.Findings, Finding{
				Severity: SeverityError,
				Message:  "config file not found",
				Fix:      "create it (config.example.yaml in the repository documents every key), or name another file with --config PATH or another home with --home DIR",
			})
			return rep, nil
		}
		rep.Findings = append(rep.Findings, Finding{
			Severity: SeverityError,
			Message:  "cannot read the config file: " + err.Error(),
		})
		return rep, nil
	}
	rep.Findings = checkConfigBytes(data, paths)
	return rep, nil
}

// RunCheck prints the report for the config file the flags select and fails
// when the file has errors, so the process exits non-zero. Warnings alone
// leave it succeeding.
func RunCheck(w io.Writer, cli CLIPaths) error {
	rep, err := Check(cli)
	if err != nil {
		return err
	}
	rep.Write(w)
	if !rep.Valid() {
		return errors.New("config test failed")
	}
	return nil
}

// checkConfigBytes runs the stages of the check over the raw file content.
func checkConfigBytes(data []byte, paths Paths) []Finding {
	var findings []Finding
	if !hasSchemaModeline(data) {
		findings = append(findings, Finding{
			Severity: SeverityWarning, Line: 1, Column: 1,
			Message: "no schema modeline, so editors do not validate this file",
			Fix:     "add as the first line: " + SchemaModeline(),
		})
	}

	// The same expansion the loader performs: ${CODDY_HOME}, environment
	// references and $$ escapes, ${CWD} left in place. Values stay on their
	// lines, so positions below are positions in the file.
	expanded := expandConfigBody(string(data), paths)

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(expanded), &doc); err != nil {
		return append(findings, syntaxFindings(err)...)
	}
	body := configDocumentRoot(&doc)
	if body == nil {
		// An empty document (or comments only) is a valid config: every field
		// has a default.
		return findings
	}
	if body.Kind != yaml.MappingNode {
		return append(findings, Finding{
			Severity: SeverityError, Line: body.Line, Column: body.Column,
			Message: "the top level must be a mapping of keys, got " + describe(body, ""),
			Fix:     "start each setting at column 1 as key: value (providers:, models:, agent:, ...)",
		})
	}

	schemaFindings, err := validateAgainstSchema(body)
	if err != nil {
		return append(findings, Finding{Severity: SeverityError, Message: err.Error()})
	}
	findings = append(findings, schemaFindings...)
	schemaErrors := 0
	for _, f := range schemaFindings {
		if f.Severity == SeverityError {
			schemaErrors++
		}
	}

	// The loader's own rules: cross-field checks the schema cannot express
	// (a model naming a provider that does not exist, a file sink without a
	// path). Decoding errors repeat what the schema already said, so they are
	// only reported when the schema had nothing to say.
	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		if schemaErrors == 0 {
			findings = append(findings, syntaxFindings(err)...)
		}
		return sortFindings(findings)
	}
	cfg.Paths = paths
	applyDefaults(&cfg)
	if err := validateSubconfigs(&cfg); err != nil {
		f := loaderFinding(err, body, &cfg)
		if !coveredAtLine(findings, f.Line) {
			findings = append(findings, f)
		}
	}
	return sortFindings(findings)
}

// coveredAtLine reports whether an error is already reported on that line; a
// loader error there restates it in the loader's words.
func coveredAtLine(findings []Finding, line int) bool {
	if line <= 0 {
		return false
	}
	for _, f := range findings {
		if f.Severity == SeverityError && f.Line == line {
			return true
		}
	}
	return false
}

// sortFindings orders findings by position, whole-file findings last, keeping
// the discovery order within a line.
func sortFindings(findings []Finding) []Finding {
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if (a.Line == 0) != (b.Line == 0) {
			return a.Line != 0
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Column < b.Column
	})
	return findings
}

var yamlLineRE = regexp.MustCompile(`line (\d+): (.*)`)

// syntaxFindings turns a yaml.v3 error into findings. The library reports
// either one "yaml: line N: message" or a list of "line N: message" entries.
func syntaxFindings(err error) []Finding {
	var out []Finding
	for _, m := range yamlLineRE.FindAllStringSubmatch(err.Error(), -1) {
		line, _ := strconv.Atoi(m[1])
		out = append(out, Finding{Severity: SeverityError, Line: line, Message: m[2], Fix: syntaxFix(m[2])})
	}
	if len(out) == 0 {
		msg := strings.TrimPrefix(err.Error(), "yaml: ")
		out = append(out, Finding{Severity: SeverityError, Message: msg, Fix: syntaxFix(msg)})
	}
	return out
}

// syntaxFix explains the YAML mistakes people actually make behind the
// parser's wording.
func syntaxFix(msg string) string {
	switch {
	case strings.Contains(msg, "cannot start any token"):
		return "YAML indents with spaces; replace tabs with spaces on this line"
	case strings.Contains(msg, "mapping values are not allowed"):
		return "check the indentation of this line against the one above; a value containing \": \" needs quotes"
	case strings.Contains(msg, "did not find expected"):
		return "check the indentation and look for an unclosed quote or bracket above this line"
	case strings.Contains(msg, "already defined"):
		return "keep one of the two definitions"
	case strings.Contains(msg, "cannot unmarshal"):
		return "give the key a value of the shape the reference documents (https://coddy.dev/docs/reference/config)"
	default:
		return "fix the YAML syntax at this line"
	}
}

var (
	quotedRE      = regexp.MustCompile(`"([^"]+)"`)
	leadingPathRE = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*(?:\[[^\]]*\])?(?:\.[A-Za-z_][A-Za-z0-9_]*(?:\[[^\]]*\])?)*):`)
	pathTokenRE   = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*(?:\[[^\]]*\])?(?:\.[A-Za-z_][A-Za-z0-9_]*(?:\[[^\]]*\])?)*`)
	selectorRE    = regexp.MustCompile(`\[[^\]]*\]`)
)

// loaderFinding places a validation error of the loader in the file: at the
// key a path in the message names ("compaction.threshold_percent",
// "providers[b]"), else at the scalar carrying a value the message quotes,
// else at the section the message starts with.
func loaderFinding(err error, body *yaml.Node, cfg *Config) Finding {
	msg := err.Error()
	f := Finding{Severity: SeverityError, Message: msg, Fix: loaderFix(msg, cfg)}
	paths := loaderPaths(msg)
	for _, p := range paths {
		if n := locatePath(body, p, true); n != nil {
			f.Line, f.Column = n.Line, n.Column
			break
		}
	}
	if f.Line == 0 {
		f.Line, f.Column = locateQuoted(msg, body)
	}
	if f.Line == 0 {
		if m := leadingPathRE.FindStringSubmatch(msg); m != nil {
			section := m[1]
			if i := strings.IndexAny(section, ".["); i >= 0 {
				section = section[:i]
			}
			if k := mappingKey(body, section); k != nil {
				f.Line, f.Column = k.Line, k.Column
			}
		}
	}
	if root, err := loadSchema(); err == nil {
		for _, p := range paths {
			if s := root.lookup(selectorRE.ReplaceAllString(p, "")); s != nil {
				f.Doc = s.doc()
				break
			}
		}
	}
	return f
}

// loaderPaths lists the config paths a loader message names, in the order
// the message names them: the first dotted or selected path is the subject
// of the sentence ("logger.file: required when 'file' is in logger.outputs"),
// the later ones are context. The bare section a message opens with comes
// last as the coarsest fallback.
func loaderPaths(msg string) []string {
	var out []string
	for _, tok := range pathTokenRE.FindAllString(msg, -1) {
		if strings.ContainsAny(tok, ".[") {
			out = append(out, tok)
		}
	}
	if m := leadingPathRE.FindStringSubmatch(msg); m != nil {
		section := m[1]
		if i := strings.IndexAny(section, ".["); i >= 0 {
			section = section[:i]
		}
		out = append(out, section)
	}
	return out
}

// locatePath walks a dotted path with optional selectors ("providers[b]",
// "logger.levels[0].component") through the document and returns the node
// that stands for it: a scalar value, a selected list entry, or - for a key
// whose value is a block section or list - the key itself, which is the line
// an operator looks for. In lenient mode an absent last key yields the key
// that introduces the section that should hold it; nil when the path does
// not fit the document.
func locatePath(body *yaml.Node, path string, lenient bool) *yaml.Node {
	cur := body
	var parentKey *yaml.Node
	segments := strings.Split(path, ".")
	for i, seg := range segments {
		key, sel := seg, ""
		if j := strings.IndexByte(seg, '['); j >= 0 && strings.HasSuffix(seg, "]") {
			key, sel = seg[:j], seg[j+1:len(seg)-1]
		}
		cur = resolveAlias(cur)
		if cur == nil || cur.Kind != yaml.MappingNode {
			return nil
		}
		k := mappingKey(cur, key)
		if k == nil {
			if lenient && i == len(segments)-1 {
				return parentKey
			}
			return nil
		}
		val := mappingValueAfterKey(cur, k)
		if sel != "" {
			val = resolveAlias(val)
			if val == nil || val.Kind != yaml.SequenceNode {
				return nil
			}
			if val = sequenceItem(val, sel); val == nil {
				return nil
			}
			parentKey, cur = k, val
			continue
		}
		parentKey, cur = k, val
	}
	if n := resolveAlias(cur); n != nil && (n.Kind == yaml.MappingNode || n.Kind == yaml.SequenceNode) && n.Style != yaml.FlowStyle && parentKey != nil && n.Line != parentKey.Line {
		// A block section starts on the line after its key; the key is where
		// the operator wrote it.
		if len(segments) > 0 && !strings.HasSuffix(segments[len(segments)-1], "]") {
			return parentKey
		}
	}
	return cur
}

// mappingKey finds the key node of name in a mapping.
func mappingKey(m *yaml.Node, name string) *yaml.Node {
	m = resolveAlias(m)
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if k := m.Content[i]; k.Kind == yaml.ScalarNode && k.Value == name {
			return k
		}
	}
	return nil
}

// mappingValueAfterKey returns the value paired with a key node of m.
func mappingValueAfterKey(m, key *yaml.Node) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i] == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// sequenceItem picks a list entry by index ("0") or by the value of one of
// its identity keys ("b" for the provider named b, "a/b" for that model).
func sequenceItem(seq *yaml.Node, sel string) *yaml.Node {
	if idx, err := strconv.Atoi(sel); err == nil {
		if idx >= 0 && idx < len(seq.Content) {
			return seq.Content[idx]
		}
		return nil
	}
	for _, item := range seq.Content {
		for _, idKey := range append([]string{"component"}, identityKeys...) {
			if k := mappingKey(item, idKey); k != nil {
				if v := resolveAlias(mappingValueAfterKey(resolveAlias(item), k)); v != nil && v.Kind == yaml.ScalarNode && v.Value == sel {
					return item
				}
			}
		}
	}
	return nil
}

// locateQuoted finds the scalar carrying a value the message quotes: an
// exact match first, then a value that contains it (a provider name inside
// "provider/model").
func locateQuoted(msg string, body *yaml.Node) (int, int) {
	var quoted []string
	for _, m := range quotedRE.FindAllStringSubmatch(msg, -1) {
		quoted = append(quoted, m[1])
	}
	scalars := collectScalars(body, nil)
	for _, q := range quoted {
		for _, s := range scalars {
			if s.Value == q {
				return s.Line, s.Column
			}
		}
	}
	for _, q := range quoted {
		for _, s := range scalars {
			if strings.Contains(s.Value, q) {
				return s.Line, s.Column
			}
		}
	}
	return 0, 0
}

// collectScalars lists the value scalars of a document in file order.
func collectScalars(n *yaml.Node, out []*yaml.Node) []*yaml.Node {
	n = resolveAlias(n)
	if n == nil {
		return out
	}
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			out = collectScalars(n.Content[i+1], out)
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			out = collectScalars(c, out)
		}
	case yaml.ScalarNode:
		out = append(out, n)
	}
	return out
}

// loaderFix adds the way out for the loader errors that have one the message
// does not already spell out.
func loaderFix(msg string, cfg *Config) string {
	switch {
	case strings.Contains(msg, "unknown provider"):
		names := providerNames(cfg)
		if len(names) == 0 {
			return "add a providers entry with that name before a model refers to it"
		}
		return fmt.Sprintf("providers configured: %s; add one with that name or point the model at one of these", strings.Join(names, ", "))
	case strings.Contains(msg, "not found in models list"), strings.Contains(msg, "agent.model is required"):
		names := modelNames(cfg)
		if len(names) == 0 {
			return "add a models entry and set agent.model to its model value"
		}
		return fmt.Sprintf("models configured: %s; set agent.model to one of them", strings.Join(names, ", "))
	case strings.Contains(msg, "duplicate"):
		return "rename or remove one of the two entries"
	}
	return ""
}

func providerNames(cfg *Config) []string {
	var out []string
	for _, p := range cfg.Providers {
		if n := strings.TrimSpace(p.Name); n != "" {
			out = append(out, n)
		}
	}
	return out
}

func modelNames(cfg *Config) []string {
	var out []string
	for _, m := range cfg.Models {
		if n := strings.TrimSpace(m.Model); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// LoadReadOnly reads and validates the config file the flags select without
// the side effects of a start: no backup is written and none is restored. It
// returns the loaded configuration and the raw file bytes, so a caller can
// locate its findings in the file the configuration came from (NewLocator).
func LoadReadOnly(cli CLIPaths) (*Config, []byte, error) {
	paths, err := resolveConfigFile(cli)
	if err != nil {
		return nil, nil, err
	}
	data, err := os.ReadFile(paths.ConfigPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read config %s: %w", paths.ConfigPath, err)
	}
	cfg, err := parseValidateYAMLBytes(expandConfigBody(string(data), paths), paths)
	if err != nil {
		return nil, nil, fmt.Errorf("config %s: %w", paths.ConfigPath, err)
	}
	return cfg, data, nil
}

// Locator maps config paths ("providers[b]", "httpserver.port",
// "skills.dirs[1]") to positions in a config file, so a report about a value
// can point at the line that set it. A selector picks a list entry by index
// or by the value of an identity key (name, model, url, ...). A path that is
// not in the file - an applied default - resolves to nothing.
type Locator struct {
	body *yaml.Node
}

// NewLocator parses the raw file bytes - not the expanded document the
// loader reads - so a column is the column in the file even on a line where
// ${CODDY_HOME} or an environment reference grows when substituted. Values
// are never read through a locator, only placed, so the unexpanded text is
// the right one. Unparsable data yields a locator that resolves nothing.
func NewLocator(data []byte) *Locator {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return &Locator{}
	}
	body := configDocumentRoot(&doc)
	if body == nil || body.Kind != yaml.MappingNode {
		return &Locator{}
	}
	return &Locator{body: body}
}

// Locate returns the 1-based line and column of path, or ok=false when the
// path is not in the file.
func (l *Locator) Locate(path string) (line, col int, ok bool) {
	if l == nil || l.body == nil {
		return 0, 0, false
	}
	n := locatePath(l.body, path, false)
	if n == nil {
		return 0, 0, false
	}
	return n.Line, n.Column, true
}
