package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

func TestLoadSkillToolReturnsBody(t *testing.T) {
	env := &tooling.Env{
		LoadSkillBody: func(name string) (string, []string, bool) {
			if name == "code-review" {
				return "# Code review\nDo the thing.", []string{"code-review", "deploy"}, true
			}
			return "", []string{"code-review", "deploy"}, false
		},
	}
	tool := LoadSkillTool()
	if tool.Definition.Name != "load_skill" {
		t.Fatalf("name = %q, want load_skill", tool.Definition.Name)
	}
	out, err := tool.Execute(context.Background(), `{"name":"code-review"}`, env)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "Do the thing.") {
		t.Fatalf("skill body not returned: %q", out)
	}
}

func TestLoadSkillToolAcceptsLeadingSlash(t *testing.T) {
	env := &tooling.Env{
		LoadSkillBody: func(name string) (string, []string, bool) {
			return "body", []string{name}, name == "review"
		},
	}
	if _, err := LoadSkillTool().Execute(context.Background(), `{"name":"/review"}`, env); err != nil {
		t.Fatalf("leading slash should be tolerated: %v", err)
	}
}

func TestLoadSkillToolUnknownListsAvailable(t *testing.T) {
	env := &tooling.Env{
		LoadSkillBody: func(string) (string, []string, bool) {
			return "", []string{"code-review", "deploy"}, false
		},
	}
	_, err := LoadSkillTool().Execute(context.Background(), `{"name":"nope"}`, env)
	if err == nil {
		t.Fatal("expected error for unknown skill")
	}
	if !strings.Contains(err.Error(), "code-review") {
		t.Fatalf("error should list available skills: %v", err)
	}
}

func TestLoadSkillToolNoCallbackFailsGracefully(t *testing.T) {
	if _, err := LoadSkillTool().Execute(context.Background(), `{"name":"x"}`, &tooling.Env{}); err == nil {
		t.Fatal("expected error when LoadSkillBody is nil (auto-discovery off)")
	}
}

func TestRegistryOffersLoadSkillPerAutoDiscovery(t *testing.T) {
	on := NewRegistryFor(&config.Config{Skills: config.Skills{}}) // AutoDiscovery nil => default on
	if _, ok := on.Get("load_skill"); !ok {
		t.Fatal("load_skill should be registered when auto-discovery is enabled (default)")
	}
	f := false
	off := NewRegistryFor(&config.Config{Skills: config.Skills{AutoDiscovery: &f}})
	if _, ok := off.Get("load_skill"); ok {
		t.Fatal("load_skill must not be registered when auto-discovery is disabled")
	}
}
