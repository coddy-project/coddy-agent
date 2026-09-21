package config

// Writing config.yaml back to disk without losing what the operator put there.
//
// A config file is a hand-edited document: it carries section headers, notes next to
// a provider, and commented-out keys kept for later. It also carries the schema
// modeline that points a YAML language server at Coddy's published JSON Schema, which
// is what makes an editor validate and autocomplete the file. Rendering the config
// structs from scratch on every save dropped all of it, so a single save from the
// settings screen turned a documented file into a flat dump and silently disconnected
// the editor's validation.
//
// So a save merges instead: the values come from the config structs, while the
// comments and the key order come from the file that is already there.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// SchemaURL is the hosted JSON Schema for config.yaml. It is published on the project
// site rather than served from a repository path so the address stays stable and any
// editor can resolve it without a checkout; internal/config/config.schema.json, embedded
// into the binary for -t / --test-config, is the source the site mirrors.
const SchemaURL = "https://coddy.dev/config.schema.json"

// schemaModelineMarker is the directive a YAML language server looks for in a comment.
const schemaModelineMarker = "yaml-language-server:"

// SchemaModeline is the comment line that binds a config file to the published schema.
func SchemaModeline() string {
	return "# " + schemaModelineMarker + " $schema=" + SchemaURL
}

// identityKeys name the field that identifies an entry of a config sequence, so a
// comment written next to one provider follows that provider rather than its position.
var identityKeys = []string{"name", "model", "id", "url", "path", "command"}

// MarshalConfigYAML serializes cfg to YAML bytes for disk (Paths is omitted via yaml:"-" on
// field). Always-literal secret fields (proxy URLs) are "$"-escaped so the load-time expansion
// pass restores them verbatim instead of resolving "$WORD"/"$N" fragments to empty environment
// variables. The result carries the schema modeline, so an editor validates every config Coddy
// writes.
func MarshalConfigYAML(cfg *Config) ([]byte, error) {
	return MarshalConfigYAMLPreservingComments(cfg, nil)
}

// MarshalConfigYAMLPreservingComments renders cfg as the new content of a config file whose
// current bytes are existing. Values are cfg's; comments, key order and the operator's own
// schema modeline are carried over from existing. A missing, empty or unparsable previous
// document simply yields a freshly rendered one - a save must never fail because the file it
// replaces could not be read.
func MarshalConfigYAMLPreservingComments(cfg *Config, existing []byte) ([]byte, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	original := existing
	// What an editor on Windows left in the file is not part of the document: a
	// carriage return would otherwise stay inside every comment yaml.v3 hands back
	// and be written out again as a line of its own (see source.go).
	existing = normalizeConfigSource(existing)
	var next yaml.Node
	if err := next.Encode(escapeYAMLSecrets(cfg)); err != nil {
		return nil, fmt.Errorf("encode config: %w", err)
	}
	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{&next}}
	var prevRoot *yaml.Node
	if prev, ok := parsePreviousDocument(existing); ok {
		// The leading comment block (the modeline and whatever follows it) hangs off
		// the document node, not off the first key.
		doc.HeadComment = prev.HeadComment
		doc.FootComment = prev.FootComment
		prevRoot = configDocumentRoot(prev)
		mergeYAMLComments(prevRoot, &next)
	}
	// The encode above renders the whole config struct, so without a prune a save
	// writes `key: null` for every optional field the operator never set and
	// materializes every section the defaults fill in - a file that kept those
	// parts commented out came back annotated with nulls and a default dump
	// (issue #265). Pruning runs after the comment merge so a key carrying an
	// operator's note is never dropped. The baseline is built even without a
	// previous document: a first write (a fresh `coddy mcp add`, a save after the
	// file was deleted) stays just as sparse - and defaultsBaseline degrades to
	// nil on error, which only widens what is kept.
	pruneUndocumentedDefaults(&next, prevRoot, defaultsBaseline(cfg), cfg.Paths)
	out, err := marshalConfigDocument(doc)
	if err != nil {
		return nil, fmt.Errorf("serialize config: %w", err)
	}
	return applyLineEnding(ensureSchemaModeline(out), configLineEnding(original)), nil
}

// MarshalConfigYAMLForFile renders cfg as the new content of the config file at path,
// preserving what that file already documents.
func MarshalConfigYAMLForFile(cfg *Config, path string) ([]byte, error) {
	var existing []byte
	if strings.TrimSpace(path) != "" {
		if raw, err := os.ReadFile(path); err == nil {
			existing = raw
		}
	}
	return MarshalConfigYAMLPreservingComments(cfg, existing)
}

