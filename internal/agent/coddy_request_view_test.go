package agent

// One request of a coddy row is built from one record of the remote's listing
// (docs/plans/remote-model-provider-phase2.md, 3.3 and 3.8 item 2; decided by
// docs/plans/remote-model-provider-models/p2-d1-config-source.md): the
// revision a request is checked against and the level it asks for come from the
// same record, so a refresh that lands between the reads of the two cannot make
// a request that names the new revision carry a level the new record does not
// offer. A row that inherits its levels and its off switch from the listing
// has its level fall back to the record's default; a row that writes either
// key sends the level as it is, and the remote's invalid_option stands.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// listingState is a session state whose listing record a test scripts: it
// answers the two methods of capabilityListing itself and counts the reads. The
// script gives one record per read; the last one repeats. Without records the
// listing does not know the model.
type listingState struct {
	*session.State
	mu     sync.Mutex
	script []llm.ModelEntry
	reads  int
}

func newListingState(t *testing.T, script ...llm.ModelEntry) *listingState {
	t.Helper()
	return &listingState{
		State:  &session.State{ID: "sess_view", CWD: t.TempDir(), Mode: session.ModeAgent},
		script: script,
	}
}

func (s *listingState) ProviderModelEntry(_ *config.Config, _, _ string) (llm.ModelEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.script) == 0 {
		return llm.ModelEntry{}, false
	}
	i := min(s.reads, len(s.script)-1)
	s.reads++
	e := s.script[i]
	e.ReasoningLevels = append([]string(nil), e.ReasoningLevels...)
	return e, true
}

func (s *listingState) RefreshProviderModelEntry(context.Context, *config.Config, string, string, string) (*llm.ModelEntry, error) {
	return nil, errors.New("not scripted")
}

func (s *listingState) readCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

// plainState is a session state that offers no listing: it hides every method
// of the real state that is not part of SessionState.
type plainState struct{ SessionState }

// record is one listing record of the model coder.
func record(rev, def string, off bool, levels ...string) llm.ModelEntry {
	return llm.ModelEntry{ID: "coder", Revision: rev, ReasoningLevels: levels, ReasoningDefault: def, AllowReasoningOff: off}
}

// requestView runs coddyRequestView for the row of the tests on a state, with
// the session's level as EffectiveReasoning resolved it earlier.
func requestView(t *testing.T, state SessionState, row func(*config.ModelEntry), effort string) llm.ProviderInput {
	t.Helper()
	cfg := coddyConfig("https://remote.example", func(c *config.Config) {
		if row != nil {
			row(&c.Models[0])
		}
	})
	cfg.Agent.ApplyDefaults()
	ag := NewAgent(cfg, state, resumePermissionSender{}, nil)
	in := llm.ProviderInput{ReasoningEffort: "never-read"}
	ag.coddyRequestView(&in, resolved(t, cfg, coddyRow), effort)
	return in
}

func writesLevels(levels ...string) func(*config.ModelEntry) {
	return func(m *config.ModelEntry) { m.ReasoningLevels = &levels }
}

// An inherited level falls back to the default of the record the request is
// built from, and the revision is that record's.
func TestRequestViewInheritedLevelFallsBackToTheNarrowedRecordsDefault(t *testing.T) {
	for _, tc := range []struct {
		name   string
		effort string
		rec    llm.ModelEntry
		want   string
	}{
		{"high is narrowed away", "high", record("rev-2", "low", false, "low"), "low"},
		{"high is still offered", "high", record("rev-2", "low", false, "low", "high"), "high"},
		{"the record has no default", "high", record("rev-2", "", false, "low"), ""},
		{"the record has no levels at all", "high", record("rev-2", "low", false), ""},
		{"off is still offered", "off", record("rev-2", "low", true, "low", "high"), "off"},
		{"off is no longer offered", "off", record("rev-2", "low", false, "low", "high"), "low"},
		{"the level is empty and the record has no default", "", record("rev-2", "", false, "low"), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newListingState(t, tc.rec)
			in := requestView(t, st, nil, tc.effort)
			if in.ReasoningEffort != tc.want {
				t.Errorf("ReasoningEffort = %q, want %q", in.ReasoningEffort, tc.want)
			}
			if in.ExpectedRevision != "rev-2" {
				t.Errorf("ExpectedRevision = %q, want the record's rev-2", in.ExpectedRevision)
			}
		})
	}
}

