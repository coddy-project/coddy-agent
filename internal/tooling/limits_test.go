package tooling

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func linesOf(n int) string {
	rows := make([]string, n)
	for i := range rows {
		rows[i] = fmt.Sprintf("line %d", i+1)
	}
	return strings.Join(rows, "\n")
}

func TestTruncateLines(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		in           string
		maxLines     int
		wantTruncate bool
		wantFirst    string
		wantLines    int // expected line count of result (incl. marker) when truncated
	}{
		{name: "unlimited zero", in: linesOf(50), maxLines: 0, wantTruncate: false},
		{name: "negative unlimited", in: linesOf(50), maxLines: -1, wantTruncate: false},
		{name: "shorter than limit", in: linesOf(5), maxLines: 10, wantTruncate: false},
		{name: "exactly at limit", in: linesOf(10), maxLines: 10, wantTruncate: false},
		{name: "truncates", in: linesOf(100), maxLines: 10, wantTruncate: true, wantFirst: "line 1", wantLines: 11},
		{name: "empty", in: "", maxLines: 5, wantTruncate: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := TruncateLines(tc.in, tc.maxLines, "tools.output_limits.read; use offset/limit")
			if !tc.wantTruncate {
				if got != tc.in {
					t.Fatalf("expected unchanged output, got %q", got)
				}
				return
			}
			if !strings.Contains(got, "[output truncated:") {
				t.Fatalf("missing truncation marker: %q", got)
			}
			if !strings.Contains(got, "tools.output_limits.read") {
				t.Fatalf("marker missing hint: %q", got)
			}
			rows := strings.Split(got, "\n")
			if rows[0] != tc.wantFirst {
				t.Fatalf("first line = %q, want %q", rows[0], tc.wantFirst)
			}
			if len(rows) != tc.wantLines {
				t.Fatalf("result lines = %d, want %d", len(rows), tc.wantLines)
			}
		})
	}
}

func TestTruncateLinesTrailingNewlineNotCountedTwice(t *testing.T) {
	t.Parallel()
	// 10 real lines plus a trailing newline must not be treated as 11 lines.
	in := linesOf(10) + "\n"
	if got := TruncateLines(in, 10, "h"); got != in {
		t.Fatalf("output with trailing newline was truncated: %q", got)
	}
}

// stubTool records the args it saw and returns a fixed multi-line output.
func stubTool(name string, lines int) *Tool {
	return &Tool{
		Definition: llm.ToolDefinition{Name: name},
		Execute: func(context.Context, string, *Env) (string, error) {
			return linesOf(lines), nil
		},
	}
}

func TestRegistryExecuteAppliesOutputLimit(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.Register(stubTool("read", 500))
	r.Register(stubTool("mystery", 500))

	env := &Env{OutputLineLimits: map[string]int{"read": 3, "": 7}}

	out, err := r.Execute(context.Background(), "read", "{}", env)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(out, "\n") + 1; got != 4 { // 3 lines + marker
		t.Fatalf("read result lines = %d, want 4", got)
	}

	// Unlisted tool falls back to the default ("") limit.
	out, err = r.Execute(context.Background(), "mystery", "{}", env)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(out, "\n") + 1; got != 8 { // 7 lines + marker
		t.Fatalf("mystery result lines = %d, want 8", got)
	}
}

func TestRegistryExecuteNilLimitsNoOp(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.Register(stubTool("read", 40))
	out, err := r.Execute(context.Background(), "read", "{}", &Env{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "truncated") {
		t.Fatalf("nil limits should not truncate: %q", out)
	}
	if got := strings.Count(out, "\n") + 1; got != 40 {
		t.Fatalf("lines = %d, want 40", got)
	}
}
