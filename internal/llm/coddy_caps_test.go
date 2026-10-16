package llm

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

// staleUntil answers stale_revision while the request's expected_revision is not
// the row's current revision, before anything is generated.
func staleUntil(current string, generated *atomic.Int32) fakeRemoteHandler {
	return func(w http.ResponseWriter, r *http.Request, n int, req WireRequest) {
		if req.Options.ExpectedRevision != nil && *req.Options.ExpectedRevision != current {
			answerWire(w, http.StatusBadRequest, WireError{Status: 400, Kind: WireKindInvalid, Code: WireCodeStaleRevision, Revision: current, Message: "the row changed"})
			return
		}
		generated.Add(1)
		simpleAnswer("Done after the refresh.")(w, r, n, req)
	}
}

func TestCoddyStaleRevisionRefreshesAndSendsOnceMore(t *testing.T) {
	var generated atomic.Int32
	remote := newFakeRemote(t, staleUntil("rev-2", &generated))
	var refreshes atomic.Int32
	in := coddyInput(remote)
	in.ExpectedRevision = "rev-1"
	in.RetryMax, in.RetryBase = 3, time.Millisecond
	in.RefreshCapabilities = func(context.Context) (*ModelEntry, error) {
		refreshes.Add(1)
		return &ModelEntry{ID: "coder", Revision: "rev-2", ContextWindow: 100000, Multimodal: true}, nil
	}
	got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
	if got.err != nil || got.resp.Content != "Done after the refresh." {
		t.Fatalf("resp %+v err %v", got.resp, got.err)
	}
	if remote.count() != 2 || generated.Load() != 1 || refreshes.Load() != 1 {
		t.Fatalf("%d requests, %d generations, %d refreshes", remote.count(), generated.Load(), refreshes.Load())
	}
	if first := remote.request(1).req.Options.ExpectedRevision; first == nil || *first != "rev-1" {
		t.Fatalf("the first request carries the revision of the cached view: %v", first)
	}
	if second := remote.request(2).req.Options.ExpectedRevision; second == nil || *second != "rev-2" {
		t.Fatalf("the retried request carries the refreshed revision: %v", second)
	}
}

func TestCoddyWithoutAnExpectedRevisionNothingIsChecked(t *testing.T) {
	var generated atomic.Int32
	remote := newFakeRemote(t, staleUntil("rev-9", &generated))
	got := streamOnce(newTestCoddy(t, coddyInput(remote)), userMsg("Hi"), nil)
	if got.err != nil || remote.count() != 1 {
		t.Fatalf("err %v, %d requests", got.err, remote.count())
	}
	if remote.request(1).req.Options.ExpectedRevision != nil {
		t.Fatalf("absent means no check: %s", remote.request(1).raw)
	}
}

func TestCoddyASecondStaleRevisionEndsTheCallWithTheInvalidError(t *testing.T) {
	remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
		answerWire(w, http.StatusBadRequest, WireError{Status: 400, Kind: WireKindInvalid, Code: WireCodeStaleRevision, Revision: "rev-3", Message: "the row changed again"})
	})
	var refreshes atomic.Int32
	in := coddyInput(remote)
	in.ExpectedRevision = "rev-1"
	in.RetryMax, in.RetryBase = 3, time.Millisecond
	in.RefreshCapabilities = func(context.Context) (*ModelEntry, error) {
		refreshes.Add(1)
		return &ModelEntry{ID: "coder", Revision: "rev-2"}, nil
	}
	got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
	if CoddyErrorKind(got.err) != WireKindInvalid || CoddyErrorCode(got.err) != WireCodeStaleRevision {
		t.Fatalf("err %v", got.err)
	}
	if remote.count() != 2 || refreshes.Load() != 1 {
		t.Fatalf("%d requests and %d refreshes: one refresh, one more send, then the call ends", remote.count(), refreshes.Load())
	}
}

