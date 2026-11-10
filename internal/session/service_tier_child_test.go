package session_test

import (
	"context"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"testing"
)

func TestChildResumeKeepsExplicitParentTier(t *testing.T) {
	m, store, root := newSubagentTestManager(t)
	parent := newParent(t, m, root)
	parent.SetSelectedServiceTier("priority")
	spec := session.SubagentSpec{ID: session.NewSessionID(), ParentSessionID: parent.ID, Name: "reviewer", CWD: root, SelectedModelID: "p2/gpt-4o-mini", SelectedServiceTier: parent.GetSelectedServiceTier()}
	child, err := m.CreateSubagentSession(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if child.GetSelectedServiceTier() != "priority" {
		t.Fatal("spawn lost explicit tier")
	}
	if err = store.Save(child); err != nil {
		t.Fatal(err)
	}
	m.RetireSubagentSession(child.ID)
	parent.SetSelectedServiceTier("default")
	spec.Resume = true
	spec.SelectedServiceTier = parent.GetSelectedServiceTier()
	child, err = m.CreateSubagentSession(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if child.GetSelectedServiceTier() != "default" {
		t.Fatal("resume restored stale tier")
	}
	m.RetireSubagentSession(child.ID)
	parent.SetSelectedServiceTier("")
	spec.SelectedServiceTier = ""
	child, err = m.CreateSubagentSession(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if child.GetSelectedServiceTier() != "" {
		t.Fatal("resume pinned model default")
	}
}
