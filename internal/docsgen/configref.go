package docsgen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// schemaNode is the part of JSON Schema draft-07 the config schema uses, with
// property order preserved so the reference reads in the schema's order.
type schemaNode struct {
	Type                 any               `json:"type"`
	Description          string            `json:"description"`
	Default              json.RawMessage   `json:"default"`
	Enum                 []json.RawMessage `json:"enum"`
	Items                *schemaNode       `json:"items"`
	Properties           orderedProps      `json:"properties"`
	AdditionalProperties json.RawMessage   `json:"additionalProperties"`
}

// orderedProps keeps the keys of a "properties" object in file order.
type orderedProps struct {
	Keys  []string
	Nodes map[string]*schemaNode
}

func (o *orderedProps) UnmarshalJSON(b []byte) error {
	o.Nodes = map[string]*schemaNode{}
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return fmt.Errorf("properties: expected an object")
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := keyTok.(string)
		if !ok {
			return fmt.Errorf("properties: expected a key")
		}
		var node schemaNode
		if err := dec.Decode(&node); err != nil {
			return fmt.Errorf("properties.%s: %w", key, err)
		}
		o.Keys = append(o.Keys, key)
		o.Nodes[key] = &node
	}
	_, err = dec.Token() // closing brace
	return err
}

// topLevelOrder is the reading order of the top-level sections; keys the
// schema adds later are appended alphabetically.
var topLevelOrder = []string{
	"providers", "models", "agent", "prompts", "instructions", "skills", "rules",
	"mcp_servers", "mcp", "tools", "subagents", "hooks", "logger", "sessions",
	"compaction", "memory", "httpserver", "swarm", "ui", "scheduler", "gateways",
}

// FieldRow is one line of the reference table.
type FieldRow struct {
	Key, Type, Default, Description string
}

// ConfigReference renders the field tables of docs/reference/config.md from
// the schema and the resolved defaults (a flattened map keyed like the rows,
// "logger.level"), one section per top-level key.
func ConfigReference(schemaJSON []byte, defaults map[string]string) (string, error) {
	var root schemaNode
	if err := json.Unmarshal(schemaJSON, &root); err != nil {
		return "", fmt.Errorf("schema: %w", err)
	}
	keys := orderedTopLevel(root.Properties.Keys)
	var b strings.Builder
	for _, key := range keys {
		node := root.Properties.Nodes[key]
		fmt.Fprintf(&b, "\n### `%s`\n\n", key)
		if node.Description != "" {
			fmt.Fprintf(&b, "%s\n\n", strings.TrimSpace(node.Description))
		}
		rows := fieldRows(key, node, defaults, true)
		if len(rows) == 0 {
			continue
		}
		b.WriteString("| Key | Type | Default | Description |\n|-----|------|---------|-------------|\n")
		for _, r := range rows {
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", r.Key, r.Type, cell(r.Default), cell(r.Description))
		}
	}
	return strings.TrimLeft(b.String(), "\n"), nil
}

