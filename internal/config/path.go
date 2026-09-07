package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

const redactedConfigValue = "<redacted>"

var configPathWriteMu sync.Mutex

// ConfigPathValue is a redacted value read from the active YAML config.
type ConfigPathValue struct {
	Exists   bool
	Value    interface{}
	Redacted bool
}

type configPathSelector struct {
	field string
	value string
}

type configPathToken struct {
	key      string
	selector *configPathSelector
}

// ReadConfigPath reads one path from the active YAML file. Paths are dotted
// uci-style ("mcp_servers[name=context7].command"); legacy slash paths are
// still accepted. Secret-shaped values are redacted.
func ReadConfigPath(paths Paths, path string) (*ConfigPathValue, error) {
	tokens, err := parseAnyConfigPath(path)
	if err != nil {
		return nil, err
	}
	raw, err := readConfigBytes(paths.ConfigPath)
	if err != nil {
		return nil, err
	}
	doc, err := parseConfigDocument(raw)
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	node, exists, err := findConfigNode(configDocumentRoot(doc), tokens)
	if err != nil {
		return nil, err
	}
	if !exists {
		return &ConfigPathValue{Exists: false}, nil
	}

	copy := cloneYAMLNode(node)
	keys := configPathKeys(tokens)
	redacted := false
	if configSecretPath(keys) {
		setRedactedNode(copy)
		redacted = true
	} else {
		redacted = redactConfigNode(copy, keys)
	}
	var value interface{}
	if err := copy.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode config value: %w", err)
	}
	return &ConfigPathValue{Exists: true, Value: value, Redacted: redacted}, nil
}

func readConfigBytes(path string) ([]byte, error) {
	raw, _, err := readExistingConfigBytes(path)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return []byte("{}\n"), nil
	}
	return raw, nil
}

func readExistingConfigBytes(path string) ([]byte, bool, error) {
	if strings.TrimSpace(path) == "" {
		return nil, false, fmt.Errorf("config path is empty")
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read config: %w", err)
	}
	return raw, true, nil
}

func parseConfigDocument(raw []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if doc.Kind == 0 {
		doc.Kind = yaml.DocumentNode
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	root := configDocumentRoot(&doc)
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config root must be a mapping")
	}
	return &doc, nil
}

func configDocumentRoot(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) == 0 {
			doc.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
		}
		return doc.Content[0]
	}
	return doc
}

func marshalConfigDocument(doc *yaml.Node) ([]byte, error) {
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func parseConfigPath(input string) ([]configPathToken, error) {
	path := strings.TrimSpace(input)
	if path == "/" {
		return nil, nil
	}
	if path == "" || !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("config path must start with /")
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	tokens := make([]configPathToken, 0, len(parts))
	for _, raw := range parts {
		part := strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~")
		if part == "" {
			return nil, fmt.Errorf("config path contains an empty segment")
		}
		token, err := parseConfigSegment(part)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, token)
	}
	return tokens, nil
}

// parseConfigSegment parses one path segment with an optional [field=value]
// selector attached to a sequence field name.
func parseConfigSegment(part string) (configPathToken, error) {
	token := configPathToken{key: part}
	if open := strings.IndexByte(part, '['); open > 0 && strings.HasSuffix(part, "]") {
		selector := strings.TrimSuffix(part[open+1:], "]")
		field, value, ok := strings.Cut(selector, "=")
		if !ok || strings.TrimSpace(field) == "" || strings.TrimSpace(value) == "" {
			return configPathToken{}, fmt.Errorf("invalid selector in config path segment %q", part)
		}
		token.key = part[:open]
		token.selector = &configPathSelector{field: strings.TrimSpace(field), value: strings.TrimSpace(value)}
	}
	return token, nil
}