// parsePreviousDocument parses the file being replaced. Anything that is not a mapping
// document is reported as absent: there are no comments worth carrying from a file the
// loader could not have read either.
func parsePreviousDocument(raw []byte) (*yaml.Node, bool) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, false
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, false
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, false
	}
	if doc.Content[0].Kind != yaml.MappingNode {
		return nil, false
	}
	return &doc, true
}

// mergeYAMLComments copies the comments of prev onto the matching nodes of next.
func mergeYAMLComments(prev, next *yaml.Node) {
	if prev == nil || next == nil {
		return
	}
	adoptComments(prev, next)
	switch {
	case prev.Kind == yaml.MappingNode && next.Kind == yaml.MappingNode:
		mergeMappingComments(prev, next)
	case prev.Kind == yaml.SequenceNode && next.Kind == yaml.SequenceNode:
		mergeSequenceComments(prev, next)
	case prev.Kind == yaml.ScalarNode && next.Kind == yaml.ScalarNode:
		keepEnvironmentReference(prev, next)
	}
}

// keepEnvironmentReference writes a value back the way the file spelled it when
// that spelling still loads as the value being saved.
//
// The load expands ${VAR} references, so the config a save renders holds what
// the environment supplied - often a key kept out of the file on purpose, as in
// api_key: ${OPENAI_API_KEY}. Rendering that value as it is would write the
// secret into config.yaml and cut it loose from the environment, and every save
// from the settings screen did exactly that. The previous spelling is kept when
// it loads as the value in memory (next.Value, or, for a field rendered with
// "$" escaped, what that escaped form loads as); a value the operator changed
// matches neither and is written as changed.
func keepEnvironmentReference(prev, next *yaml.Node) {
	if !strings.Contains(prev.Value, "$") || prev.Value == next.Value {
		return
	}
	loaded := expandEnvEscaped(prev.Value)
	if loaded != next.Value && loaded != expandEnvEscaped(next.Value) {
		return
	}
	next.Value = prev.Value
	next.Style = prev.Style
}

func adoptComments(prev, next *yaml.Node) {
	if next.HeadComment == "" {
		next.HeadComment = prev.HeadComment
	}
	if next.LineComment == "" {
		next.LineComment = prev.LineComment
	}
	if next.FootComment == "" {
		next.FootComment = prev.FootComment
	}
}

// mergeMappingComments matches by key: a comment belongs to the setting it was written
// next to. Keys the previous file listed keep their order, so a save shows up as the one
// value that changed rather than as a reshuffled file.
func mergeMappingComments(prev, next *yaml.Node) {
	for i := 0; i+1 < len(next.Content); i += 2 {
		keyIndex, value, ok := mappingValue(prev, next.Content[i].Value)
		if !ok {
			continue
		}
		adoptComments(prev.Content[keyIndex], next.Content[i])
		mergeYAMLComments(value, next.Content[i+1])
	}
	reorderMappingLikePrevious(prev, next)
}

func reorderMappingLikePrevious(prev, next *yaml.Node) {
	rank := make(map[string]int, len(prev.Content)/2)
	for i := 0; i+1 < len(prev.Content); i += 2 {
		if _, seen := rank[prev.Content[i].Value]; !seen {
			rank[prev.Content[i].Value] = i
		}
	}
	type entry struct {
		key   *yaml.Node
		value *yaml.Node
		rank  int
	}
	entries := make([]entry, 0, len(next.Content)/2)
	for i := 0; i+1 < len(next.Content); i += 2 {
		r, ok := rank[next.Content[i].Value]
		if !ok {
			// Keys the previous file did not carry go after the ones it did, in the
			// order the config structs declare them.
			r = len(prev.Content) + i
		}
		entries = append(entries, entry{key: next.Content[i], value: next.Content[i+1], rank: r})
	}
	sort.SliceStable(entries, func(a, b int) bool { return entries[a].rank < entries[b].rank })
	content := make([]*yaml.Node, 0, len(next.Content))
	for _, e := range entries {
		content = append(content, e.key, e.value)
	}
	next.Content = content
}