func orderedTopLevel(keys []string) []string {
	known := map[string]bool{}
	for _, k := range keys {
		known[k] = true
	}
	var out []string
	for _, k := range topLevelOrder {
		if known[k] {
			out = append(out, k)
			delete(known, k)
		}
	}
	var rest []string
	for k := range known {
		rest = append(rest, k)
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// fieldRows flattens a node into table rows. The section's own row is
// emitted only for lists and scalars (an object section is its table).
func fieldRows(path string, node *schemaNode, defaults map[string]string, top bool) []FieldRow {
	var rows []FieldRow
	kind := typeString(node)
	switch {
	case len(node.Properties.Keys) > 0:
		if !top {
			rows = append(rows, FieldRow{path, kind, "", node.Description})
		}
		for _, k := range node.Properties.Keys {
			rows = append(rows, fieldRows(path+"."+k, node.Properties.Nodes[k], defaults, false)...)
		}
	case node.Items != nil && len(node.Items.Properties.Keys) > 0:
		rows = append(rows, FieldRow{path, kind, defaultFor(path, node, defaults), node.Description})
		for _, k := range node.Items.Properties.Keys {
			rows = append(rows, fieldRows(path+"[]."+k, node.Items.Properties.Nodes[k], nil, false)...)
		}
	default:
		rows = append(rows, FieldRow{path, kind, defaultFor(path, node, defaults), node.Description})
	}
	return rows
}

func defaultFor(path string, node *schemaNode, defaults map[string]string) string {
	if len(node.Default) > 0 && string(node.Default) != "null" {
		return jsonScalar(node.Default)
	}
	if defaults != nil {
		if v, ok := defaults[path]; ok {
			return v
		}
	}
	return ""
}

// typeString renders the type column: "string", "integer or null", "list of
// strings", "list of objects", "map of strings", with enum values appended.
func typeString(node *schemaNode) string {
	names, nullable := typeNames(node.Type)
	var kind string
	switch {
	case len(names) == 0:
		kind = "any"
	case names[0] == "array":
		if node.Items == nil {
			kind = "list"
		} else if len(node.Items.Properties.Keys) > 0 {
			kind = "list of objects"
		} else {
			inner, _ := typeNames(node.Items.Type)
			if len(inner) > 0 {
				kind = "list of " + inner[0] + "s"
			} else {
				kind = "list"
			}
		}
	case names[0] == "object":
		if len(node.AdditionalProperties) > 0 && string(node.AdditionalProperties) != "false" {
			var inner schemaNode
			if err := json.Unmarshal(node.AdditionalProperties, &inner); err == nil {
				n, _ := typeNames(inner.Type)
				if len(n) > 0 {
					kind = "map of " + n[0] + "s"
					break
				}
			}
			kind = "map"
		} else {
			kind = "object"
		}
	default:
		kind = names[0]
	}
	if nullable {
		kind += " or null"
	}
	if len(node.Enum) > 0 {
		var vals []string
		for _, e := range node.Enum {
			if string(e) == "null" {
				continue
			}
			vals = append(vals, "`"+jsonScalar(e)+"`")
		}
		kind += ", one of " + strings.Join(vals, ", ")
	}
	return kind
}

func typeNames(t any) (names []string, nullable bool) {
	switch v := t.(type) {
	case string:
		return []string{v}, false
	case []any:
		for _, x := range v {
			s, _ := x.(string)
			if s == "null" {
				nullable = true
				continue
			}
			names = append(names, s)
		}
	}
	return names, nullable
}

// jsonScalar prints a JSON value the way it is written in YAML: strings
// without quotes, other values as compact JSON.
func jsonScalar(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" {
			return `""`
		}
		return s
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}

// cell escapes a table cell: pipes and newlines would break the row.
func cell(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	s = strings.ReplaceAll(s, "|", "\\|")
	if s == "" {
		return ""
	}
	return s
}

// FlattenDefaults turns a config value (any struct with yaml tags) into a
// map of dotted keys to printed values, the shape defaultFor looks up.
// Objects recurse, lists of scalars print as a YAML flow sequence, lists of
// objects and empty values are left out.
func FlattenDefaults(v any) (map[string]string, error) {
	data, err := yaml.Marshal(v)
	if err != nil {
		return nil, err
	}
	var generic map[string]any
	if err := yaml.Unmarshal(data, &generic); err != nil {
		return nil, err
	}
	out := map[string]string{}
	flattenInto(out, "", generic)
	return out, nil
}

func flattenInto(out map[string]string, prefix string, v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			flattenInto(out, key, val)
		}
	case []any:
		if len(x) == 0 {
			return
		}
		if _, isMap := x[0].(map[string]any); isMap {
			return
		}
		var parts []string
		for _, item := range x {
			parts = append(parts, fmt.Sprint(item))
		}
		out[prefix] = "[" + strings.Join(parts, ", ") + "]"
	case nil:
		return
	case string:
		if x != "" {
			out[prefix] = x
		}
	case bool:
		out[prefix] = fmt.Sprint(x)
	default:
		s := fmt.Sprint(x)
		if s != "0" {
			out[prefix] = s
		}
	}
}
