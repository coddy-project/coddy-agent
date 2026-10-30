package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtensionValuesSurviveJSONAndYAML(t *testing.T) {
	original := &Config{ExtensionValues: map[string]map[string]any{"demo": {"label": "hello ${EXTENSION_UNSET} $$", "enabled": true, "amount": 2.5}}}
	dto := ConfigToJSONDTO(original)
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseAndValidateConfigJSON(raw, Paths{})
	if err != nil {
		t.Fatal(err)
	}
	if back.ExtensionValues["demo"]["label"] != "hello ${EXTENSION_UNSET} $$" || back.ExtensionValues["demo"]["amount"] != 2.5 {
		t.Fatalf("round trip lost values: %#v", back.ExtensionValues)
	}
	dto.ExtensionValues["demo"]["label"] = "changed"
	if original.ExtensionValues["demo"]["label"] != "hello ${EXTENSION_UNSET} $$" {
		t.Fatal("DTO aliases live config")
	}
	data, err := MarshalConfigYAML(back)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := parseValidateYAMLBytes(expandConfigBody(string(data), Paths{}), Paths{})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ExtensionValues["demo"]["label"] != "hello ${EXTENSION_UNSET} $$" {
		t.Fatalf("YAML lost values: %s", data)
	}
}

func TestExtensionValuesRejectInvalidAndOversizedConfiguration(t *testing.T) {
	many := map[string]any{}
	for i := 0; i < 129; i++ {
		many[fmt.Sprintf("field-%d", i)] = true
	}
	for name, values := range map[string]map[string]map[string]any{
		"owner":  {"Invalid": {"field": true}},
		"field":  {"demo": {"Invalid": true}},
		"object": {"demo": {"field": map[string]any{"nested": true}}},
		"count":  {"demo": many},
		"size":   {"demo": {"field": strings.Repeat("x", 256<<10)}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateExtensionValues(values); err == nil {
				t.Fatal("invalid extension values accepted")
			}
		})
	}
}

func TestExtensionValuesEditPreservesConcurrentNamespaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	source := "# operator notes\r\nextension_values:\r\n  demo:\r\n    badge: before\r\n  other:\r\n    keep: old\r\n"
	base, err := parseValidateYAMLBytes(source, Paths{})
	if err != nil {
		t.Fatal(err)
	}
	currentSource := strings.Replace(source, "keep: old", "keep: new", 1)
	live, err := parseValidateYAMLBytes(currentSource, Paths{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(currentSource), 0600); err != nil {
		t.Fatal(err)
	}
	edited := JSONDTOToConfig(ConfigToJSONDTO(base), Paths{})
	edited.ExtensionValues["demo"]["badge"] = "literal ${EXTENSION_UNSET}"
	data, err := MarshalConfigYAMLForEdit(edited, base, live, path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := parseValidateYAMLBytes(expandConfigBody(string(data), Paths{}), Paths{})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ExtensionValues["other"]["keep"] != "new" || loaded.ExtensionValues["demo"]["badge"] != "literal ${EXTENSION_UNSET}" {
		t.Fatalf("merged values: %#v", loaded.ExtensionValues)
	}
	if !strings.Contains(string(data), "# operator notes\r\n") {
		t.Fatalf("lost comments or CRLF: %q", data)
	}
}