func configSchemaType(tokens []configPathToken) (reflect.Type, error) {
	typ := reflect.TypeOf(Config{})
	for _, token := range tokens {
		for typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		switch typ.Kind() {
		case reflect.Struct:
			field, ok := yamlStructField(typ, token.key)
			if !ok {
				return nil, fmt.Errorf("unknown config path segment %q", token.key)
			}
			typ = field.Type
			if token.selector != nil {
				for typ.Kind() == reflect.Pointer {
					typ = typ.Elem()
				}
				if typ.Kind() != reflect.Slice && typ.Kind() != reflect.Array {
					return nil, fmt.Errorf("config path selector requires a sequence at %q", token.key)
				}
				typ = typ.Elem()
				selectorType := typ
				for selectorType.Kind() == reflect.Pointer {
					selectorType = selectorType.Elem()
				}
				if selectorType.Kind() != reflect.Struct {
					return nil, fmt.Errorf("config path selector requires object entries at %q", token.key)
				}
				if _, ok := yamlStructField(selectorType, token.selector.field); !ok {
					return nil, fmt.Errorf("unknown selector field %q at %q", token.selector.field, token.key)
				}
			}
		case reflect.Slice, reflect.Array:
			if token.selector != nil {
				return nil, fmt.Errorf("selectors must be attached to a sequence field")
			}
			if token.key != "-" {
				if _, err := strconv.Atoi(token.key); err != nil {
					return nil, fmt.Errorf("sequence path segment %q must be an index or -", token.key)
				}
			}
			typ = typ.Elem()
		default:
			return nil, fmt.Errorf("config path continues through scalar segment %q", token.key)
		}
	}
	return typ, nil
}

func yamlStructField(typ reflect.Type, name string) (reflect.StructField, bool) {
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := strings.Split(field.Tag.Get("yaml"), ",")[0]
		if tag == "" {
			tag = strings.ToLower(field.Name)
		}
		if tag == name {
			return field, true
		}
	}
	return reflect.StructField{}, false
}

func findConfigNode(root *yaml.Node, tokens []configPathToken) (*yaml.Node, bool, error) {
	current := root
	for _, token := range tokens {
		switch current.Kind {
		case yaml.MappingNode:
			_, child, ok := mappingValue(current, token.key)
			if !ok {
				return nil, false, nil
			}
			current = child
			if token.selector != nil {
				if current.Kind != yaml.SequenceNode {
					return nil, false, fmt.Errorf("config path %q is not a sequence", token.key)
				}
				_, current, ok = sequenceMatch(current, token.selector)
				if !ok {
					return nil, false, nil
				}
			}
		case yaml.SequenceNode:
			idx, err := sequenceIndex(token.key, len(current.Content), false)
			if err != nil {
				return nil, false, err
			}
			current = current.Content[idx]
		default:
			return nil, false, fmt.Errorf("config path continues through a scalar")
		}
	}
	return current, true, nil
}

func mutateConfigNode(current *yaml.Node, tokens []configPathToken, replacement *yaml.Node, deleteValue bool) (bool, error) {
	token := tokens[0]
	last := len(tokens) == 1
	switch current.Kind {
	case yaml.MappingNode:
		keyIndex, child, exists := mappingValue(current, token.key)
		if last && token.selector == nil {
			if deleteValue {
				if !exists {
					return false, nil
				}
				current.Content = append(current.Content[:keyIndex], current.Content[keyIndex+2:]...)
				return true, nil
			}
			if exists {
				current.Content[keyIndex+1] = cloneYAMLNode(replacement)
			} else {
				current.Content = append(current.Content, scalarYAMLNode(token.key), cloneYAMLNode(replacement))
			}
			return true, nil
		}

		if token.selector != nil {
			if !exists {
				if deleteValue {
					return false, nil
				}
				child = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
				current.Content = append(current.Content, scalarYAMLNode(token.key), child)
			}
			if child.Kind != yaml.SequenceNode {
				return false, fmt.Errorf("config path %q is not a sequence", token.key)
			}
			idx, entry, found := sequenceMatch(child, token.selector)
			if last {
				if deleteValue {
					if !found {
						return false, nil
					}
					child.Content = append(child.Content[:idx], child.Content[idx+1:]...)
					return true, nil
				}
				next := cloneYAMLNode(replacement)
				if err := enforceSelector(next, token.selector); err != nil {
					return false, err
				}
				if found {
					child.Content[idx] = next
				} else {
					child.Content = append(child.Content, next)
				}
				return true, nil
			}
			if !found {
				entry = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
					scalarYAMLNode(token.selector.field), scalarYAMLNode(token.selector.value),
				}}
				child.Content = append(child.Content, entry)
			}
			return mutateConfigNode(entry, tokens[1:], replacement, deleteValue)
		}

		if !exists {
			if deleteValue {
				return false, nil
			}
			child = newConfigContainer(tokens[1])
			current.Content = append(current.Content, scalarYAMLNode(token.key), child)
		}
		if child.Kind != yaml.MappingNode && child.Kind != yaml.SequenceNode {
			return false, fmt.Errorf("config path %q continues through a scalar", token.key)
		}
		return mutateConfigNode(child, tokens[1:], replacement, deleteValue)

	case yaml.SequenceNode:
		idx, err := sequenceIndex(token.key, len(current.Content), !deleteValue)
		if err != nil {
			if deleteValue && strings.Contains(err.Error(), "out of range") {
				return false, nil
			}
			return false, err
		}
		if last {
			if deleteValue {
				current.Content = append(current.Content[:idx], current.Content[idx+1:]...)
				return true, nil
			}
			next := cloneYAMLNode(replacement)
			if idx == len(current.Content) {
				current.Content = append(current.Content, next)
			} else {
				current.Content[idx] = next
			}
			return true, nil
		}
		if idx == len(current.Content) {
			if deleteValue {
				return false, nil
			}
			current.Content = append(current.Content, newConfigContainer(tokens[1]))
		}
		child := current.Content[idx]
		if child.Kind != yaml.MappingNode && child.Kind != yaml.SequenceNode {
			return false, fmt.Errorf("config sequence path continues through a scalar")
		}
		return mutateConfigNode(child, tokens[1:], replacement, deleteValue)
	default:
		return false, fmt.Errorf("config path continues through a scalar")
	}
}

