package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// docsSchemaPath is the published editor-facing JSON Schema for config.yaml.
const docsSchemaPath = "../../docs/config.schema.json"

func loadDocsSchema(t *testing.T) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(docsSchemaPath))
	if err != nil {
		t.Fatalf("read %s: %v (the schema must be committed alongside the config structs)", docsSchemaPath, err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse %s: %v", docsSchemaPath, err)
	}
	return doc
}

// yamlFieldName returns the effective YAML key for a struct field, or "" when skipped.
func yamlFieldName(f reflect.StructField) string {
	tag := f.Tag.Get("yaml")
	name := tag
	if c := strings.IndexByte(tag, ','); c >= 0 {
		name = tag[:c]
	}
	if name == "-" {
		return ""
	}
	if name == "" {
		return strings.ToLower(f.Name)
	}
	return name
}

// schemaTypeForGoType maps a Go config field type to the expected JSON Schema "type" value.
func schemaTypeForGoType(t reflect.Type) string {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Struct, reflect.Map:
		return "object"
	default:
		return ""
	}
}

// checkSchemaNodeMatchesType recursively verifies that a schema node describes goType.
func checkSchemaNodeMatchesType(t *testing.T, path string, goType reflect.Type, node map[string]interface{}) {
	t.Helper()
	if goType.Kind() == reflect.Pointer {
		goType = goType.Elem()
	}
	wantType := schemaTypeForGoType(goType)
	gotType, _ := node["type"].(string)
	if gotType != wantType {
		t.Errorf("%s: schema type %q, want %q", path, gotType, wantType)
		return
	}
	switch goType.Kind() {
	case reflect.Slice, reflect.Array:
		items, ok := node["items"].(map[string]interface{})
		if !ok {
			t.Errorf("%s: array schema missing items", path)
			return
		}
		checkSchemaNodeMatchesType(t, path+"[]", goType.Elem(), items)
	case reflect.Struct:
		props, ok := node["properties"].(map[string]interface{})
		if !ok {
			t.Errorf("%s: object schema missing properties", path)
			return
		}
		if ap, ok := node["additionalProperties"].(bool); !ok || ap {
			t.Errorf("%s: object schema must set additionalProperties:false so editors flag typos", path)
		}
		want := map[string]reflect.Type{}
		for i := 0; i < goType.NumField(); i++ {
			f := goType.Field(i)
			name := yamlFieldName(f)
			if name == "" {
				continue
			}
			want[name] = f.Type
		}
		for name, ft := range want {
			sub, ok := props[name].(map[string]interface{})
			if !ok {
				t.Errorf("%s: schema missing property %q (add it to docs/config.schema.json)", path, name)
				continue
			}
			checkSchemaNodeMatchesType(t, path+"."+name, ft, sub)
		}
		for name := range props {
			if _, ok := want[name]; !ok {
				t.Errorf("%s: schema has property %q not present in the Go config structs (remove it or add the field)", path, name)
			}
		}
	}
}

// TestDocsConfigSchemaMatchesStructs keeps docs/config.schema.json in sync with the
// yaml-tagged config structs: every YAML key must appear in the schema with the right
// type, and the schema must not describe keys the loader does not know.
func TestDocsConfigSchemaMatchesStructs(t *testing.T) {
	doc := loadDocsSchema(t)
	checkSchemaNodeMatchesType(t, "$", reflect.TypeOf(Config{}), doc)
}

