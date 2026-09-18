package session_test

// Godog harness for features/mentions.feature: a real session.Manager with a
// file store and a runner that records the prompt blocks it is handed, which
// is exactly what the agent turns into the user message. Every scenario goes
// through HandleSessionPromptWithSender, the path the console, the web UI,
// ACP editors and the Telegram gateway share.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/mention"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type mentionsFeatureState struct {
	tmp       []string
	workspace string
	outside   string
	home      string
	oldHome   string
	homeSet   bool
	store     *session.FileStore
	mgr       *session.Manager
	sessionID string
	earlierID string
	pages     map[string]string
	seen      []acp.ContentBlock
	checked   []session.CheckedMention
}

func (s *mentionsFeatureState) tempDir() (string, error) {
	d, err := os.MkdirTemp("", "coddy-bdd-mentions-*")
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(d); err == nil {
		d = real
	}
	s.tmp = append(s.tmp, d)
	return d, nil
}

func (s *mentionsFeatureState) close() {
	for _, d := range s.tmp {
		_ = os.RemoveAll(d)
	}
	if s.homeSet {
		_ = os.Setenv("HOME", s.oldHome)
	}
	mention.RegisterURLFetcher(nil)
	*s = mentionsFeatureState{}
}

func (s *mentionsFeatureState) workspaceWithFiles(table *godog.Table) error {
	ws, err := s.tempDir()
	if err != nil {
		return err
	}
	s.workspace = ws
	for _, row := range table.Rows[1:] {
		p := filepath.Join(ws, filepath.FromSlash(row.Cells[0].Value))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(row.Cells[1].Value+"\n"), 0o644); err != nil {
			return err
		}
	}
	root, err := s.tempDir()
	if err != nil {
		return err
	}
	s.store = &session.FileStore{Root: root}
	cfg := &config.Config{}
	cfg.Agent.ApplyDefaults()
	runner := func(_ context.Context, _ *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		s.seen = append([]acp.ContentBlock(nil), prompt...)
		return string(acp.StopReasonEndTurn), nil
	}
	s.mgr = session.NewManager(cfg, noopSender{}, runner, slog.New(slog.DiscardHandler), ws, s.store)
	res, err := s.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: ws})
	if err != nil {
		return err
	}
	s.sessionID = res.SessionID
	return nil
}

func (s *mentionsFeatureState) fileOutsideHolding(body string) error {
	dir, err := s.tempDir()
	if err != nil {
		return err
	}
	s.outside = filepath.Join(dir, "outside.txt")
	return os.WriteFile(s.outside, []byte(body+"\n"), 0o644)
}

func (s *mentionsFeatureState) fileInHomeHolding(name, body string) error {
	home, err := s.tempDir()
	if err != nil {
		return err
	}
	s.home = home
	s.oldHome, s.homeSet = os.Getenv("HOME"), true
	if err := os.Setenv("HOME", home); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(home, name), []byte(body+"\n"), 0o644)
}

func (s *mentionsFeatureState) earlierSession(userText, answer string) error {
	res, err := s.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.workspace})
	if err != nil {
		return err
	}
	st := s.mgr.SessionByID(res.SessionID)
	if st == nil {
		return fmt.Errorf("earlier session %s is not live", res.SessionID)
	}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: userText})
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: answer})
	if err := s.store.Save(st); err != nil {
		return err
	}
	s.earlierID = res.SessionID
	return nil
}

func (s *mentionsFeatureState) webPage(url, body string) error {
	if s.pages == nil {
		s.pages = map[string]string{}
	}
	s.pages[url] = body
	mention.RegisterURLFetcher(func(_ context.Context, u string) (string, error) {
		if text, ok := s.pages[u]; ok {
			return text, nil
		}
		return "", fmt.Errorf("no such page %s", u)
	})
	return nil
}

func (s *mentionsFeatureState) userSends(text string) error {
	text = strings.ReplaceAll(text, "<outside file>", s.outside)
	text = strings.ReplaceAll(text, "<earlier session>", s.earlierID)
	s.seen = nil
	_, err := s.mgr.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{
		SessionID: s.sessionID,
		Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: text}},
	}, noopSender{}, nil)
	if err != nil {
		return err
	}
	if s.seen == nil {
		return fmt.Errorf("the runner never received the prompt")
	}
	return nil
}

func (s *mentionsFeatureState) attachesOnly(uri, body string) error {
	res := s.resources()
	if len(res) != 1 || res[0].URI != uri || !strings.Contains(res[0].Text, body) {
		return fmt.Errorf("want only %s holding %q, got:\n%s", uri, body, s.describe())
	}
	return nil
}

func (s *mentionsFeatureState) composerChecks(text string) error {
	s.checked = s.mgr.CheckMentions(context.Background(), session.MentionCheck{SessionID: s.sessionID, Text: text})
	if len(s.checked) == 0 {
		return fmt.Errorf("the check read no mention in %q", text)
	}
	return nil
}

func (s *mentionsFeatureState) checkMarks(typed, kind string) error {
	for _, c := range s.checked {
		if c.Typed == typed && c.Kind == kind {
			return nil
		}
	}
	return fmt.Errorf("%s is not marked as a %s: %+v", typed, kind, s.checked)
}

func (s *mentionsFeatureState) checkLeavesUnmarked(token string) error {
	for _, c := range s.checked {
		if c.Token == token {
			if c.Typed != "" || c.Kind != "" {
				return fmt.Errorf("%s is marked: %+v", token, c)
			}
			return nil
		}
	}
	return fmt.Errorf("the check did not read %s as a token: %+v", token, s.checked)
}