func TestCoddyStaleRevisionWithoutARefreshHookIsTheInvalidError(t *testing.T) {
	remote := newFakeRemote(t, func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
		answerWire(w, http.StatusBadRequest, WireError{Status: 400, Kind: WireKindInvalid, Code: WireCodeStaleRevision, Revision: "rev-2"})
	})
	in := coddyInput(remote)
	in.ExpectedRevision = "rev-1"
	got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
	if CoddyErrorCode(got.err) != WireCodeStaleRevision || remote.count() != 1 {
		t.Fatalf("err %v after %d requests", got.err, remote.count())
	}
}

func TestCoddyStaleRevisionWhenTheRefreshFailsOrTheRowIsGone(t *testing.T) {
	stale := func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
		answerWire(w, http.StatusBadRequest, WireError{Status: 400, Kind: WireKindInvalid, Code: WireCodeStaleRevision, Revision: "rev-2"})
	}
	t.Run("the refresh fails", func(t *testing.T) {
		remote := newFakeRemote(t, stale)
		in := coddyInput(remote)
		in.ExpectedRevision = "rev-1"
		boom := errors.New("listing unavailable")
		in.RefreshCapabilities = func(context.Context) (*ModelEntry, error) { return nil, boom }
		got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
		if !errors.Is(got.err, boom) || remote.count() != 1 {
			t.Fatalf("err %v after %d requests", got.err, remote.count())
		}
	})
	t.Run("the alias is no longer listed", func(t *testing.T) {
		remote := newFakeRemote(t, stale)
		in := coddyInput(remote)
		in.ExpectedRevision = "rev-1"
		in.RefreshCapabilities = func(context.Context) (*ModelEntry, error) { return nil, nil }
		got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
		if CoddyErrorCode(got.err) != WireCodeStaleRevision || remote.count() != 1 {
			t.Fatalf("err %v after %d requests", got.err, remote.count())
		}
	})
}

func TestCoddyStaleRevisionFallsBackALevelTheRefreshedRowNoLongerLists(t *testing.T) {
	cases := []struct {
		name      string
		effort    string
		entry     ModelEntry
		wantLevel *string
	}{
		{"still listed", "high", ModelEntry{Revision: "r2", ReasoningLevels: []string{"low", "high"}, ReasoningDefault: "low"}, ptr("high")},
		{"narrowed away, falls back to the default", "high", ModelEntry{Revision: "r2", ReasoningLevels: []string{"low"}, ReasoningDefault: "low"}, ptr("low")},
		{"narrowed away, no default", "high", ModelEntry{Revision: "r2", ReasoningLevels: []string{"low"}}, nil},
		{"reasoning gone altogether", "high", ModelEntry{Revision: "r2"}, nil},
		{"off allowed", "off", ModelEntry{Revision: "r2", ReasoningLevels: []string{"low"}, AllowReasoningOff: true}, ptr("off")},
		{"off no longer allowed", "off", ModelEntry{Revision: "r2", ReasoningLevels: []string{"low", "high"}, ReasoningDefault: "high"}, ptr("high")},
		{"a default that is not a level is no default", "high", ModelEntry{Revision: "r2", ReasoningLevels: []string{"low"}, ReasoningDefault: "xhigh"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var generated atomic.Int32
			remote := newFakeRemote(t, staleUntil("r2", &generated))
			in := coddyInput(remote)
			in.ExpectedRevision, in.ReasoningEffort = "r1", tc.effort
			entry := tc.entry
			in.RefreshCapabilities = func(context.Context) (*ModelEntry, error) { return &entry, nil }
			got := streamOnce(newTestCoddy(t, in), userMsg("Hi"), nil)
			if got.err != nil {
				t.Fatal(got.err)
			}
			first := remote.request(1).req.Options.ReasoningEffort
			if first == nil || *first != tc.effort {
				t.Fatalf("the first request sends the level as the session chose it: %v", first)
			}
			second := remote.request(2).req.Options.ReasoningEffort
			if (second == nil) != (tc.wantLevel == nil) || (second != nil && *second != *tc.wantLevel) {
				t.Fatalf("the retried request sends %v, want %v", deref(second), deref(tc.wantLevel))
			}
		})
	}
}

