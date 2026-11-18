package models

import (
	"encoding/json"
	"testing"
)

func TestCompactImageOutputModalities(t *testing.T) {
	input := `{"openai":{"models":{"image":{"limit":{"context":1000},"modalities":{"input":["text"],"output":["text","image"]}},"text":{"limit":{"context":1000},"modalities":{"input":["image"],"output":["text"]}},"unknown":{"limit":{"context":1000},"modalities":{"input":["image"]}}}}}`
	got, err := Compact([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	var rows map[string][]int
	if err := json.Unmarshal(got, &rows); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string][]int{"image": {1000, 0, 0, 2}, "text": {1000, 0, 1, 1}, "unknown": {1000, 0, 1}} {
		actual, _ := json.Marshal(rows[id])
		expected, _ := json.Marshal(want)
		if string(actual) != string(expected) {
			t.Errorf("%s: got %s, want %s", id, actual, expected)
		}
	}
}

func TestCompactKeepsImageOutputWithoutWindow(t *testing.T) {
	input := `{"openai":{"models":{"image-only":{"limit":{"output":123},"modalities":{"output":["image"]}},"unknown":{"limit":{}},"negative":{"limit":{"context":-1},"modalities":{"output":["text"]}}}}}`
	got, err := Compact([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	var rows map[string][]int
	if err := json.Unmarshal(got, &rows); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"image-only", "negative"} {
		if len(rows[id]) != 4 || rows[id][0] != 0 || rows[id][1] != 0 {
			t.Errorf("%s = %v", id, rows[id])
		}
	}
	if _, ok := rows["unknown"]; ok {
		t.Error("empty record should not be retained")
	}
}

func TestCompactPrefersKnownWindowWithinProviderClass(t *testing.T) {
	input := `{"opencode":{"models":{"m":{"limit":{"context":1000,"output":100},"modalities":{"output":["text"]}}}},"opencode-go":{"models":{"m":{"modalities":{"output":["image"]}}}}}`
	got, err := Compact([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	var rows map[string][]int
	if err := json.Unmarshal(got, &rows); err != nil {
		t.Fatal(err)
	}
	if v := rows["m"]; len(v) != 4 || v[0] != 1000 || v[1] != 100 || v[3] != 1 {
		t.Fatalf("wrong selected record: %v", v)
	}
}