// A row that writes reasoning_levels sends the session's level as it is: the
// remote answers invalid_option for a level it does not offer, and no refresh
// fixes a key the operator wrote.
func TestRequestViewExplicitLevelsKeepALevelTheRecordNoLongerOffers(t *testing.T) {
	st := newListingState(t, record("rev-2", "low", false, "low"))
	in := requestView(t, st, writesLevels("low", "high"), "high")
	if in.ReasoningEffort != "high" {
		t.Errorf("ReasoningEffort = %q, want high kept: the row writes its levels", in.ReasoningEffort)
	}
	if in.ExpectedRevision != "rev-2" {
		t.Errorf("ExpectedRevision = %q, want the record's rev-2", in.ExpectedRevision)
	}
}

// Writing allow_reasoning_off alone, true or an explicit false, also makes the
// row's choices its own: the level goes as it is.
func TestRequestViewAWrittenOffSwitchKeepsTheLevelAsItIs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		off    bool
		effort string
	}{
		{"off switched on locally, the record has none", true, "off"},
		{"off switched on locally, another level", true, "high"},
		{"an explicit false is a written key", false, "high"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newListingState(t, record("rev-2", "low", false, "low"))
			in := requestView(t, st, func(m *config.ModelEntry) { m.AllowReasoningOff = config.BoolPtr(tc.off) }, tc.effort)
			if in.ReasoningEffort != tc.effort {
				t.Errorf("ReasoningEffort = %q, want %q as it is", in.ReasoningEffort, tc.effort)
			}
			if in.ExpectedRevision != "rev-2" {
				t.Errorf("ExpectedRevision = %q, want rev-2", in.ExpectedRevision)
			}
		})
	}
}

// reasoning_default is not a level key of the request: a row that writes only
// it (and the other keys the listing may fill) still inherits its levels.
func TestRequestViewAWrittenDefaultAloneStillInheritsTheLevels(t *testing.T) {
	st := newListingState(t, record("rev-2", "low", false, "low"))
	in := requestView(t, st, func(m *config.ModelEntry) { m.ReasoningDefault = "high" }, "high")
	if in.ReasoningEffort != "low" {
		t.Errorf("ReasoningEffort = %q, want the record's default low: the levels are inherited", in.ReasoningEffort)
	}
}

// No record (the listing never answered, or dropped the alias) and no listing
// at all leave the level as it is and name no revision.
func TestRequestViewWithoutARecordSendsTheLevelAndNoRevision(t *testing.T) {
	for name, state := range map[string]func(*testing.T) SessionState{
		"the listing does not know the model": func(t *testing.T) SessionState { return newListingState(t) },
		"the state offers no listing": func(t *testing.T) SessionState {
			return plainState{&session.State{ID: "sess_plain", CWD: t.TempDir(), Mode: session.ModeAgent}}
		},
		"a state no manager built": func(t *testing.T) SessionState {
			return &session.State{ID: "sess_bare", CWD: t.TempDir(), Mode: session.ModeAgent}
		},
	} {
		t.Run(name, func(t *testing.T) {
			in := requestView(t, state(t), nil, "high")
			if in.ReasoningEffort != "high" || in.ExpectedRevision != "" {
				t.Errorf("input = effort %q revision %q, want high and none", in.ReasoningEffort, in.ExpectedRevision)
			}
		})
	}
}

// The record is read once. A listing that changes between two reads cannot
// split the level from the revision of one request.
func TestRequestViewReadsTheRecordOnce(t *testing.T) {
	st := newListingState(t,
		record("rev-2", "low", false, "low"),
		record("rev-3", "high", false, "low", "high"),
	)
	in := requestView(t, st, nil, "high")
	if n := st.readCount(); n != 1 {
		t.Fatalf("the listing was read %d times for one request, want 1", n)
	}
	if in.ExpectedRevision != "rev-2" || in.ReasoningEffort != "low" {
		t.Fatalf("input = revision %q effort %q, want rev-2 and low: both from the first record", in.ExpectedRevision, in.ReasoningEffort)
	}
}

