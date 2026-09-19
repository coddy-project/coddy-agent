package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestToolInputProgressEverySplit(t *testing.T) {
	for _, name := range []string{"write", "edit", "apply_patch"} {
		p := newToolInputProgress(name)
		// Surrogate pairs, newlines, quotes and multibyte UTF-8 across every boundary.
		input := `{"path":"a\"b.html","` + p.field + `":"a\n\uD83D\uDE80я\/\\z"}`
		var decoded map[string]string
		if err := json.Unmarshal([]byte(input), &decoded); err != nil {
			t.Fatal(err)
		}
		for split := 0; split <= len(input); split++ {
			p = newToolInputProgress(name)
			p.add(input[:split])
			p.add(input[split:])
			if p.path != decoded["path"] || p.tail != decoded[p.field] || p.bytes != len(decoded[p.field]) || p.lines != 2 {
				t.Fatalf("%s split %d: %+v", name, split, p)
			}
		}
	}
}

func TestToolInputProgressBoundedAndPrivate(t *testing.T) {
	p := newToolInputProgress("write")
	p.add(`{"ignored":{"content":"SECRET"},"content":"`)
	for i := 0; i < 100000; i++ {
		p.add(`line\n`)
	}
	p.add(`","path":"demo.html"}`)
	u := p.update("w1", time.Now(), true)
	data, _ := json.Marshal(u)
	if len(data) > 2000 || strings.Contains(string(data), "SECRET") || p.bytes != 500000 {
		t.Fatalf("unbounded or incorrect progress: %d bytes, size %d", p.bytes, len(data))
	}
	p = newToolInputProgress("http_request")
	p.add(`{"content":"SECRET"}`)
	if p.update("x", time.Now(), true) != nil || p.tail != "" {
		t.Fatal("non-file tool exposes arguments")
	}
}

func TestToolInputProgressThrottle(t *testing.T) {
	p := newToolInputProgress("write")
	p.add(`{"content":"hello`)
	now := time.Now()
	if p.update("w", now, false) == nil || p.update("w", now.Add(time.Millisecond), false) != nil || p.update("w", now.Add(time.Millisecond), true) == nil {
		t.Fatal("throttle must retain final flush")
	}
}