// mergeSequenceComments pairs entries by identity (a provider's name, a model's id, the
// value of a plain string entry) and falls back to the position only for entries that
// carry no identity at all. An entry that was renamed matches nothing, which is the point:
// notes about the old destination must not silently reappear under a new one.
func mergeSequenceComments(prev, next *yaml.Node) {
	used := make([]bool, len(prev.Content))
	matched := make([]*yaml.Node, len(next.Content))
	for i, item := range next.Content {
		id, ok := sequenceItemIdentity(item)
		if !ok {
			continue
		}
		for j, old := range prev.Content {
			if used[j] {
				continue
			}
			oldID, ok := sequenceItemIdentity(old)
			if !ok || oldID != id {
				continue
			}
			used[j], matched[i] = true, old
			break
		}
	}
	for i, item := range next.Content {
		if matched[i] != nil {
			continue
		}
		if _, ok := sequenceItemIdentity(item); ok {
			continue
		}
		if i >= len(prev.Content) || used[i] {
			continue
		}
		if _, ok := sequenceItemIdentity(prev.Content[i]); ok {
			continue
		}
		used[i], matched[i] = true, prev.Content[i]
	}
	for i, old := range matched {
		if old != nil {
			mergeYAMLComments(old, next.Content[i])
		}
	}
}

// sequenceItemIdentity returns a stable id for a sequence entry, or false when the entry
// has none and can only be matched by position.
func sequenceItemIdentity(node *yaml.Node) (string, bool) {
	if node == nil {
		return "", false
	}
	if node.Kind == yaml.ScalarNode {
		return "=" + node.Value, node.Value != ""
	}
	if node.Kind != yaml.MappingNode {
		return "", false
	}
	for _, key := range identityKeys {
		_, value, ok := mappingValue(node, key)
		if !ok || value.Kind != yaml.ScalarNode || value.Value == "" {
			continue
		}
		return key + "=" + value.Value, true
	}
	return "", false
}

// ensureSchemaModeline prepends the published schema modeline unless the document already
// names a schema - an operator who pointed the file at a local or pinned schema keeps it.
func ensureSchemaModeline(yamlBytes []byte) []byte {
	if hasSchemaModeline(yamlBytes) {
		return yamlBytes
	}
	out := make([]byte, 0, len(yamlBytes)+len(SchemaModeline())+1)
	out = append(out, SchemaModeline()...)
	out = append(out, '\n')
	return append(out, yamlBytes...)
}

func hasSchemaModeline(yamlBytes []byte) bool {
	for _, line := range strings.Split(string(yamlBytes), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(trimmed, schemaModelineMarker) {
			return true
		}
	}
	return false
}

// defaultsBaseline renders the document a save of an untouched config would produce:
// an empty file carried through the same pipeline the incoming document went through -
// the loader's defaults and normalization, the JSON DTO round trip that resolves
// effective values (permission_mode, project_trust and friends come back spelled out
// even when the file never named them), and the secret escaping the real document was
// encoded with. A subtree identical to it adds nothing a reader could not infer, so a
// save keeps it out of the file when the previous document never had the key.
//
// A nil result disables the baseline half of pruning: nulls and emptied mappings are
// still dropped, and everything else is kept.
func defaultsBaseline(cfg *Config) *yaml.Node {
	def := &Config{Paths: cfg.Paths}
	applyDefaults(def)
	if err := validateSubconfigs(def); err != nil {
		// The empty config validates; a failure here means a global default is
		// broken, and pruning against a guessed baseline is worse than none.
		return nil
	}
	raw, err := json.Marshal(ConfigToJSONDTO(def))
	if err != nil {
		return nil
	}
	round, err := ParseConfigJSONPreservingSecrets(raw, cfg.Paths, nil)
	if err != nil {
		return nil
	}
	var node yaml.Node
	if err := node.Encode(escapeYAMLSecrets(round)); err != nil {
		return nil
	}
	return &node
}

// pruneUndocumentedDefaults removes from next every pair whose value says nothing:
// a null scalar (an unset optional field, including one the operator just cleared -
// null and absent load identically for every pointer field), a mapping left empty by
// pruning, and, only when the previous document never had the key, a subtree identical
// to the defaults baseline. It runs after the comment merge, so a pair carrying an
// operator's note anywhere is always kept. Sequences are compared as units: an empty
// list can be a deliberate value (reasoning_levels: []), and entries carry fields that
// are meaningful even at zero.
func pruneUndocumentedDefaults(next, prev, baseline *yaml.Node, paths Paths) {
	if next == nil || next.Kind != yaml.MappingNode {
		return
	}
	kept := next.Content[:0]
	for i := 0; i+1 < len(next.Content); i += 2 {
		key, val := next.Content[i], next.Content[i+1]
		var prevVal, baseVal *yaml.Node
		if prev != nil {
			if _, v, ok := mappingValue(prev, key.Value); ok {
				prevVal = v
			}
		}
		if baseline != nil {
			if _, v, ok := mappingValue(baseline, key.Value); ok {
				baseVal = v
			}
		}
		// Children decide before the parent can empty out.
		switch val.Kind {
		case yaml.MappingNode:
			pruneUndocumentedDefaults(val, prevVal, baseVal, paths)
		case yaml.SequenceNode:
			for _, item := range val.Content {
				pruneSequenceItem(item)
			}
		}
		if nodeCarriesComment(key) || nodeCarriesComment(val) {
			kept = append(kept, key, val)
			continue
		}
		if nodeIsNothing(val) {
			continue
		}
		if prevVal == nil && baseVal != nil && yamlNodesDeepEqual(val, baseVal, paths) {
			continue
		}
		kept = append(kept, key, val)
	}
	next.Content = kept
}