// A refresh that lands between the moment the session's level was resolved and
// the moment the request is built cannot produce a refusal at a matching
// revision: for an inheriting row, whatever level the session held and whatever
// record the refresh brought, the remote that holds that record accepts the
// request it is sent (the level is one it offers, or none).
func TestRequestViewARefreshBetweenTheReadsCannotRefuseAtAMatchingRevision(t *testing.T) {
	records := []llm.ModelEntry{
		record("r1", "low", false, "low"),
		record("r2", "medium", false, "low", "medium", "high"),
		record("r3", "high", true, "medium", "high"),
		record("r4", "", false, "low", "high"),
		record("r5", "low", true),
		record("r6", "xhigh", false, "high", "xhigh"),
	}
	for _, rec := range records {
		for _, effort := range []string{"", "low", "medium", "high", "xhigh", "off"} {
			t.Run(fmt.Sprintf("%s level %q", rec.Revision, effort), func(t *testing.T) {
				in := requestView(t, newListingState(t, rec), nil, effort)
				if in.ExpectedRevision != rec.Revision {
					t.Fatalf("revision = %q, want %q", in.ExpectedRevision, rec.Revision)
				}
				if !offeredBy(rec, in.ReasoningEffort) {
					t.Fatalf("request names revision %s with level %q, which that record does not offer (levels %v, off %v)",
						rec.Revision, in.ReasoningEffort, rec.ReasoningLevels, rec.AllowReasoningOff)
				}
			})
		}
	}
}

// offeredBy reports whether a remote holding rec accepts a request at level:
// none is always accepted, "off" only when the record allows it, any other
// level only when it is one of the record's.
func offeredBy(rec llm.ModelEntry, level string) bool {
	switch level {
	case "":
		return true
	case "off":
		return rec.AllowReasoningOff && len(rec.ReasoningLevels) > 0
	}
	for _, lv := range rec.ReasoningLevels {
		if lv == level {
			return true
		}
	}
	return false
}

