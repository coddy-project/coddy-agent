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
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// SchemaURL is the hosted JSON Schema for config.yaml. It is published on the project
// site rather than served from a repository path so the address stays stable and any
// editor can resolve it without a checkout; docs/config.schema.json is the source the
// site mirrors.
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
	var next yaml.Node
	if err := next.Encode(escapeYAMLSecrets(cfg)); err != nil {
		return nil, fmt.Errorf("encode config: %w", err)
	}
	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{&next}}
	if prev, ok := parsePreviousDocument(existing); ok {
		// The leading comment block (the modeline and whatever follows it) hangs off
		// the document node, not off the first key.
		doc.HeadComment = prev.HeadComment
		doc.FootComment = prev.FootComment
		mergeYAMLComments(configDocumentRoot(prev), &next)
	}
	out, err := marshalConfigDocument(doc)
	if err != nil {
		return nil, fmt.Errorf("serialize config: %w", err)
	}
	return ensureSchemaModeline(out), nil
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
	}
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