// pruneSequenceItem applies the value-means-nothing rule inside a sequence entry:
// null fields and emptied maps go, everything else - including explicit zeros and
// empty lists - stays, because an entry's fields are the record it exists for.
func pruneSequenceItem(item *yaml.Node) {
	if item == nil || item.Kind != yaml.MappingNode {
		return
	}
	kept := item.Content[:0]
	for i := 0; i+1 < len(item.Content); i += 2 {
		key, val := item.Content[i], item.Content[i+1]
		switch val.Kind {
		case yaml.MappingNode:
			pruneSequenceItem(val)
		case yaml.SequenceNode:
			for _, inner := range val.Content {
				pruneSequenceItem(inner)
			}
		}
		if nodeIsNothing(val) && !nodeCarriesComment(key) && !nodeCarriesComment(val) {
			continue
		}
		kept = append(kept, key, val)
	}
	item.Content = kept
}

// nodeIsNothing reports whether a value node carries no information: a null scalar,
// or a mapping left with no pairs (emptied by pruning, or an empty map the struct
// encoded). A sequence is never "nothing" - [] can be a deliberate value.
func nodeIsNothing(v *yaml.Node) bool {
	if v == nil {
		return true
	}
	switch v.Kind {
	case yaml.ScalarNode:
		return v.Tag == "!!null"
	case yaml.MappingNode:
		return len(v.Content) == 0
	default:
		return false
	}
}

// nodeCarriesComment reports whether the node or anything under it has a comment.
// A commented key is the operator writing something down, and a save loses nothing.
func nodeCarriesComment(n *yaml.Node) bool {
	if n == nil {
		return false
	}
	if n.HeadComment != "" || n.LineComment != "" || n.FootComment != "" {
		return true
	}
	for _, c := range n.Content {
		if nodeCarriesComment(c) {
			return true
		}
	}
	return false
}

// yamlNodesDeepEqual compares two document fragments by shape and value alone -
// comments and style do not make two nodes different. Scalar values compare after
// path expansion so `${CODDY_HOME}`/ its expanded form and `~` spellings of the
// same directory count as equal: defaults injected on a load keep the placeholder
// spelling, while a DTO round trip expands it, and both mean the same directory.
// `${CWD}` is deliberately NOT expanded: it is a per-session placeholder the
// per-session fields (skills.dirs, hooks.files, prompts.dir and friends) keep
// literal by design, so an operator-written absolute path equal to this process's
// cwd must never collapse into the placeholder spelling.
func yamlNodesDeepEqual(a, b *yaml.Node, paths Paths) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Kind != b.Kind || a.Tag != b.Tag || len(a.Content) != len(b.Content) {
		return false
	}
	if a.Kind == yaml.ScalarNode {
		return expandScalarForCompare(a.Value, paths) == expandScalarForCompare(b.Value, paths)
	}
	for i := range a.Content {
		if !yamlNodesDeepEqual(a.Content[i], b.Content[i], paths) {
			return false
		}
	}
	return true
}

// expandScalarForCompare expands ${CODDY_HOME} and ~, the two spellings a
// default and a DTO-round-tripped value of the same directory differ by, and
// normalizes every backslash to a forward slash: ExpandCODDYHomeOnly substitutes
// the raw home (C:\Users\... on Windows) while ExpandPathVars spells the
// yaml-safe form, so the same directory reaches the compare written either way.
// ${CWD} stays literal on purpose - see yamlNodesDeepEqual.
func expandScalarForCompare(s string, paths Paths) string {
	s = strings.ReplaceAll(s, "${CODDY_HOME}", yamlSafePath(paths.Home))
	return yamlSafePath(expandHome(s))
}