// getProvider calls the view for a coddy row, with the level the session
// resolved, and leaves every other provider type exactly as it was: the level
// as EffectiveReasoning gave it, no revision, and no read of any listing.
func TestGetProviderBuildsACoddyRequestFromOneRecord(t *testing.T) {
	st := newListingState(t, record("rev-7", "low", false, "low"))
	st.SetSelectedReasoning("high")
	cfg := coddyConfig("https://remote.example", func(c *config.Config) {
		c.Models[0].ReasoningLevels = &[]string{"low", "high"}
		c.Models[1].ReasoningLevels = &[]string{"low", "high"}
	})
	cfg.Agent.ApplyDefaults()
	ag := NewAgent(cfg, st, resumePermissionSender{}, nil)
	var got []llm.ProviderInput
	ag.providerFactory = func(in llm.ProviderInput) (llm.Provider, error) {
		got = append(got, in)
		return &silentLaneProvider{}, nil
	}

	cfg.Agent.Model = coddyRow
	if _, err := ag.getProvider("agent"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ExpectedRevision != "rev-7" || got[0].ReasoningEffort != "high" {
		t.Fatalf("coddy input = %+v, want revision rev-7 and the written level high", got)
	}
	if n := st.readCount(); n != 1 {
		t.Fatalf("a coddy request read the listing %d times, want 1", n)
	}

	cfg.Agent.Model = "plain/gpt"
	if _, err := ag.getProvider("agent"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].ExpectedRevision != "" || got[1].ReasoningEffort != "high" || got[1].RefreshCapabilities != nil {
		t.Fatalf("openai input = %+v, want the level high, no revision and no refresh", got)
	}
	if n := st.readCount(); n != 1 {
		t.Fatalf("a row of another type read the listing: %d reads in all, want still 1", n)
	}
}

// A child's fallback chain builds each coddy candidate through the same view,
// and a candidate of another type as before.
func TestChildFallbacksBuildACoddyCandidateFromOneRecord(t *testing.T) {
	st := newListingState(t, record("rev-9", "low", false, "low"))
	st.SetSelectedReasoning("high")
	cfg := coddyConfig("https://remote.example", func(c *config.Config) {
		c.Models = append(c.Models, config.ModelEntry{Model: "remote/backup", MaxTokens: 100})
		c.Models[0].ReasoningLevels = &[]string{"low", "high"}
		c.Models[2].ReasoningLevels = &[]string{"low", "high"}
		c.Models[1].ReasoningLevels = &[]string{"low", "high"}
	})
	cfg.Agent.ApplyDefaults()
	ag := NewAgent(cfg, st, resumePermissionSender{}, nil)
	ag.subagent = &session.SubagentMeta{Depth: 1, FallbackModels: []string{"remote/backup", "plain/gpt"}}
	byModel := map[string]llm.ProviderInput{}
	ag.providerFactory = func(in llm.ProviderInput) (llm.Provider, error) {
		byModel[in.Name+"/"+in.Model] = in
		return &silentLaneProvider{}, nil
	}
	if _, err := ag.getProvider("agent"); err != nil {
		t.Fatal(err)
	}
	for _, sel := range []string{"remote/coder", "remote/backup"} {
		in, ok := byModel[sel]
		if !ok {
			t.Fatalf("no provider was built for %s: %v", sel, byModel)
		}
		if in.ExpectedRevision != "rev-9" || in.ReasoningEffort != "high" {
			t.Errorf("%s input = revision %q effort %q, want rev-9 and the written level high", sel, in.ExpectedRevision, in.ReasoningEffort)
		}
	}
	if in := byModel["plain/gpt"]; in.ExpectedRevision != "" || in.ReasoningEffort != "high" {
		t.Errorf("plain/gpt input = revision %q effort %q, want none and high as before", in.ExpectedRevision, in.ReasoningEffort)
	}
	if n := st.readCount(); n != 2 {
		t.Fatalf("the listing was read %d times for two coddy candidates, want one each", n)
	}
}

// The helper calls that build a provider input without a request level
// (compaction's summarizer, the memory copilot) leave the revision to the
// request view: the input of coddyProviderInput carries the wait budget and the
// refresh and no ExpectedRevision.
func TestCoddyProviderInputCarriesNoRevision(t *testing.T) {
	st := newListingState(t, record("rev-4", "low", false, "low"))
	cfg := coddyConfig("https://remote.example", nil)
	cfg.Agent.ApplyDefaults()
	ag := NewAgent(cfg, st, resumePermissionSender{}, nil)
	for name, in := range map[string]llm.ProviderInput{
		"turn":   ag.turnProviderInput(resolved(t, cfg, coddyRow)),
		"helper": ag.llmProviderInput(resolved(t, cfg, coddyRow)),
	} {
		if in.ExpectedRevision != "" {
			t.Errorf("%s input names revision %q before the request view ran", name, in.ExpectedRevision)
		}
		if in.RefreshCapabilities == nil || in.BusyWait == 0 {
			t.Errorf("%s input lacks the refresh or the busy wait budget: %+v", name, in)
		}
	}
}

// --- through a session manager ------------------------------------------------

// An inheriting row, narrowed by the remote: the cache holds the new record, the
// session's selection high is no longer offered, and the request goes out at the
// record's default with the new revision. The remote that holds that record
// accepts it, in one call.
func TestInheritedLevelGoesOutAtTheNarrowedRecordsDefault(t *testing.T) {
	rm := newRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, req llm.WireRequest) {
		if e := req.Options.ReasoningEffort; e != nil && *e != "low" {
			refuse(w, http.StatusBadRequest, llm.WireError{Kind: llm.WireKindInvalid, Code: "invalid_option"})
			return
		}
		f := startFrames(w)
		f.text("ok")
		f.final("ok")
	})
	h := newCoddyHarness(t, rm, nil)
	h.setListing("rev-1", []string{"low", "high"}, "low", false)
	if _, err := h.relist(); err != nil {
		t.Fatal(err)
	}
	h.mgr.SessionByID(h.sessionID).SetSelectedReasoning("high")
	h.setListing("rev-2", []string{"low"}, "low", false)
	if _, err := h.relist(); err != nil {
		t.Fatal(err)
	}

	if err := h.prompt("hello"); err != nil {
		t.Fatal(err)
	}
	if n := rm.count(); n != 1 {
		t.Fatalf("remote saw %d requests, want 1", n)
	}
	opts := rm.request(1).Options
	if opts.ReasoningEffort == nil || *opts.ReasoningEffort != "low" || opts.ExpectedRevision == nil || *opts.ExpectedRevision != "rev-2" {
		t.Fatalf("options = effort %v revision %v, want low at rev-2", opts.ReasoningEffort, opts.ExpectedRevision)
	}
}

