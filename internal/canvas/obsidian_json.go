package canvas

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

// Obsidian saves a .canvas file with a stringify of its own (Xb and Yb of its
// app bundle, read from version 1.14.4), and a file Coddy writes follows it to
// the byte, so neither side's save shows in a diff as the other's formatting:
//
//   - a value that is not an object (a string, a number, a boolean) is
//     JSON.stringify'd; null counts as an object, as typeof says in
//     JavaScript;
//   - an array whose elements are all such values, and an object whose
//     values all are, go on one line as JSON.stringify writes them;
//   - any other array or object is written one element or key per line,
//     each line indented by one more tab, a comma after every element but
//     the last;
//   - the lines are joined with "\n", with no newline after the last.

// obsidianStringify writes a JSON value the way Obsidian's canvas stringify
// does.
func obsidianStringify(raw json.RawMessage) ([]byte, error) {
	lines, err := obsidianLines(raw)
	if err != nil {
		return nil, err
	}
	return []byte(strings.Join(lines, "\n")), nil
}

func obsidianLines(raw json.RawMessage) ([]string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, errors.New("empty JSON value")
	}
	switch raw[0] {
	case '{':
		var o object
		if err := json.Unmarshal(raw, &o); err != nil {
			return nil, err
		}
		if allLeaves(o.values()) {
			return []string{flatObject(o)}, nil
		}
		out := []string{"{"}
		for i, m := range o {
			h, err := obsidianLines(m.Value)
			if err != nil {
				return nil, err
			}
			k, _ := encodeJSON(m.Key)
			h[0] = string(k) + ":" + h[0]
			out = appendIndented(out, h, i < len(o)-1)
		}
		return append(out, "}"), nil
	case '[':
		var list []json.RawMessage
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, err
		}
		if allLeaves(list) {
			parts := make([]string, len(list))
			for i, v := range list {
				parts[i] = leaf(v)
			}
			return []string{"[" + strings.Join(parts, ",") + "]"}, nil
		}
		out := []string{"["}
		for i, v := range list {
			h, err := obsidianLines(v)
			if err != nil {
				return nil, err
			}
			out = appendIndented(out, h, i < len(list)-1)
		}
		return append(out, "]"), nil
	default:
		return []string{leaf(raw)}, nil
	}
}

func appendIndented(out, lines []string, comma bool) []string {
	for j, l := range lines {
		l = "\t" + l
		if j == len(lines)-1 && comma {
			l += ","
		}
		out = append(out, l)
	}
	return out
}

// isLeaf is JavaScript's typeof v !== "object": a string, a number or a
// boolean, and never null.
func isLeaf(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] != '{' && raw[0] != '[' && !bytes.Equal(raw, []byte("null"))
}

func allLeaves(values []json.RawMessage) bool {
	for _, v := range values {
		if !isLeaf(v) {
			return false
		}
	}
	return true
}

func (o object) values() []json.RawMessage {
	out := make([]json.RawMessage, len(o))
	for i, m := range o {
		out[i] = m.Value
	}
	return out
}

// leaf writes a value that is not an object or an array as JSON.stringify
// does: a string decoded and escaped again (so "<" written by another
// tool becomes "<"), anything else as it is.
func leaf(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			if enc, err := encodeJSON(s); err == nil {
				return string(enc)
			}
		}
	}
	return string(raw)
}

func flatObject(o object) string {
	var b strings.Builder
	b.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := encodeJSON(m.Key)
		b.Write(k)
		b.WriteByte(':')
		b.WriteString(leaf(m.Value))
	}
	b.WriteByte('}')
	return b.String()
}

// geometryKeys are what Obsidian's node getData writes after the other keys
// of a node, its colour after them.
var geometryKeys = []string{"x", "y", "width", "height"}

// groupOwnKeys are the fields a group node's getData writes after the
// geometry and the colour: its setData takes them out of the node's other
// keys (a text, file or link node keeps its text, file, subpath or url among
// them).
var groupOwnKeys = []string{"label", "background"}

// obsidianOrder puts the keys of a node Coddy wrote in the order Obsidian's
// getData gives a node it changed (read from version 1.14.4): every other key
// in its order, then x, y, width and height, then color, then a group's label
// and background. A node Coddy did not touch keeps its order, as Obsidian
// keeps one it did not change.
func (o *object) obsidianOrder() {
	var own []string
	if o.str("type") == NodeGroup {
		own = groupOwnKeys
	}
	in := func(k string, list []string) bool {
		for _, x := range list {
			if k == x {
				return true
			}
		}
		return false
	}
	var out object
	for _, m := range *o {
		if m.Key != "color" && !in(m.Key, geometryKeys) && !in(m.Key, own) {
			out = append(out, m)
		}
	}
	for _, k := range append(append(append([]string{}, geometryKeys...), "color"), own...) {
		if v, ok := o.get(k); ok {
			out = append(out, member{Key: k, Value: v})
		}
	}
	*o = out
}