func mappingValue(node *yaml.Node, key string) (int, *yaml.Node, bool) {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return i, node.Content[i+1], true
		}
	}
	return -1, nil, false
}

func sequenceMatch(node *yaml.Node, selector *configPathSelector) (int, *yaml.Node, bool) {
	for i, entry := range node.Content {
		if entry.Kind != yaml.MappingNode {
			continue
		}
		_, value, ok := mappingValue(entry, selector.field)
		if ok && value.Kind == yaml.ScalarNode && value.Value == selector.value {
			return i, entry, true
		}
	}
	return -1, nil, false
}

func enforceSelector(node *yaml.Node, selector *configPathSelector) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("selected sequence entries must be JSON objects")
	}
	idx, _, ok := mappingValue(node, selector.field)
	if ok {
		node.Content[idx+1] = scalarYAMLNode(selector.value)
	} else {
		node.Content = append(node.Content, scalarYAMLNode(selector.field), scalarYAMLNode(selector.value))
	}
	return nil
}

func sequenceIndex(raw string, length int, allowAppend bool) (int, error) {
	if raw == "-" {
		if allowAppend {
			return length, nil
		}
		return 0, fmt.Errorf("- is only valid when appending a config value")
	}
	idx, err := strconv.Atoi(raw)
	if err != nil || idx < 0 {
		return 0, fmt.Errorf("config sequence index %q is invalid", raw)
	}
	if idx > length || (!allowAppend && idx == length) {
		return 0, fmt.Errorf("config sequence index %d is out of range", idx)
	}
	return idx, nil
}

func newConfigContainer(next configPathToken) *yaml.Node {
	if next.key == "-" {
		return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	}
	if _, err := strconv.Atoi(next.key); err == nil {
		return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	}
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
}

func scalarYAMLNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func cloneYAMLNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	copy := *node
	copy.Content = make([]*yaml.Node, len(node.Content))
	for i, child := range node.Content {
		copy.Content[i] = cloneYAMLNode(child)
	}
	return &copy
}

func configPathKeys(tokens []configPathToken) []string {
	keys := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if token.key == "-" {
			continue
		}
		if _, err := strconv.Atoi(token.key); err == nil {
			continue
		}
		keys = append(keys, token.key)
	}
	return keys
}

func redactConfigNode(node *yaml.Node, path []string) bool {
	redacted := false
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i].Value
			value := node.Content[i+1]
			nextPath := appendPath(path, key)
			if configSecretPath(nextPath) {
				setRedactedNode(value)
				redacted = true
				continue
			}
			if redactConfigNode(value, nextPath) {
				redacted = true
			}
		}
	case yaml.SequenceNode:
		for _, child := range node.Content {
			if redactConfigNode(child, path) {
				redacted = true
			}
		}
	}
	return redacted
}

func appendPath(path []string, key string) []string {
	out := make([]string, len(path), len(path)+1)
	copy(out, path)
	return append(out, key)
}

func configSecretPath(path []string) bool {
	if len(path) == 0 {
		return false
	}
	last := strings.ToLower(strings.ReplaceAll(path[len(path)-1], "-", "_"))
	switch last {
	case "api_key", "api_key_command", "auth_token", "token", "proxy", "password", "secret":
		return true
	case "value":
		for _, part := range path[:len(path)-1] {
			switch strings.ToLower(part) {
			case "env", "headers":
				return true
			}
		}
	}
	return strings.Contains(last, "password") || strings.HasSuffix(last, "_secret")
}

func setRedactedNode(node *yaml.Node) {
	*node = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: redactedConfigValue}
}