// TestDocsConfigSchemaEnums pins schema enums to the constants the loader validates against.
func TestDocsConfigSchemaEnums(t *testing.T) {
	doc := loadDocsSchema(t)

	enumAt := func(pathParts ...string) []string {
		node := doc
		for i, p := range pathParts {
			var ok bool
			if node, ok = node[p].(map[string]interface{}); !ok {
				t.Fatalf("schema path %v: segment %q not found", pathParts, pathParts[i])
			}
		}
		raw, ok := node["enum"].([]interface{})
		if !ok {
			t.Fatalf("schema path %v: enum missing", pathParts)
		}
		out := make([]string, 0, len(raw))
		for _, v := range raw {
			s, _ := v.(string)
			out = append(out, s)
		}
		return out
	}

	assertSet := func(name string, got []string, want map[string]struct{}) {
		t.Helper()
		gotSet := map[string]struct{}{}
		for _, v := range got {
			gotSet[v] = struct{}{}
		}
		if len(gotSet) != len(want) {
			t.Errorf("%s: enum %v, want keys of %v", name, got, want)
			return
		}
		for k := range want {
			if _, ok := gotSet[k]; !ok {
				t.Errorf("%s: enum %v missing %q", name, got, k)
			}
		}
	}

	assertSet("providers[].type",
		enumAt("properties", "providers", "items", "properties", "type"),
		AllowedLLMProviderTypes)

	assertSet("tools.permission_mode",
		enumAt("properties", "tools", "properties", "permission_mode"),
		map[string]struct{}{PermModeAsk: {}, PermModeAcceptEdits: {}, PermModeBypass: {}})

	assertSet("logger.format",
		enumAt("properties", "logger", "properties", "format"),
		map[string]struct{}{LogFormatText: {}, LogFormatJSON: {}})

	assertSet("logger.level",
		enumAt("properties", "logger", "properties", "level"),
		map[string]struct{}{
			LogLevelDebug: {}, LogLevelInfo: {}, LogLevelWarn: {}, LogLevelError: {},
			// The loader also accepts "warning" and normalises it to "warn".
			"warning": {},
		})

	assertSet("logger.outputs[]",
		enumAt("properties", "logger", "properties", "outputs", "items"),
		map[string]struct{}{LogOutputStdout: {}, LogOutputStderr: {}, LogOutputFile: {}})

	assertSet("gateways.telegram.default_isolation",
		enumAt("properties", "gateways", "properties", "telegram", "properties", "default_isolation"),
		map[string]struct{}{
			string(IsolationIndividual): {}, string(IsolationShared): {}, string(IsolationAdmin): {},
		})
}

// canonicalSchemaURL is the hosted, absolute JSON Schema URL that YAML language
// servers must be able to resolve without a local checkout. The refs/heads/main
// form is the canonical raw.githubusercontent.com reference GitHub itself emits;
// the shorter /main/ form is avoided so editors resolve the schema reliably.
const canonicalSchemaURL = "https://raw.githubusercontent.com/coddy-project/coddy-agent/refs/heads/main/docs/config.schema.json"

// TestConfigSchemaURLIsCanonical guards that every published $schema reference and
// the schema's own $id use the canonical hosted URL, so config.yaml highlighting
// works for users who never clone the repository.
func TestConfigSchemaURLIsCanonical(t *testing.T) {
	// The schema's own identifier.
	schema := loadDocsSchema(t)
	if got, _ := schema["$id"].(string); got != canonicalSchemaURL {
		t.Errorf("docs/config.schema.json $id = %q, want %q", got, canonicalSchemaURL)
	}

	// Every file that ships the yaml-language-server header for users/editors.
	header := "# yaml-language-server: $schema=" + canonicalSchemaURL
	for _, rel := range []string{
		"../../config.example.yaml",
		"../../docs/config.md",
		"../../docs/config-reference.md",
	} {
		data, err := os.ReadFile(filepath.Clean(rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if !strings.Contains(string(data), header) {
			t.Errorf("%s: missing canonical schema header %q", rel, header)
		}
		// The non-canonical /main/ form (without refs/heads) must not linger.
		stale := "$schema=https://raw.githubusercontent.com/coddy-project/coddy-agent/main/docs/config.schema.json"
		if strings.Contains(string(data), stale) {
			t.Errorf("%s: still references non-canonical schema URL %q", rel, stale)
		}
	}
}
