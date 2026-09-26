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
// comments and the key order come from the file that is already there - and so does
// the spelling of every value the save leaves as it was, since the loader expands
// ${CODDY_HOME}, ${VAR} and ~ and the structs hold only what they stand for.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
// current bytes are existing. Values are cfg's; comments, key order, the operator's own
// schema modeline and the spelling of every value cfg did not change are carried over from
// existing. A missing, empty or unparsable previous document simply yields a freshly
// rendered one - a save must never fail because the file it replaces could not be read.
func MarshalConfigYAMLPreservingComments(cfg *Config, existing []byte) ([]byte, error) {
	return marshalOverDocument(cfg, existing, nil)
}

// MarshalConfigYAMLForFile renders cfg as the new content of the config file at path,
// preserving what that file already documents.
func MarshalConfigYAMLForFile(cfg *Config, path string) ([]byte, error) {
	return marshalOverDocument(cfg, readPreviousFile(path), nil)
}

// MarshalConfigYAMLForEdit renders cfg over the config file at path, where cfg is an edit
// of served: the configuration a client read (GET /coddy/config) and sent back changed.
// live is the configuration running now; a client never reads the write-only secrets, so
// the ones it left empty come back from live (ParseConfigJSONPreservingSecrets), and live
// is what they are compared with.
//
// What a client was served is not always what the file says. The process adjusts what it
// runs (coddy serve spells out the relay's listen address, a flag overrides the file's log
// level, a pairing token arrives from the environment), and the file may have changed since
// the client read it - another save, the agent's config_commit, a hand edit. A value the
// client sent back as it was served is not an edit, so for it the save writes what the file
// says now; only the values the client changed are taken from cfg. A list is one value: an
// edited list is written as the client sent it.
func MarshalConfigYAMLForEdit(cfg, served, live *Config, path string) ([]byte, error) {
	return marshalOverDocument(cfg, readPreviousFile(path), &editBase{served: served, live: live})
}

// editBase is what an edit is measured against: the configuration the client read and
// the one whose secrets it was handed back.
type editBase struct {
	served *Config
	live   *Config
}

func readPreviousFile(path string) []byte {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return raw
}

