package session

import (
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// The confined mention scope carries the session's additionalDirectories as
// workspace roots, so a restricted prompt of that session may mention a file
// under one of them - the same widening the tool-call confinement applies.
func TestConfinedMentionScopeCarriesAdditionalDirectories(t *testing.T) {
	extra := filepath.Join(t.TempDir(), "extra")
	mgr := NewManager(&config.Config{}, nil, nil, slog.Default(), t.TempDir(), nil)
	res, err := mgr.HandleSessionNew(t.Context(), acp.SessionNewParams{
		CWD:                   t.TempDir(),
		AdditionalDirectories: []string{extra},
	})
	if err != nil {
		t.Fatal(err)
	}
	st := mgr.SessionByID(res.SessionID)
	st.SetTurnRestriction(&TurnRestriction{ConfineToWorkspace: true})
	defer st.SetTurnRestriction(nil)
	scope := mgr.mentionScope(st, false, false)
	if !scope.Confined {
		t.Fatal("the turn is confined but the scope says otherwise")
	}
	if len(scope.WorkspaceRoots) != 1 || scope.WorkspaceRoots[0] != extra {
		t.Fatalf("scope workspace roots = %#v, want the session's additional directory %q", scope.WorkspaceRoots, extra)
	}
}
