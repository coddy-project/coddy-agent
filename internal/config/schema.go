package config

// The JSON Schema for config.yaml, embedded so that -t / --test-config checks a
// file against exactly what editors check it against. The file is the one
// published at SchemaURL (scripts/sync-site-schema.sh copies it to the site).
// The loader stays lenient - an unknown key is ignored, "yes" is a boolean -
// and this is the strict view of the same surface, with every disagreement
// located in the file and explained.

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed config.schema.json
var configSchemaJSON []byte

// ConfigSchemaJSON returns the embedded JSON Schema for config.yaml, the same
// bytes that are published at SchemaURL.
func ConfigSchemaJSON() []byte { return append([]byte(nil), configSchemaJSON...) }

// schemaNode is the subset of JSON Schema draft-07 the config schema uses: a
// type (one name, or a name plus "null" for tri-state fields), nested
// properties closed with additionalProperties:false, list items, enum,
// pattern, minimum, maximum, required, default and the descriptions that
// become the doc line of a finding.
type schemaNode struct {
	Type                 schemaType             `json:"type"`
	Title                string                 `json:"title"`
	Description          string                 `json:"description"`
	Properties           map[string]*schemaNode `json:"properties"`
	AdditionalProperties additionalProperties   `json:"additionalProperties"`
	Items                *schemaNode            `json:"items"`
	Enum                 []interface{}          `json:"enum"`
	Pattern              string                 `json:"pattern"`
	Minimum              *float64               `json:"minimum"`
	Maximum              *float64               `json:"maximum"`
	Required             []string               `json:"required"`
	Default              interface{}            `json:"default"`

	re *regexp.Regexp
}

// additionalProperties is that keyword as the schema uses it: false closes a
// section to the keys it lists, and a sub-schema describes a map whose values
// all share one shape (swarm.join[].labels).
type additionalProperties struct {
	closed bool
	schema *schemaNode
}

func (a *additionalProperties) UnmarshalJSON(b []byte) error {
	var allow bool
	if err := json.Unmarshal(b, &allow); err == nil {
		a.closed = !allow
		return nil
	}
	var s schemaNode
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("additionalProperties must be a boolean or a schema, got %s", b)
	}
	a.schema = &s
	return nil
}

// schemaType is the "type" keyword: one name, or a list such as ["boolean", "null"].
type schemaType []string

func (t *schemaType) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*t = schemaType{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return fmt.Errorf("schema type must be a string or a list of strings, got %s", b)
	}
	*t = many
	return nil
}

// main is the type a value takes when it is not null.
func (t schemaType) main() string {
	for _, k := range t {
		if k != "null" {
			return k
		}
	}
	return ""
}

var (
	schemaOnce sync.Once
	schemaRoot *schemaNode
	schemaErr  error
)

// loadSchema parses the embedded schema once per process.
func loadSchema() (*schemaNode, error) {
	schemaOnce.Do(func() {
		var root schemaNode
		if err := json.Unmarshal(configSchemaJSON, &root); err != nil {
			schemaErr = fmt.Errorf("embedded config schema: %w", err)
			return
		}
		schemaRoot = &root
	})
	return schemaRoot, schemaErr
}

// lookup walks a dotted path such as "logger.file" or "providers.timeout_ms",
// stepping into list items where the path names a list.
func (s *schemaNode) lookup(path string) *schemaNode {
	cur := s
	for _, seg := range strings.Split(path, ".") {
		if cur == nil {
			return nil
		}
		for cur.Type.main() == "array" && cur.Items != nil {
			cur = cur.Items
		}
		cur = cur.Properties[seg]
	}
	return cur
}

// enumStrings renders the enum for messages.
func (s *schemaNode) enumStrings() []string {
	out := make([]string, 0, len(s.Enum))
	for _, v := range s.Enum {
		out = append(out, fmt.Sprint(v))
	}
	return out
}