func marshalOverDocument(cfg *Config, existing []byte, edit *editBase) ([]byte, error) {
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
	var prevRoot, loaded *yaml.Node
	prev, havePrev := parsePreviousDocument(existing)
	if havePrev {
		prevRoot = configDocumentRoot(prev)
		// What the file says: the previous document as the loader reads it,
		// rendered the way next was, so the two compare value by value. It is what
		// decides that a spelling (${CODDY_HOME}/memory, ~/.agents/skills,
		// ${OPENAI_API_KEY}) still stands for the value being saved.
		loaded = loadedForm(existing, cfg.Paths)
	}
	// An edit writes what the file says for every value the client left as it
	// was served. Only against a file that loads: a file that is missing, empty
	// or broken says nothing a save could keep, and the configuration the server
	// runs is written whole, as it always was - restoring the "nothing" of a
	// deleted file would throw that configuration away.
	if edit != nil && loaded != nil {
		restoreUntouched(&next, savedFormWithSecretsOf(edit.served, edit.live), loaded, cfg.Paths)
	}
	if havePrev {
		// The leading comment block (the modeline and whatever follows it) hangs off
		// the document node, not off the first key.
		doc.HeadComment = prev.HeadComment
		doc.FootComment = prev.FootComment
		mergeYAMLComments(prevRoot, loaded, &next, cfg.Paths, true)
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
	pruneUndocumentedDefaults(&next, prevRoot, defaultsBaseline(cfg), loaded, cfg.Paths)
	out, err := marshalConfigDocument(doc)
	if err != nil {
		return nil, fmt.Errorf("serialize config: %w", err)
	}
	return applyLineEnding(ensureSchemaModeline(out), configLineEnding(original)), nil
}

// savedForm renders c the way a save renders the configuration it writes: through the
// JSON document the settings screen reads and sends back (GET and PUT /coddy/config,
// which spell out effective values and keep the write-only secrets), then encoded with
// its secrets "$"-escaped. Two configurations rendered this way compare field by field
// with the document a save is about to write. Nil when a step fails.
func savedForm(c *Config) *yaml.Node {
	return savedFormWithSecretsOf(c, c)
}

// savedFormWithSecretsOf is savedForm with the write-only secrets taken from secrets
// rather than from c: the configuration a client read, with the secrets a save hands
// back to a client that sent them empty.
func savedFormWithSecretsOf(c, secrets *Config) *yaml.Node {
	if c == nil {
		return nil
	}
	raw, err := json.Marshal(ConfigToJSONDTO(c))
	if err != nil {
		return nil
	}
	round, err := ParseConfigJSONPreservingSecrets(raw, c.Paths, secrets)
	if err != nil {
		return nil
	}
	var node yaml.Node
	if err := node.Encode(escapeYAMLSecrets(round)); err != nil {
		return nil
	}
	return &node
}

// loadedForm reads the previous document the way the loader does - ${CODDY_HOME}, ${VAR}
// and "$$" expanded over the whole text, then every field's own expansion and defaults -
// and renders the result like savedForm: what each spelling in the file stands for. Nil
// when the document does not load, which leaves a save only the checks it can make
// without the loader (see keepPreviousSpelling).
func loadedForm(existing []byte, paths Paths) *yaml.Node {
	cfg, err := parseValidateYAMLBytes(expandConfigBody(string(existing), paths), paths)
	if err != nil {
		return nil
	}
	return savedForm(cfg)
}

// restoreUntouched puts what the file says back into next wherever next still holds
// what served held: a value the client sent back as it was served is not an edit. The
// merge that follows finds the file's spelling for it, and the prune leaves it out when
// the file never had the key. The walk goes key by key through mappings, so an edit of
// one field leaves its neighbours to this rule; a list is one value, restored whole or
// not at all, because its entries carry no key to pair them with. served and loaded are
// renderings of the same struct as next (savedForm), so their keys are next's.
func restoreUntouched(next, served, loaded *yaml.Node, paths Paths) {
	if next == nil || served == nil || loaded == nil ||
		next.Kind != yaml.MappingNode || served.Kind != yaml.MappingNode || loaded.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(next.Content); i += 2 {
		key, val := next.Content[i].Value, next.Content[i+1]
		_, was, ok := mappingValue(served, key)
		if !ok {
			continue
		}
		_, says, ok := mappingValue(loaded, key)
		if !ok {
			continue
		}
		if val.Kind == yaml.MappingNode && was.Kind == yaml.MappingNode && says.Kind == yaml.MappingNode {
			restoreUntouched(val, was, says, paths)
			continue
		}
		if yamlNodesDeepEqual(val, was, paths) && !yamlNodesDeepEqual(val, says, paths) {
			next.Content[i+1] = cloneYAMLNode(says)
		}
	}
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

// mergeYAMLComments copies the comments of prev onto the matching nodes of next and
// keeps the file's own spelling of every value next did not change. loaded is prev as
// the loader reads it (loadedForm), nil when the previous document does not load.
// comments is false for an entry paired with a previous one only by its place in a
// list: it takes that entry's spelling, not its notes.
func mergeYAMLComments(prev, loaded, next *yaml.Node, paths Paths, comments bool) {
	if prev == nil || next == nil {
		return
	}
	if comments {
		adoptComments(prev, next)
	}
	switch {
	case prev.Kind == yaml.MappingNode && next.Kind == yaml.MappingNode:
		adoptFlowStyle(prev, next)
		mergeMappingComments(prev, nodeOfKind(loaded, yaml.MappingNode), next, paths, comments)
	case prev.Kind == yaml.SequenceNode && next.Kind == yaml.SequenceNode:
		// A list that loads as exactly the list being saved is written the way the
		// file has it: the [] the loader fills with its defaults, entries in their
		// places, their notes and their spelling.
		if l := nodeOfKind(loaded, yaml.SequenceNode); comments && l != nil && yamlNodesDeepEqual(next, l, paths) {
			*next = *cloneYAMLNode(prev)
			return
		}
		adoptFlowStyle(prev, next)
		mergeSequenceComments(prev, nodeOfKind(loaded, yaml.SequenceNode), next, paths, comments)
	case prev.Kind == yaml.ScalarNode && next.Kind == yaml.ScalarNode:
		keepPreviousSpelling(prev, nodeOfKind(loaded, yaml.ScalarNode), next, paths)
	}
}

// nodeOfKind returns n when it is of the given kind, nil otherwise, so a reading of the
// previous document that does not match its shape is not consulted.
func nodeOfKind(n *yaml.Node, kind yaml.Kind) *yaml.Node {
	if n == nil || n.Kind != kind {
		return nil
	}
	return n
}

// adoptFlowStyle keeps a list or a mapping the file wrote on one line ([stderr, file])
// on one line. An empty one ([], {}) says nothing about a preference: it has no other
// way to be written.
func adoptFlowStyle(prev, next *yaml.Node) {
	if prev.Style&yaml.FlowStyle != 0 && len(prev.Content) > 0 {
		next.Style |= yaml.FlowStyle
	}
}

// keepPreviousSpelling writes a value back the way the file spelled it when that
// spelling still loads as the value being saved.
//
// The load expands ${CODDY_HOME}, ${VAR} and ~, so the config a save renders holds
// what they stand for: an absolute path where the file wrote ${CODDY_HOME}/memory, a
// key the environment supplied where it wrote api_key: ${OPENAI_API_KEY}. Rendering
// that as it is cut the file loose from its home and its environment and put secrets
// into it, and every save from the settings screen did exactly that. A value that did
// not change keeps its quotes too. A value the operator changed loads from nothing
// the file wrote and is written as changed.
//
// loaded is the loader's reading of the spelling. Without it (the previous document no
// longer loads) only the expansions the loader applies can be tried on the text:
// ${CODDY_HOME} from paths, ${VAR} from the environment and a leading ~, also against
// the "$"-escaped form a secret field is rendered in.
func keepPreviousSpelling(prev, loaded, next *yaml.Node, paths Paths) {
	if prev.Value == next.Value {
		adoptSpelling(prev, next)
		return
	}
	if loaded != nil {
		if loaded.Value == next.Value {
			adoptSpelling(prev, next)
		}
		return
	}
	for _, expanded := range spellingExpansions(prev.Value, paths) {
		if expanded == next.Value || expanded == expandEnvEscaped(next.Value) {
			adoptSpelling(prev, next)
			return
		}
	}
}

// spellingExpansions lists what a spelling may load as when the loader cannot be asked:
// the expansion of the whole text (${CODDY_HOME}, ${VAR}), for a path a leading ~ as
// well, and the same path in the separators of the system, which is how the loader
// cleans the process-scoped directories (memory.dir comes back with backslashes on
// Windows). Nothing for a spelling that has neither a $ nor a leading ~.
func spellingExpansions(s string, paths Paths) []string {
	if !strings.Contains(s, "$") && !strings.HasPrefix(s, "~") {
		return nil
	}
	expanded := expandConfigText(s, paths)
	out := []string{expanded, expandHome(expanded)}
	if native := filepath.FromSlash(expanded); native != expanded {
		out = append(out, native)
	}
	return out
}

// adoptSpelling writes next the way prev was written: its text, its quotes and its
// tag. The tag goes with the text - max_turns: ${TURNS} is a string in the file that
// loads as an integer, and with next's integer tag the encoder would write it as
// !!int ${TURNS}; a string field the file wrote as a bare 30 would come back "30".
func adoptSpelling(prev, next *yaml.Node) {
	next.Value = prev.Value
	next.Style = prev.Style
	next.Tag = prev.Tag
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
func mergeMappingComments(prev, loaded, next *yaml.Node, paths Paths, comments bool) {
	for i := 0; i+1 < len(next.Content); i += 2 {
		key := next.Content[i].Value
		keyIndex, value, ok := mappingValue(prev, key)
		if !ok {
			continue
		}
		if comments {
			adoptComments(prev.Content[keyIndex], next.Content[i])
		}
		var reading *yaml.Node
		if loaded != nil {
			if _, v, ok := mappingValue(loaded, key); ok {
				reading = v
			}
		}
		mergeYAMLComments(value, reading, next.Content[i+1], paths, comments)
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
//
// What an entry's spelling loads as is its identity too, so ${CODDY_HOME}/skills pairs
// with the absolute directory a save renders for it and keeps its spelling. loaded, the
// loader's reading of the list, lines up with prev entry by entry: the loader keeps a
// list's entries and their order, and a reading of another length is not used. An entry
// that stays in its place is paired with its old self first, so two spellings of one
// directory each keep their own place.
//
// A renamed entry matches no identity, and its notes stay behind. When the list kept its
// length, the entry in its place is still the one it was renamed from, though: it takes
// that entry's spelling - an api_key: ${OPENAI_API_KEY} must not come out resolved because
// the provider got a new name - and leaves out the fields that entry never named.
func mergeSequenceComments(prev, loaded, next *yaml.Node, paths Paths, comments bool) {
	if loaded != nil && len(loaded.Content) != len(prev.Content) {
		loaded = nil
	}
	reading := func(j int) *yaml.Node {
		if loaded == nil {
			return nil
		}
		return loaded.Content[j]
	}
	// same reports whether item is prev entry j, by what j says or by what it loads as.
	same := func(item *yaml.Node, j int, byReading bool) bool {
		id, ok := sequenceItemIdentity(item)
		if !ok {
			return false
		}
		if !byReading {
			old, ok := sequenceItemIdentity(prev.Content[j])
			return ok && old == id
		}
		for _, old := range readingIdentities(prev.Content[j], reading(j), paths) {
			if old == id {
				return true
			}
		}
		return false
	}
	used := make([]bool, len(prev.Content))
	matched := make([]int, len(next.Content))
	for i, item := range next.Content {
		matched[i] = -1
		if i < len(prev.Content) && (same(item, i, false) || same(item, i, true)) {
			used[i], matched[i] = true, i
		}
	}
	for _, byReading := range []bool{false, true} {
		for i, item := range next.Content {
			if matched[i] >= 0 {
				continue
			}
			for j := range prev.Content {
				if !used[j] && same(item, j, byReading) {
					used[j], matched[i] = true, j
					break
				}
			}
		}
	}
	for i, item := range next.Content {
		if matched[i] >= 0 {
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
		used[i], matched[i] = true, i
	}
	byPlace := make([]bool, len(next.Content))
	if len(next.Content) == len(prev.Content) {
		for i := range next.Content {
			if matched[i] < 0 && !used[i] {
				used[i], matched[i], byPlace[i] = true, i, true
			}
		}
	}
	for i, j := range matched {
		if j < 0 {
			continue
		}
		mergeYAMLComments(prev.Content[j], reading(j), next.Content[i], paths, comments && !byPlace[i])
		leaveOutUnwrittenFields(prev.Content[j], reading(j), next.Content[i], paths)
	}
}

// readingIdentities are the identities the previous entry old may have as the loader
// reads it: the identity of its reading, or, when the previous document does not load,
// of each thing a ${VAR}, ${CODDY_HOME} or ~ spelling of a plain value may expand to
// (see spellingExpansions).
func readingIdentities(old, reading *yaml.Node, paths Paths) []string {
	if reading != nil {
		if id, ok := sequenceItemIdentity(reading); ok {
			return []string{id}
		}
		return nil
	}
	if old == nil || old.Kind != yaml.ScalarNode {
		return nil
	}
	var ids []string
	for _, expanded := range spellingExpansions(old.Value, paths) {
		if expanded != old.Value && expanded != "" {
			ids = append(ids, "="+expanded)
		}
	}
	return ids
}

// leaveOutUnwrittenFields drops from a list entry the fields its previous version did
// not write and that hold what the loader gives such a field. Rendering the struct
// spells out every field of an entry - "- name: spare\n  api_key: k" came back with an
// empty api_base, proxy and api_key_command and a zero timeout_ms - and the prune cannot
// tell those from values, because a list has no defaults to compare its entries with.
// The loader's reading of the previous entry is that comparison: a field the entry did
// not write whose value is still what its absence loads as says nothing, so it stays
// out. A field the operator wrote stays even at its zero, and so does one set now.
func leaveOutUnwrittenFields(prev, loaded, next *yaml.Node, paths Paths) {
	if prev == nil || loaded == nil || next == nil ||
		prev.Kind != yaml.MappingNode || loaded.Kind != yaml.MappingNode || next.Kind != yaml.MappingNode {
		return
	}
	kept := next.Content[:0]
	for i := 0; i+1 < len(next.Content); i += 2 {
		key, val := next.Content[i], next.Content[i+1]
		_, before, written := mappingValue(prev, key.Value)
		_, reading, known := mappingValue(loaded, key.Value)
		if !written && known && !nodeCarriesComment(key) && !nodeCarriesComment(val) &&
			yamlNodesDeepEqual(val, reading, paths) {
			continue
		}
		if written && known && val.Kind == yaml.MappingNode {
			leaveOutUnwrittenFields(before, reading, val, paths)
		}
		kept = append(kept, key, val)
	}
	next.Content = kept
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
	return savedForm(def)
}

// pruneUndocumentedDefaults removes from next every pair whose value says nothing:
// a null scalar (an unset optional field, including one the operator just cleared -
// null and absent load identically for every pointer field), a mapping left empty by
// pruning, and, only when the previous document never had the key, a subtree identical
// to the defaults baseline or to what the previous document loads the absent key as
// (loaded, which also covers a default one field takes from another). It runs after
// the comment merge, so a pair carrying an operator's note anywhere is always kept.
// Sequences are compared as units: an empty list can be a deliberate value
// (reasoning_levels: []), and entries carry fields that are meaningful even at zero.
func pruneUndocumentedDefaults(next, prev, baseline, loaded *yaml.Node, paths Paths) {
	if next == nil || next.Kind != yaml.MappingNode {
		return
	}
	kept := next.Content[:0]
	for i := 0; i+1 < len(next.Content); i += 2 {
		key, val := next.Content[i], next.Content[i+1]
		var prevVal, baseVal, loadedVal *yaml.Node
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
		if loaded != nil && loaded.Kind == yaml.MappingNode {
			if _, v, ok := mappingValue(loaded, key.Value); ok {
				loadedVal = v
			}
		}
		// Children decide before the parent can empty out.
		switch val.Kind {
		case yaml.MappingNode:
			pruneUndocumentedDefaults(val, prevVal, baseVal, loadedVal, paths)
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
		if prevVal == nil && ((baseVal != nil && yamlNodesDeepEqual(val, baseVal, paths)) ||
			(loadedVal != nil && yamlNodesDeepEqual(val, loadedVal, paths))) {
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