func TestCoddyStaleRevisionDropsImagesWhenTheRowIsNoLongerMultimodal(t *testing.T) {
	images := []Message{
		{Role: RoleUser, Content: "look", ImageParts: []ImagePart{{DataURL: "data:image/png;base64,AAAA", MIMEType: "image/png"}}},
		{Role: RoleAssistant, Content: "a square"},
		{Role: RoleUser, Content: "and now", ImageParts: []ImagePart{{DataURL: "data:text/plain;base64,aGk=", Name: "note.txt"}}},
	}
	for name, tc := range map[string]struct {
		multimodal bool
		wantImages int
	}{"still multimodal": {true, 2}, "no longer multimodal": {false, 0}} {
		t.Run(name, func(t *testing.T) {
			var generated atomic.Int32
			remote := newFakeRemote(t, staleUntil("r2", &generated))
			in := coddyInput(remote)
			in.ExpectedRevision = "r1"
			in.RefreshCapabilities = func(context.Context) (*ModelEntry, error) {
				return &ModelEntry{Revision: "r2", Multimodal: tc.multimodal}, nil
			}
			msgs := slices.Clone(images)
			got := streamOnce(newTestCoddy(t, in), msgs, nil)
			if got.err != nil {
				t.Fatal(got.err)
			}
			count := func(req WireRequest) (n int) {
				for _, m := range req.Messages {
					n += len(m.ImageParts)
				}
				return n
			}
			if first := count(remote.request(1).req); first != 2 {
				t.Fatalf("the first request sends the history as it is: %d image parts", first)
			}
			if second := count(remote.request(2).req); second != tc.wantImages {
				t.Fatalf("the retried request carries %d image parts, want %d", second, tc.wantImages)
			}
			// The caller's own history is never rewritten.
			if len(msgs[0].ImageParts) != 1 || len(msgs[2].ImageParts) != 1 {
				t.Fatal("the provider changed the caller's messages")
			}
		})
	}
}

func TestCoddyARefreshedViewServesTheNextCallOfTheSameProvider(t *testing.T) {
	var generated atomic.Int32
	remote := newFakeRemote(t, staleUntil("r2", &generated))
	var refreshes atomic.Int32
	in := coddyInput(remote)
	in.ExpectedRevision = "r1"
	in.RefreshCapabilities = func(context.Context) (*ModelEntry, error) {
		refreshes.Add(1)
		return &ModelEntry{Revision: "r2"}, nil
	}
	p := newTestCoddy(t, in)
	for i := 0; i < 2; i++ {
		if got := streamOnce(p, userMsg("Hi"), nil); got.err != nil {
			t.Fatal(got.err)
		}
	}
	if refreshes.Load() != 1 || remote.count() != 3 {
		t.Fatalf("%d refreshes, %d requests: the second call starts from the refreshed view", refreshes.Load(), remote.count())
	}
}

func TestCoddyAContextWindowThatShrankIsNotAppliedToTheRetriedRequest(t *testing.T) {
	// The history is not re-projected: the retried request carries exactly the
	// messages of the first one.
	var generated atomic.Int32
	remote := newFakeRemote(t, staleUntil("r2", &generated))
	in := coddyInput(remote)
	in.ExpectedRevision = "r1"
	in.RefreshCapabilities = func(context.Context) (*ModelEntry, error) {
		return &ModelEntry{Revision: "r2", ContextWindow: 10}, nil
	}
	msgs := []Message{{Role: RoleUser, Content: bigText(5000)}}
	if got := streamOnce(newTestCoddy(t, in), msgs, nil); got.err != nil {
		t.Fatal(got.err)
	}
	if len(remote.request(2).req.Messages) != 1 || remote.request(2).req.Messages[0].Content != bigText(5000) {
		t.Fatal("the retried request was re-projected")
	}
}

func ptr[T any](v T) *T { return &v }

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
