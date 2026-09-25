package skills_test

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

// The standard delivery: what the binary carries and hands to a home. The
// system skill configure-coddy is written here; the rpa-* workflow skills are
// vendored from their own repositories by scripts/vendor-bundled-skills.sh.
var deliveredSkills = []string{
	"configure-coddy",
	"rpa-bugfix",
	"rpa-feat",
	"rpa-gen-rules",
	"rpa-init",
}

func TestBundledCarriesTheDelivery(t *testing.T) {
	b := skills.Bundled()
	found := make(map[string]bool, len(b))
	for _, skill := range b {
		found[skills.CanonicalCommandName(skill)] = true
	}
	for _, name := range deliveredSkills {
		if !found[name] {
			t.Errorf("delivered skill %q missing from %+v", name, found)
		}
	}
	if len(b) != len(deliveredSkills) {
		t.Errorf("expected %d delivered skills, got %d: %+v", len(deliveredSkills), len(b), found)
	}
}

// Every delivered skill declares a version: it is what decides whether a
// release replaces the copy in an operator's home.
func TestBundledSkillsDeclareAVersion(t *testing.T) {
	for _, e := range skills.BundledEntries() {
		if e.Version == "" {
			t.Errorf("delivered skill %q has no version in its SKILL.md frontmatter", e.Name)
		}
	}
}

func TestLoadAllPrependsBundled(t *testing.T) {
	loader := skills.NewLoader(nil)
	all, err := loader.LoadAll(".", "")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, s := range all {
		found[skills.CanonicalCommandName(s)] = true
	}
	for _, name := range deliveredSkills {
		if !found[name] {
			t.Fatalf("delivered skill %q missing from LoadAll: %+v", name, found)
		}
	}
}

// configure-coddy is the agent-facing copy of the config surface, so every key
// it names has to exist. A snake_case name written as code is a key or an enum
// value the config schema declares, or the name of a tool; a dotted path that
// starts at a top-level key (agent.model, models.N.reasoning_levels) resolves
// through the schema. A name that lives only in an HTTP response
// (default_agent_model of GET /v1/models) or a mistyped path sends the agent to
// stage a key that config.yaml does not have.
func TestConfigureCoddyNamesOnlyRealKeys(t *testing.T) {
	var body string
	for _, s := range skills.Bundled() {
		if skills.CanonicalCommandName(s) == "configure-coddy" {
			body = s.Content
			break
		}
	}
	if body == "" {
		t.Fatal("configure-coddy is missing from skills.Bundled()")
	}
	var schema map[string]interface{}
	if err := json.Unmarshal(config.ConfigSchemaJSON(), &schema); err != nil {
		t.Fatal(err)
	}

	known := map[string]bool{}
	var collect func(interface{})
	collect = func(n interface{}) {
		switch v := n.(type) {
		case map[string]interface{}:
			if props, ok := v["properties"].(map[string]interface{}); ok {
				for k := range props {
					known[k] = true
				}
			}
			if enum, ok := v["enum"].([]interface{}); ok {
				for _, e := range enum {
					if s, ok := e.(string); ok {
						known[s] = true
					}
				}
			}
			for _, child := range v {
				collect(child)
			}
		case []interface{}:
			for _, child := range v {
				collect(child)
			}
		}
	}
	collect(schema)
	for _, def := range tools.NewRegistryFor(nil).AllToolDefinitions() {
		known[def.Name] = true
	}

	top, _ := schema["properties"].(map[string]interface{})
	snake := regexp.MustCompile(`^[a-z][a-z0-9]*(?:_[a-z0-9]+)+$`)
	word := regexp.MustCompile(`[A-Za-z0-9_.]+`)
	command := regexp.MustCompile(`^(?:set|delete|add_list|del_list) ((?:[A-Za-z0-9_.]|\[[^\]]*\])+)`)
	selector := regexp.MustCompile(`\[[^\]]*\]`)
	for _, span := range regexp.MustCompile("`([^`\n]+)`").FindAllStringSubmatch(body, -1) {
		code := span[1]
		// A command the agent copies into config_set: its whole path resolves,
		// a one-segment key and a selected entry (mcp_servers[name=x]) included.
		if m := command.FindStringSubmatch(code); m != nil {
			path := selector.ReplaceAllString(m[1], ".N")
			if !schemaPathExists(schema, strings.Split(path, ".")) {
				t.Errorf("configure-coddy stages %q, a path the schema does not declare", m[1])
			}
			continue
		}
		if snake.MatchString(code) && !known[code] {
			t.Errorf("configure-coddy names %q, which is neither a config key nor a tool", code)
		}
		for _, loc := range word.FindAllStringIndex(code, -1) {
			// A part of a path or an address (.coddy/mcp.json, api.neuraldeep.ru)
			// is not a config key.
			if loc[0] > 0 && strings.ContainsRune("/.$-~", rune(code[loc[0]-1])) {
				continue
			}
			segs := strings.Split(strings.Trim(code[loc[0]:loc[1]], "."), ".")
			if len(segs) < 2 || top[segs[0]] == nil {
				continue
			}
			if !schemaPathExists(schema, segs) {
				t.Errorf("configure-coddy names the key path %q, which the schema does not declare", code[loc[0]:loc[1]])
			}
		}
	}
}

// schemaPathExists walks a dotted config path through the schema: a property
// name, an array index (N or a number) into items, or a free-form map key.
func schemaPathExists(node map[string]interface{}, segs []string) bool {
	for _, seg := range segs {
		if _, err := strconv.Atoi(seg); err == nil || seg == "N" {
			items, ok := node["items"].(map[string]interface{})
			if !ok {
				return false
			}
			node = items
			continue
		}
		props, _ := node["properties"].(map[string]interface{})
		if next, ok := props[seg].(map[string]interface{}); ok {
			node = next
			continue
		}
		if extra, ok := node["additionalProperties"].(map[string]interface{}); ok {
			node = extra
			continue
		}
		return false
	}
	return true
}
