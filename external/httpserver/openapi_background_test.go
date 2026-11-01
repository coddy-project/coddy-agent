//go:build http

package httpserver

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
)

// openAPIBackgroundSchema returns the properties map of a 200 response schema
// on the given route and method, or fails.
func openAPIBackgroundSchema(t *testing.T, route, method string) map[string]interface{} {
	t.Helper()
	paths := openAPISpec()["paths"].(map[string]interface{})
	routeSpec, ok := paths[route].(map[string]interface{})
	if !ok {
		t.Fatalf("openapi has no %s", route)
	}
	op, ok := routeSpec[method].(map[string]interface{})
	if !ok {
		t.Fatalf("openapi %s has no %s operation", route, method)
	}
	responses := op["responses"].(map[string]interface{})
	content := responses["200"].(map[string]interface{})["content"].(map[string]interface{})
	schema := content["application/json"].(map[string]interface{})["schema"].(map[string]interface{})
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("%s %s 200 schema has no properties: %#v", method, route, schema)
	}
	return props
}

func backgroundRowKeys(t *testing.T) map[string]bool {
	t.Helper()
	finished := time.Now()
	lastOut := finished
	exit := 0
	row := newBackgroundTaskRow(bgtask.Snapshot{
		ID:              "bg_1",
		SessionID:       "sess_x",
		Kind:            bgtask.KindCommand,
		Label:           "cmd",
		Command:         "echo hi",
		CWD:             "/tmp",
		ToolCallID:      "tc_1",
		Status:          bgtask.StatusSucceeded,
		ExitCode:        &exit,
		Error:           "boom",
		StartedAt:       finished.Add(-time.Minute),
		FinishedAt:      &finished,
		ExpectedSeconds: 10,
		TimeoutSeconds:  30,
		OutputBytes:     42,
		OutputTruncated: true,
		NotifyOnFinish:  true,
		WokeAgent:       true,
		Agent: &bgtask.AgentInfo{
			Name:            "general",
			SessionID:       "sess_child",
			System:          true,
			Model:           "openai/gpt-4o",
			Reasoning:       "high",
			InputTokens:     1,
			OutputTokens:    2,
			UsageIncomplete: true,
			Worktree:        "/tmp/wt",
			Branch:          "feat/x",
		},
		URL:          "http://127.0.0.1:1",
		PID:          1234,
		LastOutputAt: &lastOut,
	}, finished)
	row.PendingPermission = &detachedPermissionDTO{AskedAt: finished}

	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	var emitted map[string]interface{}
	if err := json.Unmarshal(raw, &emitted); err != nil {
		t.Fatal(err)
	}
	keys := make(map[string]bool, len(emitted))
	for k := range emitted {
		keys[k] = true
	}
	return keys
}

// The background task endpoints answer with an envelope (object, sessionId,
// running/data for the list; object, sessionId, task/output for a single row)
// and every key a task row serialises must be declared, so the served spec
// matches the live handlers.
func TestOpenAPIBackgroundTasksShape(t *testing.T) {
	listProps := openAPIBackgroundSchema(t, "/coddy/sessions/{id}/background-tasks", "get")
	for _, name := range []string{"object", "sessionId", "running", "data"} {
		if _, ok := listProps[name]; !ok {
			t.Errorf("list schema misses %q", name)
		}
	}
	if _, ok := listProps["running"].(map[string]interface{}); ok {
		if got := listProps["running"].(map[string]interface{})["type"]; got != "integer" {
			t.Errorf("running type = %v, want integer", got)
		}
	}

	rowKeys := backgroundRowKeys(t)

	data, ok := listProps["data"].(map[string]interface{})
	if !ok {
		t.Fatal("list schema has no data")
	}
	items, ok := data["items"].(map[string]interface{})
	if !ok {
		t.Fatal("data schema has no items")
	}
	listRowProps, ok := items["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("data items schema has no properties")
	}
	for k := range rowKeys {
		if _, ok := listRowProps[k]; !ok {
			t.Errorf("list row schema misses %q emitted by taskResponse", k)
		}
	}

	for _, route := range []struct {
		path   string
		method string
	}{
		{"/coddy/sessions/{id}/background-tasks/{task_id}", "get"},
		{"/coddy/sessions/{id}/background-tasks/{task_id}/stop", "post"},
	} {
		props := openAPIBackgroundSchema(t, route.path, route.method)
		for _, name := range []string{"object", "sessionId", "task", "output"} {
			if _, ok := props[name]; !ok {
				t.Errorf("%s schema misses %q", route.path, name)
			}
		}
		taskProps, ok := props["task"].(map[string]interface{})["properties"].(map[string]interface{})
		if !ok {
			t.Fatalf("%s task schema has no properties", route.path)
		}
		for k := range rowKeys {
			if _, ok := taskProps[k]; !ok {
				t.Errorf("%s task schema misses %q emitted by taskResponse", route.path, k)
			}
		}
	}
}