// A row that writes reasoning_levels: [low, high] keeps high when the remote
// narrows to [low]: the request names the new revision and the old level, the
// remote answers invalid_option, and nothing re-issues it or refreshes the
// cache - the key is the operator's, and a refresh cannot change it.
func TestExplicitLevelsKeepHighAndTheRemoteAnswersInvalidOption(t *testing.T) {
	rm := newRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, req llm.WireRequest) {
		if e := req.Options.ReasoningEffort; e != nil && *e == "high" {
			refuse(w, http.StatusBadRequest, llm.WireError{Kind: llm.WireKindInvalid, Code: "invalid_option"})
			return
		}
		f := startFrames(w)
		f.text("ok")
		f.final("ok")
	})
	h := newCoddyHarness(t, rm, func(c *config.Config) {
		c.Models[0].ReasoningLevels = &[]string{"low", "high"}
	})
	h.setListing("rev-1", []string{"low", "high"}, "low", false)
	if _, err := h.relist(); err != nil {
		t.Fatal(err)
	}
	h.mgr.SessionByID(h.sessionID).SetSelectedReasoning("high")
	h.setListing("rev-2", []string{"low"}, "low", false)
	if _, err := h.relist(); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	listingsBefore := h.listings
	h.mu.Unlock()

	_ = h.prompt("hello")
	if n := rm.count(); n != 1 {
		t.Fatalf("remote saw %d requests, want 1: invalid_option is neither re-issued nor refreshed away", n)
	}
	opts := rm.request(1).Options
	if opts.ReasoningEffort == nil || *opts.ReasoningEffort != "high" || opts.ExpectedRevision == nil || *opts.ExpectedRevision != "rev-2" {
		t.Fatalf("options = effort %v revision %v, want the written level high at the new revision rev-2", opts.ReasoningEffort, opts.ExpectedRevision)
	}
	h.mu.Lock()
	listingsAfter := h.listings
	h.mu.Unlock()
	if listingsAfter != listingsBefore {
		t.Fatalf("the listing was read %d more times during the call, want none: no refresh fixes a key the operator wrote", listingsAfter-listingsBefore)
	}
}

// A refresh that fails keeps the last record that answered: the next request is
// built from it, revision and level together.
func TestAFailedRefreshKeepsTheLastRecordThatAnswered(t *testing.T) {
	rm := newRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ llm.WireRequest) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	h := newCoddyHarness(t, rm, nil)
	h.setListing("rev-1", []string{"low", "high"}, "low", false)
	if _, err := h.relist(); err != nil {
		t.Fatal(err)
	}
	st := h.mgr.SessionByID(h.sessionID)
	st.SetSelectedReasoning("high")

	h.failListing(errors.New("the remote is down"))
	if rec, err := h.relist(); err == nil {
		t.Fatalf("the refresh answered %+v, want the failure the stand-in injected", rec)
	}

	ag := NewAgent(h.mgr.Cfg(), st, &limitWaitCapture{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var got llm.ProviderInput
	ag.providerFactory = func(in llm.ProviderInput) (llm.Provider, error) {
		got = in
		return &silentLaneProvider{}, nil
	}
	if _, err := ag.getProvider("agent"); err != nil {
		t.Fatal(err)
	}
	if got.ExpectedRevision != "rev-1" || got.ReasoningEffort != "high" {
		t.Fatalf("input after the failed refresh = revision %q effort %q, want the last record's rev-1 and high", got.ExpectedRevision, got.ReasoningEffort)
	}
}
