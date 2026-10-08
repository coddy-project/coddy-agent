package config

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// saveMarks walks a settings form schema and returns every path carrying
// UISchemaSaveKey with its value. Array items are not walked: a mark there
// would have no single path in the document.
func saveMarks(node map[string]interface{}, path string, out map[string]string) {
	if how, ok := node[UISchemaSaveKey].(string); ok {
		out[path] = how
	}
	props, _ := node["properties"].(map[string]interface{})
	for k, v := range props {
		sub, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		next := k
		if path != "" {
			next = path + "." + k
		}
		saveMarks(sub, next, out)
	}
}

func saveMarkList(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k+"="+m[k])
	}
	sort.Strings(out)
	return out
}

// TestUISchemaMarksTheChangesThatWaitForSave holds the list of the agent's
// settings whose change the form keeps until Save: a subsystem of coddy serve
// turned on or off, the address the preview server binds, and a provider or a
// model taken out. Everything else saves on its own.
func TestUISchemaMarksTheChangesThatWaitForSave(t *testing.T) {
	got := map[string]string{}
	saveMarks(UISchemaMap(), "", got)
	want := map[string]string{
		"providers":                 SaveConfirmRemoval,
		"models":                    SaveConfirmRemoval,
		"gateways.telegram.enable":  SaveConfirm,
		"gateways.pachca.enable":    SaveConfirm,
		"scheduler.enable":          SaveConfirm,
		"tools.preview_server.host": SaveConfirm,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("save marks:\n got  %s\n want %s", strings.Join(saveMarkList(got), " "), strings.Join(saveMarkList(want), " "))
	}
}

// TestRelayUISchemaMarksTheChangesThatWaitForSave holds the relay's list: the
// address it listens on, the credentials and the certificate that decide
// whether its clients - this page included - and its nodes still get in, and
// its links to other machines.
func TestRelayUISchemaMarksTheChangesThatWaitForSave(t *testing.T) {
	got := map[string]string{}
	saveMarks(RelayUISchemaMap(), "", got)
	want := map[string]string{
		"swarm.host":           SaveConfirm,
		"swarm.port":           SaveConfirm,
		"swarm.auth_token":     SaveConfirm,
		"swarm.pairing_tokens": SaveConfirm,
		"swarm.tls":            SaveConfirm,
		"swarm.upstreams":      SaveConfirm,
		"swarm.join":           SaveConfirm,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("relay save marks:\n got  %s\n want %s", strings.Join(saveMarkList(got), " "), strings.Join(saveMarkList(want), " "))
	}
}

// TestUISchemaSaveMarksAreOnFieldsAndLists keeps a removal mark on lists only:
// on anything else the form would have no rows to tell apart.
func TestUISchemaSaveMarksAreOnFieldsAndLists(t *testing.T) {
	for name, doc := range map[string]map[string]interface{}{"agent": UISchemaMap(), "relay": RelayUISchemaMap()} {
		var walk func(node map[string]interface{}, path string)
		walk = func(node map[string]interface{}, path string) {
			if how, ok := node[UISchemaSaveKey].(string); ok {
				switch how {
				case SaveConfirm:
				case SaveConfirmRemoval:
					if node["type"] != "array" {
						t.Errorf("%s: %s is marked %q but is not a list", name, path, how)
					}
				default:
					t.Errorf("%s: %s carries an unknown save mark %q", name, path, how)
				}
			}
			props, _ := node["properties"].(map[string]interface{})
			for k, v := range props {
				if sub, ok := v.(map[string]interface{}); ok {
					walk(sub, strings.TrimPrefix(path+"."+k, "."))
				}
			}
		}
		walk(doc, "")
	}
}
