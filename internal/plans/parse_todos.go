package plans

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// todoList accepts flexible YAML shapes for plan frontmatter todos.
type todoList []TodoItem

func (t *todoList) UnmarshalYAML(value *yaml.Node) error {
	if value == nil || value.Kind == yaml.ScalarNode && value.Tag == "!!null" {
		*t = nil
		return nil
	}
	if value.Kind != yaml.SequenceNode {
		return fmt.Errorf("todos must be a YAML list")
	}
	out := make(todoList, 0, len(value.Content))
	for _, node := range value.Content {
		item, err := decodeTodoNode(node)
		if err != nil {
			return err
		}
		if strings.TrimSpace(item.Content) != "" {
			out = append(out, item)
		}
	}
	*t = out
	return nil
}

func decodeTodoNode(node *yaml.Node) (TodoItem, error) {
	switch node.Kind {
	case yaml.ScalarNode:
		return TodoItem{Content: strings.TrimSpace(node.Value)}, nil
	case yaml.MappingNode:
		var item TodoItem
		if err := node.Decode(&item); err == nil && strings.TrimSpace(item.Content) != "" {
			return normalizeTodoItem(item), nil
		}
		var m map[string]interface{}
		if err := node.Decode(&m); err != nil {
			return TodoItem{}, fmt.Errorf("todo item: %w", err)
		}
		return todoItemFromMap(m), nil
	default:
		return TodoItem{}, fmt.Errorf("todo item must be a string or mapping")
	}
}

func todoItemFromMap(m map[string]interface{}) TodoItem {
	var item TodoItem
	for _, key := range []string{"content", "title", "description", "label", "task", "name"} {
		if s := stringField(m[key]); s != "" {
			item.Content = s
			break
		}
	}
	if s := stringField(m["status"]); s != "" {
		item.Status = s
	}
	if s := stringField(m["priority"]); s != "" {
		item.Priority = s
	}
	return normalizeTodoItem(item)
}

func stringField(v interface{}) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	default:
		return ""
	}
}

func normalizeTodoItem(item TodoItem) TodoItem {
	item.Content = strings.TrimSpace(item.Content)
	item.Status = strings.TrimSpace(item.Status)
	item.Priority = strings.TrimSpace(item.Priority)
	return item
}
