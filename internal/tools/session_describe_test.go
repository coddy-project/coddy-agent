package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// filingEnv is a session_describe environment backed by a map instead of a
// session: the tool's own contract - what it refuses, what it reports - does not
// need a bundle on disk to be pinned down.
type filingEnv struct {
	filing  tooling.SessionFiling
	updates []tooling.SessionFilingUpdate
	err     error
}

func (f *filingEnv) env() *tooling.Env {
	return &tooling.Env{
		FileSession: func(upd tooling.SessionFilingUpdate) (tooling.SessionFilingResult, error) {
			f.updates = append(f.updates, upd)
			if f.err != nil {
				return tooling.SessionFilingResult{Filing: f.filing}, f.err
			}
			changed := make([]string, 0, 2)
			if upd.Title != nil && *upd.Title != f.filing.Title {
				f.filing.Title = *upd.Title
				changed = append(changed, "title")
			}
			if upd.Tags != nil {
				f.filing.Tags = *upd.Tags
				changed = append(changed, "tags")
			}
			return tooling.SessionFilingResult{Filing: f.filing, Changed: changed}, nil
		},
	}
}

func runSessionDescribe(t *testing.T, env *tooling.Env, args string) (string, error) {
	t.Helper()
	return SessionDescribeTool().Execute(context.Background(), args, env)
}

func TestSessionDescribeRefusesWhenNoSessionBacksTheRun(t *testing.T) {
	// A scheduled run or a bare tool test has no session to file; saying so is
	// better than reporting a filing nobody stored.
	_, err := runSessionDescribe(t, &tooling.Env{}, `{"title":"x"}`)
	if err == nil || !strings.Contains(err.Error(), ToolSessionDescribe) {
		t.Fatalf("err = %v, want one naming the tool", err)
	}
}

func TestSessionDescribeRefusesReplacingAndEditingTheSameTags(t *testing.T) {
	f := &filingEnv{filing: tooling.SessionFiling{Title: "Old", Tags: []string{"api"}}}
	_, err := runSessionDescribe(t, f.env(), `{"tags":["a"],"add_tags":["b"]}`)
	if err == nil {
		t.Fatal("a call carrying both ways of writing the tags was accepted")
	}
	if len(f.updates) != 0 {
		t.Fatalf("the refused call still wrote %v", f.updates)
	}
}

func TestSessionDescribeReportsInvalidArguments(t *testing.T) {
	f := &filingEnv{}
	if _, err := runSessionDescribe(t, f.env(), `{"title":`); err == nil {
		t.Fatal("malformed arguments were accepted")
	}
}

func TestSessionDescribeReadsWithoutWritingWhenCalledEmpty(t *testing.T) {
	// A read is an update that names nothing: one hook, so the tool never holds
	// a filing it read a moment ago and cannot write one another surface moved.
	f := &filingEnv{filing: tooling.SessionFiling{Title: "Rewrite the store", Tags: []string{"api"}}}
	for _, args := range []string{"", "{}", "  "} {
		out, err := runSessionDescribe(t, f.env(), args)
		if err != nil {
			t.Fatalf("%q: %v", args, err)
		}
		var got struct {
			Title   string   `json:"title"`
			Tags    []string `json:"tags"`
			Changed []string `json:"changed"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("%q: %v", args, err)
		}
		if got.Title != "Rewrite the store" || len(got.Tags) != 1 || len(got.Changed) != 0 {
			t.Fatalf("%q reported %+v", args, got)
		}
	}
}

func TestSessionDescribeReportsNoTagsAsAnEmptyList(t *testing.T) {
	// The model reads this answer back as JSON; a null where a list belongs is
	// one more thing for it to guess about.
	f := &filingEnv{filing: tooling.SessionFiling{Title: "Untagged"}}
	out, err := runSessionDescribe(t, f.env(), "{}")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"tags":[]`) {
		t.Fatalf("got %s", out)
	}
}

func TestSessionDescribeReportsWhatMovedNotWhatWasAsked(t *testing.T) {
	f := &filingEnv{filing: tooling.SessionFiling{Title: "Same title", Tags: []string{"api"}}}
	out, err := runSessionDescribe(t, f.env(), `{"title":"Same title"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"changed":[]`) {
		t.Fatalf("setting the title it already had was reported as a change: %s", out)
	}
	out, err = runSessionDescribe(t, f.env(), `{"title":"Another title"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"changed":["title"]`) {
		t.Fatalf("got %s", out)
	}
}

func TestSessionDescribeSurfacesTheRuntimeRefusal(t *testing.T) {
	f := &filingEnv{filing: tooling.SessionFiling{Title: "Old"}, err: errTooLongForTest{}}
	if _, err := runSessionDescribe(t, f.env(), `{"title":"x"}`); err == nil {
		t.Fatal("the runtime refusal did not reach the model")
	}
}

type errTooLongForTest struct{}

func (errTooLongForTest) Error() string { return "the title is too long" }