func (s *mentionsFeatureState) resources() []*acp.Resource {
	var out []*acp.Resource
	for _, b := range s.seen {
		if b.Type == acp.ContentTypeResource && b.Resource != nil {
			out = append(out, b.Resource)
		}
	}
	return out
}

func (s *mentionsFeatureState) describe() string {
	var b strings.Builder
	for _, r := range s.resources() {
		kind := ""
		if r.Mention != nil {
			kind = r.Mention.Kind
		}
		fmt.Fprintf(&b, "- %s [%s]: %q\n", r.URI, kind, r.Text)
	}
	if b.Len() == 0 {
		return "(no attachments)"
	}
	return b.String()
}

func (s *mentionsFeatureState) attachesHolding(uri, body string) error {
	for _, r := range s.resources() {
		if r.URI == uri && strings.Contains(r.Text, body) {
			return nil
		}
	}
	return fmt.Errorf("no attachment %q holding %q; the prompt has:\n%s", uri, body, s.describe())
}

func (s *mentionsFeatureState) attachesOutsideFile(body string) error {
	return s.attachesHolding(filepath.ToSlash(s.outside), body)
}

func (s *mentionsFeatureState) attachesFileMentionedAs(body, typed string) error {
	for _, r := range s.resources() {
		if strings.Contains(r.Text, body) && r.Mention != nil && r.Mention.Typed == typed {
			return nil
		}
	}
	return fmt.Errorf("no attachment holding %q mentioned as %q; the prompt has:\n%s", body, typed, s.describe())
}

func (s *mentionsFeatureState) attachesFolderListing(dir, a, b string) error {
	for _, r := range s.resources() {
		if r.URI == dir && r.Mention != nil && r.Mention.Kind == mention.KindDirectory {
			for _, want := range []string{a, b} {
				if !strings.Contains(r.Text, "\n"+want) {
					return fmt.Errorf("the listing of %s lacks %q:\n%s", dir, want, r.Text)
				}
			}
			return nil
		}
	}
	return fmt.Errorf("no folder attachment %q; the prompt has:\n%s", dir, s.describe())
}

func (s *mentionsFeatureState) attachesEarlierSession(a, b string) error {
	for _, r := range s.resources() {
		if r.URI == "session:"+s.earlierID {
			for _, want := range []string{a, b} {
				if !strings.Contains(r.Text, want) {
					return fmt.Errorf("the digest lacks %q:\n%s", want, r.Text)
				}
			}
			return nil
		}
	}
	return fmt.Errorf("no attachment for session %s; the prompt has:\n%s", s.earlierID, s.describe())
}

func (s *mentionsFeatureState) attachesSubagent(name string) error {
	for _, r := range s.resources() {
		if r.URI == "agent:"+name && strings.Contains(r.Text, "spawn_agent") {
			return nil
		}
	}
	return fmt.Errorf("no subagent attachment for %q; the prompt has:\n%s", name, s.describe())
}

func (s *mentionsFeatureState) attachesDocumentation(ref, body string) error {
	for _, r := range s.resources() {
		if r.URI == "coddy:"+ref && r.Mention != nil && r.Mention.Kind == mention.KindDoc && strings.Contains(r.Text, body) {
			return nil
		}
	}
	return fmt.Errorf("no documentation attachment %q holding %q; the prompt has:\n%s", ref, body, s.describe())
}

func initializeMentionsScenario(sc *godog.ScenarioContext) {
	s := &mentionsFeatureState{}
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^a workspace with the files:$`, s.workspaceWithFiles)
	sc.Step(`^a file outside the workspace holding "([^"]*)"$`, s.fileOutsideHolding)
	sc.Step(`^a file "([^"]*)" in the home folder holding "([^"]*)"$`, s.fileInHomeHolding)
	sc.Step(`^an earlier session where the user wrote "([^"]*)" and the assistant answered "([^"]*)"$`, s.earlierSession)
	sc.Step(`^a web page "([^"]*)" reading "([^"]*)"$`, s.webPage)
	sc.Step(`^the user sends "(.*)"$`, s.userSends)
	sc.Step(`^the prompt attaches the outside file holding "([^"]*)"$`, s.attachesOutsideFile)
	sc.Step(`^the prompt attaches a file holding "([^"]*)" mentioned as "([^"]*)"$`, s.attachesFileMentionedAs)
	sc.Step(`^the prompt attaches "([^"]*)" holding "([^"]*)"$`, s.attachesHolding)
	sc.Step(`^the prompt attaches only "([^"]*)" holding "([^"]*)"$`, s.attachesOnly)
	sc.Step(`^the composer checks the draft "(.*)"$`, s.composerChecks)
	sc.Step(`^the check marks "([^"]*)" as a mention of a (\w+)$`, s.checkMarks)
	sc.Step(`^the check leaves "([^"]*)" unmarked$`, s.checkLeavesUnmarked)
	sc.Step(`^the prompt attaches the folder "([^"]*)" listing "([^"]*)" and "([^"]*)"$`, s.attachesFolderListing)
	sc.Step(`^the prompt attaches the earlier session holding "([^"]*)" and "([^"]*)"$`, s.attachesEarlierSession)
	sc.Step(`^the prompt attaches the subagent "([^"]*)" asking to hand the work to spawn_agent$`, s.attachesSubagent)
	sc.Step(`^the prompt attaches the documentation "([^"]*)" holding "([^"]*)"$`, s.attachesDocumentation)
}

func TestMentionsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "mentions",
		ScenarioInitializer: initializeMentionsScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/mentions.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("mentions feature suite failed")
	}
}
