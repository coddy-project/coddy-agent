package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// kimiChatNormalizeSchema resolves local references and fills missing property types.
func kimiChatNormalizeSchema(value any) (map[string]any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if err = json.Unmarshal(data, &root); err != nil || root == nil {
		return nil, errors.New("kimi chat: tool schema must be an object")
	}
	resolved, ok := kimiChatResolveSchema(root, root, map[string]bool{}).(map[string]any)
	if !ok {
		return nil, errors.New("kimi chat: resolved tool schema must be an object")
	}
	for _, bucket := range []string{"$defs", "definitions"} {
		if !kimiChatHasRef(resolved, "#/"+bucket+"/", bucket) {
			delete(resolved, bucket)
		}
	}
	if err = kimiChatSchemaChildren(resolved); err != nil {
		return nil, err
	}
	return resolved, nil
}

func kimiChatResolveSchema(v any, root map[string]any, seen map[string]bool) any {
	switch node := v.(type) {
	case []any:
		out := make([]any, len(node))
		for i, x := range node {
			out[i] = kimiChatResolveSchema(x, root, seen)
		}
		return out
	case map[string]any:
		if ref, ok := node["$ref"].(string); ok && (ref == "#" || strings.HasPrefix(ref, "#/")) && !seen[ref] {
			var target any = root
			if ref != "#" {
				for _, part := range strings.Split(ref[2:], "/") {
					part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
					switch x := target.(type) {
					case map[string]any:
						target = x[part]
					case []any:
						i, e := strconv.Atoi(part)
						if e != nil || i < 0 || i >= len(x) {
							target = nil
						} else {
							target = x[i]
						}
					default:
						target = nil
					}
				}
			}
			if target != nil {
				seen[ref] = true
				result := kimiChatResolveSchema(target, root, seen)
				delete(seen, ref)
				if m, ok := result.(map[string]any); ok {
					for k, v := range node {
						if k != "$ref" {
							m[k] = kimiChatResolveSchema(v, root, seen)
						}
					}
					return m
				}
				return result
			}
		}
		out := make(map[string]any, len(node))
		for k, x := range node {
			out[k] = kimiChatResolveSchema(x, root, seen)
		}
		return out
	default:
		return v
	}
}

func kimiChatHasRef(v any, prefix, skip string) bool {
	switch x := v.(type) {
	case []any:
		for _, v := range x {
			if kimiChatHasRef(v, prefix, skip) {
				return true
			}
		}
	case map[string]any:
		if ref, ok := x["$ref"].(string); ok && strings.HasPrefix(ref, prefix) {
			return true
		}
		for k, v := range x {
			if k != skip && kimiChatHasRef(v, prefix, skip) {
				return true
			}
		}
	}
	return false
}

func kimiChatSchemaChildren(node map[string]any) error {
	for _, key := range []string{"$defs", "definitions", "dependencies", "dependentSchemas", "patternProperties", "properties"} {
		if m, ok := node[key].(map[string]any); ok {
			for _, v := range m {
				if child, ok := v.(map[string]any); ok {
					if err := kimiChatSchemaProperty(child); err != nil {
						return err
					}
				}
			}
		}
	}
	for _, key := range []string{"additionalItems", "additionalProperties", "contains", "contentSchema", "else", "if", "not", "propertyNames", "then", "unevaluatedItems", "unevaluatedProperties", "items", "allOf", "anyOf", "oneOf", "prefixItems"} {
		switch v := node[key].(type) {
		case map[string]any:
			if err := kimiChatSchemaProperty(v); err != nil {
				return err
			}
		case []any:
			for _, x := range v {
				if m, ok := x.(map[string]any); ok {
					if err := kimiChatSchemaProperty(m); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func kimiChatSchemaProperty(m map[string]any) error {
	skip := false
	for _, k := range []string{"$ref", "allOf", "anyOf", "else", "if", "not", "oneOf", "then"} {
		if _, ok := m[k]; ok {
			skip = true
			break
		}
	}
	if !skip {
		var values []any
		if e, ok := m["enum"].([]any); ok && len(e) > 0 {
			values = e
		} else if v, ok := m["const"]; ok {
			values = []any{v}
		}
		if len(values) > 0 {
			typ := ""
			for _, v := range values {
				t := ""
				switch x := v.(type) {
				case nil:
					t = "null"
				case bool:
					t = "boolean"
				case string:
					t = "string"
				case float64:
					if x == float64(int64(x)) {
						t = "integer"
					} else {
						t = "number"
					}
				case []any:
					t = "array"
				case map[string]any:
					t = "object"
				}
				if typ == "" {
					typ = t
				} else if typ != t {
					if (typ == "integer" && t == "number") || (typ == "number" && t == "integer") {
						typ = "number"
					} else {
						return fmt.Errorf("kimi chat: mixed enum types")
					}
				}
			}
			m["type"] = typ
		} else if _, ok := m["type"]; !ok {
			typ := ""
			for _, group := range []struct {
				typ  string
				keys []string
			}{{"object", []string{"properties", "required", "additionalProperties", "patternProperties", "dependencies", "dependentSchemas", "dependentRequired", "maxProperties", "minProperties"}}, {"array", []string{"items", "prefixItems", "contains", "maxItems", "minItems", "uniqueItems"}}, {"string", []string{"format", "pattern", "minLength", "maxLength"}}, {"number", []string{"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf"}}} {
				found := false
				for _, k := range group.keys {
					if _, ok := m[k]; ok {
						found = true
						break
					}
				}
				if found {
					typ = group.typ
					break
				}
			}
			if typ != "" {
				m["type"] = typ
			}
		}
	}
	return kimiChatSchemaChildren(m)
}
