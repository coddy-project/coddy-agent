package agent

// Godog harness for features/prompt_cache_pictures.feature: a real turn whose
// scripted model takes one screenshot after another with the helper MCP
// server of bdd_mcp_image_test.go, and the requests the provider was sent.

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

type pictureCacheState struct {
	mcpImageFeatureState
	shots int
}

func (s *pictureCacheState) session() error {
	if err := s.server(); err != nil {
		return err
	}
	s.multimodal = true
	return nil
}

// takeScreenshots runs one turn of n screenshot calls and an answer. The
// calls differ in their arguments, as a browsing model's do, so the loop
// guard against a repeated identical call stays out of the way.
func (s *pictureCacheState) takeScreenshots(n int) error {
	s.shots = n
	a := s.agent()
	s.st.AddSessionMCPClient(s.client)
	for i := 1; i <= n; i++ {
		s.provider.steps = append(s.provider.steps, evStep{calls: []llm.ToolCall{{
			ID: fmt.Sprintf("shot%02d", i), Name: "screens__capture", InputJSON: fmt.Sprintf(`{"step":%d}`, i),
		}}})
	}
	s.provider.steps = append(s.provider.steps, evStep{text: "done"})
	_, err := a.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "browse"}})
	return err
}

// requests are the requests of the turn without the turn context block the
// send boundary appends to each: what the provider caches by.
func (s *pictureCacheState) requests() ([][]llm.Message, error) {
	seen := s.provider.streamSeen
	if len(seen) != s.shots+1 {
		return nil, fmt.Errorf("the provider was sent %d requests, want %d", len(seen), s.shots+1)
	}
	out := make([][]llm.Message, 0, len(seen))
	for _, req := range seen {
		if n := len(req); n > 0 && req[n-1].Role == llm.RoleUser && strings.Contains(req[n-1].Content, turnContextOpenTag) {
			req = req[:n-1]
		}
		out = append(out, req)
	}
	return out, nil
}

func (s *pictureCacheState) newestAndAtMostTwenty() error {
	reqs, err := s.requests()
	if err != nil {
		return err
	}
	for k := 1; k < len(reqs); k++ {
		pictures, newest := 0, false
		for _, m := range reqs[k] {
			pictures += len(m.ImageParts)
			for _, p := range m.ImageParts {
				newest = newest || p.Name == "screens-capture-1.png" && strings.Contains(m.Content, fmt.Sprintf("(from call shot%02d)", k))
			}
		}
		if pictures > toolImagesMaxCount || !newest {
			return fmt.Errorf("the request after %d screenshots carries %d pictures (the newest: %v)", k, pictures, newest)
		}
	}
	return nil
}

func (s *pictureCacheState) beginsAsTheOneBefore(a, b int) error {
	reqs, err := s.requests()
	if err != nil {
		return err
	}
	var changed []int
	for k := 1; k < len(reqs); k++ {
		prev, cur := reqs[k-1], reqs[k]
		if len(cur) < len(prev) || !reflect.DeepEqual(cur[:len(prev)], prev) {
			changed = append(changed, k)
		}
	}
	if want := []int{a, b}; !reflect.DeepEqual(changed, want) {
		return fmt.Errorf("a request changed what the one before it began with after screenshots %v, want only %v", changed, want)
	}
	return nil
}

func TestPromptCachePicturesFeature(t *testing.T) {
	s := &pictureCacheState{}
	suite := godog.TestSuite{
		Name: "prompt-cache-pictures",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) { return ctx, s.reset() })
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				s.close()
				return ctx, nil
			})
			sc.Step(`^an agent session whose model reads images and an MCP server that returns a screenshot$`, s.session)
			sc.Step(`^the model takes (\d+) screenshots in one turn, then answers$`, s.takeScreenshots)
			sc.Step(`^every request after a screenshot carries the newest one and at most 20$`, s.newestAndAtMostTwenty)
			sc.Step(`^a request begins as the one before it, except after the (\d+)(?:st|nd|rd|th) and the (\d+)(?:st|nd|rd|th) screenshot$`, s.beginsAsTheOneBefore)
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/prompt_cache_pictures.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("prompt cache pictures feature failed")
	}
}