// propertyNames lists the keys a section takes, sorted for stable output.
func (s *schemaNode) propertyNames() []string {
	out := make([]string, 0, len(s.Properties))
	for name := range s.Properties {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (s *schemaNode) regex() (*regexp.Regexp, error) {
	if s.re == nil {
		re, err := regexp.Compile(s.Pattern)
		if err != nil {
			return nil, err
		}
		s.re = re
	}
	return s.re, nil
}

// defaultString renders the default as it would be written in YAML, or "".
func (s *schemaNode) defaultString() string {
	switch d := s.Default.(type) {
	case string:
		return d
	case float64:
		return strconv.FormatFloat(d, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(d)
	default:
		return ""
	}
}

// doc is the description trimmed to a length a terminal line can carry.
func (s *schemaNode) doc() string {
	if s == nil {
		return ""
	}
	const limit = 300
	d := []rune(strings.TrimSpace(s.Description))
	if len(d) <= limit {
		return string(d)
	}
	cut := limit
	for i := limit; i > limit/2; i-- {
		if d[i] == ' ' {
			cut = i
			break
		}
	}
	return string(d[:cut]) + "..."
}

// schemaValidator walks a parsed config.yaml document next to the schema and
// records a finding for every place the two disagree.
type schemaValidator struct {
	findings []Finding
}

// validateAgainstSchema checks the body of a config document (its top-level
// mapping) against the embedded schema.
func validateAgainstSchema(body *yaml.Node) ([]Finding, error) {
	root, err := loadSchema()
	if err != nil {
		return nil, err
	}
	v := &schemaValidator{}
	v.value("", body, root)
	return v.findings, nil
}

func (v *schemaValidator) at(n *yaml.Node, sev Severity, path, msg, fix, doc string) {
	v.findings = append(v.findings, Finding{
		Severity: sev, Line: n.Line, Column: n.Column, Path: path, Message: msg, Fix: fix, Doc: doc,
	})
}

// value checks one node against the schema that describes its place.
func (v *schemaValidator) value(path string, n *yaml.Node, s *schemaNode) {
	n = resolveAlias(n)
	if n == nil || s == nil {
		return
	}
	// A key with no value is the zero value for the loader and "unset" for a
	// tri-state field, whatever the type.
	if n.Kind == yaml.ScalarNode && n.ShortTag() == "!!null" {
		return
	}
	switch s.Type.main() {
	case "object":
		if n.Kind != yaml.MappingNode {
			v.typeMismatch(path, n, s, "a section with its own keys")
			return
		}
		v.mapping(path, n, s)
	case "array":
		if n.Kind != yaml.SequenceNode {
			v.typeMismatch(path, n, s, "a list")
			return
		}
		for i, item := range n.Content {
			v.value(fmt.Sprintf("%s[%d]", path, i), item, s.Items)
		}
	case "string":
		if n.Kind != yaml.ScalarNode {
			v.typeMismatch(path, n, s, "a string")
			return
		}
		v.scalar(path, n, s)
	case "integer":
		if v.number(path, n, s, true) {
			v.scalar(path, n, s)
		}
	case "number":
		if v.number(path, n, s, false) {
			v.scalar(path, n, s)
		}
	case "boolean":
		v.boolean(path, n, s)
	}
}

// mapping checks a section: every key must be known, unique and of the right
// shape, and every required key must be present.
func (v *schemaValidator) mapping(path string, n *yaml.Node, s *schemaNode) {
	seen := map[string]*yaml.Node{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, val := n.Content[i], n.Content[i+1]
		if k.Kind != yaml.ScalarNode || k.ShortTag() == "!!merge" {
			continue
		}
		name := k.Value
		if first, dup := seen[name]; dup {
			v.at(k, SeverityError, join(path, name),
				fmt.Sprintf("duplicate key %q, first defined on line %d", name, first.Line),
				"keep one of the two; the loader rejects a file that defines a key twice", "")
			continue
		}
		seen[name] = k
		child, known := s.Properties[name]
		if !known {
			switch {
			case s.AdditionalProperties.schema != nil:
				v.value(join(path, name), val, s.AdditionalProperties.schema)
			case s.AdditionalProperties.closed:
				v.unknownKey(path, k, s)
			}
			continue
		}
		v.value(join(path, name), val, child)
	}
	for _, req := range s.Required {
		if _, ok := seen[req]; ok {
			continue
		}
		prop := s.Properties[req]
		fix := fmt.Sprintf("add the key %q", req)
		if prop != nil {
			if e := prop.enumStrings(); len(e) > 0 {
				fix += " with one of " + strings.Join(e, ", ")
			}
		}
		v.at(n, SeverityError, path, fmt.Sprintf("missing required key %q", req), fix, prop.doc())
	}
}

func (v *schemaValidator) unknownKey(path string, k *yaml.Node, s *schemaNode) {
	allowed := s.propertyNames()
	where := "at the top level"
	if path != "" {
		where = "under " + path
	}
	fix := fmt.Sprintf("keys allowed %s: %s", where, strings.Join(allowed, ", "))
	doc := ""
	if c := closest(k.Value, allowed); c != "" {
		fix = fmt.Sprintf("did you mean %q? %s", c, fix)
		doc = s.Properties[c].doc()
	}
	v.at(k, SeverityError, join(path, k.Value),
		fmt.Sprintf("unknown key %q (the loader ignores it, so it has no effect)", k.Value), fix, doc)
}

// typeMismatch reports a node whose shape is not what the schema wants.
func (v *schemaValidator) typeMismatch(path string, n *yaml.Node, s *schemaNode, want string) {
	key := lastSegment(path)
	var fix string
	switch want {
	case "a section with its own keys":
		if names := s.propertyNames(); len(names) > 0 {
			fix = fmt.Sprintf("write %s as a section of keys: %s", key, strings.Join(names, ", "))
		} else {
			fix = fmt.Sprintf("write %s as a section of keys", key)
		}
	case "a list":
		example := "value"
		if n.Kind == yaml.ScalarNode {
			example = displayValue(n, path)
		}
		fix = fmt.Sprintf(`write a list, one item per line beginning with "- " (inline form: %s: [%s])`, key, example)
	case "a string":
		fix = "write a single value"
		if d := s.defaultString(); d != "" {
			fix += fmt.Sprintf(", for example %s: %q", key, d)
		}
	default:
		fix = fmt.Sprintf("write %s", want)
	}
	v.at(n, SeverityError, path, fmt.Sprintf("expected %s, got %s", want, describe(n, path)), fix, s.doc())
}

// number checks an integer or number field and reports whether the value is
// usable, so the caller can go on with enum and range checks.
func (v *schemaValidator) number(path string, n *yaml.Node, s *schemaNode, integer bool) bool {
	want, word := "a number", "a number"
	if integer {
		want, word = "an integer", "a whole number"
	}
	if n.Kind != yaml.ScalarNode {
		v.typeMismatch(path, n, s, want)
		return false
	}
	key := lastSegment(path)
	example := s.defaultString()
	if example == "" {
		example = "0"
	}
	switch n.ShortTag() {
	case "!!int":
		return true
	case "!!float":
		if !integer {
			return true
		}
		f, err := strconv.ParseFloat(n.Value, 64)
		if err == nil && !math.IsInf(f, 0) && f == math.Trunc(f) {
			v.at(n, SeverityWarning, path,
				fmt.Sprintf("%s is read as the integer %d, but the schema expects a whole number", n.Value, int64(f)),
				fmt.Sprintf("write %s: %d", key, int64(f)), "")
			return true
		}
		v.at(n, SeverityError, path, fmt.Sprintf("expected an integer, got the number %s", n.Value),
			fmt.Sprintf("write %s, for example %s: %s", word, key, example), s.doc())
		return false
	case "!!str":
		val := strings.TrimSpace(n.Value)
		if _, err := strconv.ParseFloat(strings.ReplaceAll(val, "_", ""), 64); err == nil {
			v.at(n, SeverityError, path, fmt.Sprintf("expected %s, got the string %s", want, displayValue(n, path)),
				fmt.Sprintf("remove the quotes: %s: %s", key, unquotedValue(n, path)), "")
			return false
		}
		v.at(n, SeverityError, path, fmt.Sprintf("expected %s, got the string %s", want, displayValue(n, path)),
			fmt.Sprintf("write %s, for example %s: %s", word, key, example), s.doc())
		return false
	default:
		v.at(n, SeverityError, path, fmt.Sprintf("expected %s, got %s", want, describe(n, path)),
			fmt.Sprintf("write %s, for example %s: %s", word, key, example), s.doc())
		return false
	}
}

// boolean checks a boolean field. The loader still reads the YAML 1.1
// spellings (yes, no, on, off), so those are a warning rather than an error.
func (v *schemaValidator) boolean(path string, n *yaml.Node, s *schemaNode) {
	if n.Kind != yaml.ScalarNode {
		v.typeMismatch(path, n, s, "a boolean")
		return
	}
	key := lastSegment(path)
	fix := fmt.Sprintf("write %s: true or %s: false", key, key)
	switch n.ShortTag() {
	case "!!bool":
		return
	case "!!str":
		if b, ok := yaml11Bool(n.Value); ok {
			v.at(n, SeverityWarning, path,
				fmt.Sprintf("%q is read as the boolean %t, but the schema and editors expect true or false", n.Value, b),
				fmt.Sprintf("write %s: %t (true or false, unquoted)", key, b), "")
			return
		}
		if lower := strings.ToLower(strings.TrimSpace(n.Value)); lower == "true" || lower == "false" {
			v.at(n, SeverityError, path, fmt.Sprintf("expected a boolean, got the string %q", n.Value),
				fmt.Sprintf("remove the quotes: %s: %s", key, lower), "")
			return
		}
		v.at(n, SeverityError, path, fmt.Sprintf("expected a boolean, got the string %s", displayValue(n, path)),
			fix+" (true or false, unquoted)", s.doc())
	default:
		v.at(n, SeverityError, path, fmt.Sprintf("expected a boolean, got %s", describe(n, path)), fix, s.doc())
	}
}

// scalar runs the checks a scalar of the right type still has to pass: enum,
// pattern and range.
func (v *schemaValidator) scalar(path string, n *yaml.Node, s *schemaNode) {
	if len(s.Enum) > 0 {
		allowed := s.enumStrings()
		found := false
		for _, a := range allowed {
			if a == n.Value {
				found = true
				break
			}
		}
		if !found {
			fix := "use one of " + strings.Join(allowed, ", ")
			if c := closest(n.Value, allowed); c != "" {
				fix = fmt.Sprintf("did you mean %q? %s", c, fix)
			}
			v.at(n, SeverityError, path, fmt.Sprintf("%s is not an allowed value", displayValue(n, path)), fix, s.doc())
			return
		}
	}
	if s.Pattern != "" {
		if re, err := s.regex(); err == nil && !re.MatchString(n.Value) {
			v.at(n, SeverityError, path,
				fmt.Sprintf("%s does not match the required pattern %s", displayValue(n, path), s.Pattern),
				fmt.Sprintf("use a value matching %s", s.Pattern), s.doc())
			return
		}
	}
	if s.Minimum == nil && s.Maximum == nil {
		return
	}
	f, ok := numericValue(n)
	if !ok {
		return
	}
	key := lastSegment(path)
	rangeFix := ""
	switch {
	case s.Minimum != nil && s.Maximum != nil:
		rangeFix = fmt.Sprintf("set %s between %s and %s", key, formatNumber(*s.Minimum), formatNumber(*s.Maximum))
	case s.Minimum != nil:
		rangeFix = fmt.Sprintf("set %s to %s or more", key, formatNumber(*s.Minimum))
	default:
		rangeFix = fmt.Sprintf("set %s to %s or less", key, formatNumber(*s.Maximum))
	}
	if s.Minimum != nil && f < *s.Minimum {
		v.at(n, SeverityError, path, fmt.Sprintf("%s must be at least %s", n.Value, formatNumber(*s.Minimum)), rangeFix, s.doc())
		return
	}
	if s.Maximum != nil && f > *s.Maximum {
		v.at(n, SeverityError, path, fmt.Sprintf("%s must be at most %s", n.Value, formatNumber(*s.Maximum)), rangeFix, s.doc())
	}
}

// resolveAlias follows an anchor reference to the node it names.
func resolveAlias(n *yaml.Node) *yaml.Node {
	for i := 0; n != nil && n.Kind == yaml.AliasNode && i < 16; i++ {
		n = n.Alias
	}
	return n
}

// describe names what a node is, for the "got ..." half of a message.
func describe(n *yaml.Node, path string) string {
	switch n.Kind {
	case yaml.MappingNode:
		return "a section (mapping)"
	case yaml.SequenceNode:
		return "a list"
	case yaml.ScalarNode:
		switch n.ShortTag() {
		case "!!int", "!!float":
			return "the number " + n.Value
		case "!!bool":
			return "the boolean " + n.Value
		default:
			return "the string " + displayValue(n, path)
		}
	default:
		return "an unexpected node"
	}
}

// displayValue quotes a scalar for a message, hides values under secret-shaped
// keys and shortens long ones.
func displayValue(n *yaml.Node, path string) string {
	if isSecretPath(path) {
		return "<redacted>"
	}
	val := n.Value
	if r := []rune(val); len(r) > 60 {
		val = string(r[:57]) + "..."
	}
	return strconv.Quote(val)
}

// unquotedValue is a scalar as it would be written without quotes, hidden
// under a secret-shaped key.
func unquotedValue(n *yaml.Node, path string) string {
	if isSecretPath(path) {
		return "<value>"
	}
	return strings.TrimSpace(n.Value)
}

// isSecretPath reports whether a dotted path names a credential, by the same
// rule config_get uses to redact one (configSecretPath), so max_tokens is
// echoed and pairing_tokens is not.
func isSecretPath(path string) bool {
	if path == "" {
		return false
	}
	segs := strings.Split(path, ".")
	for i, seg := range segs {
		if j := strings.IndexByte(seg, '['); j >= 0 {
			segs[i] = seg[:j]
		}
	}
	return configSecretPath(segs)
}

// yaml11Bool recognises the YAML 1.1 boolean spellings yaml.v3 still decodes
// into a bool field.
func yaml11Bool(s string) (bool, bool) {
	switch s {
	case "y", "Y", "yes", "Yes", "YES", "on", "On", "ON":
		return true, true
	case "n", "N", "no", "No", "NO", "off", "Off", "OFF":
		return false, true
	}
	return false, false
}

// numericValue parses an !!int or !!float scalar the way yaml.v3 does.
func numericValue(n *yaml.Node) (float64, bool) {
	if n.Kind != yaml.ScalarNode {
		return 0, false
	}
	switch n.ShortTag() {
	case "!!int":
		if i, err := strconv.ParseInt(strings.ReplaceAll(n.Value, "_", ""), 0, 64); err == nil {
			return float64(i), true
		}
	case "!!float":
		if f, err := strconv.ParseFloat(strings.ReplaceAll(n.Value, "_", ""), 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

func formatNumber(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// join appends a key to a dotted path.
func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// lastSegment is the key a path ends with, without a list index.
func lastSegment(path string) string {
	if i := strings.LastIndex(path, "."); i >= 0 {
		path = path[i+1:]
	}
	if i := strings.IndexByte(path, '['); i >= 0 {
		path = path[:i]
	}
	return path
}

// closest picks the candidate a misspelt word most likely meant, or "" when
// nothing is close enough to suggest.
func closest(word string, candidates []string) string {
	w := strings.ToLower(strings.TrimSpace(word))
	if w == "" {
		return ""
	}
	best, bestDist := "", -1
	for _, c := range candidates {
		d := levenshtein(w, strings.ToLower(c))
		if bestDist < 0 || d < bestDist {
			best, bestDist = c, d
		}
	}
	if best == "" {
		return ""
	}
	if bestDist <= 1+len(w)/3 {
		return best
	}
	lower := strings.ToLower(best)
	if strings.HasPrefix(lower, w) || strings.HasPrefix(w, lower) {
		return best
	}
	return ""
}

// levenshtein is the edit distance between two strings.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
