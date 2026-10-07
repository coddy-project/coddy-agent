package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

const goalJSON = `{"id":"goal_1","objective":"ship the release","status":"blocked","statusReason":"which branch?",` +
	`"continuations":2,"maxContinuations":10,"checks":3,"activeMs":1500,"tokensUsed":900,` +
	`"lastCheck":{"verdict":"needs_user","reason":"which branch?","remaining":["tag it"]},` +
	`"checklist":[{"text":"changelog","status":"met"}]}`

func (c *collectSender) goalUpdates() []acp.SessionGoalUpdate {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []acp.SessionGoalUpdate
	for _, u := range c.updates {
		if g, ok := u.(acp.SessionGoalUpdate); ok {
			out = append(out, g)
		}
	}
	return out
}

func (c *collectSender) goalTurns() []acp.GoalTurnUpdate {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []acp.GoalTurnUpdate
	for _, u := range c.updates {
		if g, ok := u.(acp.GoalTurnUpdate); ok {
			out = append(out, g)
		}
	}
	return out
}

// A goal set or checked on the server - by a browser, by the supervisor - is
// announced on the events stream; the console hears it as the session update
// a local manager publishes, and the client keeps it for the session.
func TestEventsStreamDeliversTheSessionGoal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/coddy/events" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "event: ready\ndata: {}\n\n")
		_, _ = fmt.Fprint(w, "event: session_goal\ndata: "+
			`{"object":"coddy.session_goal","sessionId":"sess_shared","goal":`+goalJSON+`,"version":7,"notice":"Goal blocked: which branch?"}`+"\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()

	h, err := NewHandler(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	sender := &collectSender{}
	h.SetServer(sender)
	h.StartEvents()
	defer h.Close()

	deadline := time.Now().Add(5 * time.Second)
	for len(sender.goalUpdates()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the events subscription never delivered the goal")
		}
		time.Sleep(20 * time.Millisecond)
	}
	u := sender.goalUpdates()[0]
	if u.SessionUpdate != acp.UpdateTypeSessionGoal || u.SessionID != "sess_shared" || u.Version != 7 ||
		u.Notice != "Goal blocked: which branch?" || u.Goal == nil || u.Goal.Status != acp.GoalStatusBlocked ||
		u.Goal.LastCheck == nil || len(u.Goal.LastCheck.Remaining) != 1 {
		t.Fatalf("goal update = %+v", u)
	}
	held, err := h.SessionGoal("sess_shared")
	if err != nil || held.Goal == nil || held.Goal.Objective != "ship the release" || held.Version != 7 || held.Notice != "" {
		t.Fatalf("held goal = %+v (%v)", held, err)
	}
}

// A goal turn of a prompt this client posted opens with its goal_turn frame,
// and the goal's changes during the turn arrive on the same stream: both reach
// the surface, and the client keeps the goal.
func TestTurnStreamForwardsTheGoalTurnAndTheGoal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			"event: session_goal\ndata: " + `{"sessionUpdate":"session_goal","sessionId":"sess_test","goal":` + goalJSON + `,"version":4,"notice":"Goal set: ship the release"}` + "\n\n" +
				"event: goal_turn\ndata: " + `{"sessionUpdate":"goal_turn","kind":"continue","index":2,"limit":10,"objective":"ship the release","reason":"the tag is missing","remaining":["tag it"]}` + "\n\n" +
				"data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"tagging\"}}]}\n\n" +
				"data: [DONE]\n\n"))
	}))
	defer srv.Close()
	h, err := NewHandler(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	sender := &collectSender{}
	h.SetServer(sender)
	if _, err := h.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{
		SessionID: "sess_test", Prompt: []acp.ContentBlock{{Type: "text", Text: "/goal ship the release"}},
	}, sender, nil); err != nil {
		t.Fatal(err)
	}
	turns := sender.goalTurns()
	if len(turns) != 1 || turns[0].SessionUpdate != acp.UpdateTypeGoalTurn || turns[0].Kind != acp.GoalTurnContinue ||
		turns[0].Index != 2 || turns[0].Reason != "the tag is missing" || len(turns[0].Remaining) != 1 {
		t.Fatalf("goal turns = %+v", turns)
	}
	goals := sender.goalUpdates()
	if len(goals) != 1 || goals[0].Version != 4 || goals[0].SessionID != "sess_test" || goals[0].Notice != "Goal set: ship the release" {
		t.Fatalf("goal updates = %+v", goals)
	}
	if held, _ := h.SessionGoal("sess_test"); held.Version != 4 || held.Goal == nil {
		t.Fatalf("held goal = %+v", held)
	}
}

// Loading a session replays its goal turns as the rows they were live, never
// as the instruction text the model read, and adopts the goal the transcript
// came with; on ready the goal goes to the surface like the local manager
// sends it.
func TestLoadReplaysGoalTurnsAndAdoptsTheGoal(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"remote/terra","default":true,"owned_by":"remote"}]}`))
	})
	mux.HandleFunc("GET /coddy/sessions/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"messages":[
			{"role":"user","content":"Work toward the goal below. <objective>ship the release</objective>","goal_turn":{"kind":"kickoff","limit":10,"objective":"ship the release"}},
			{"role":"assistant","content":"on it"},
			{"role":"user","content":"Continuation 1 of 10: the tag is missing","goal_turn":{"kind":"continue","index":1,"limit":10,"objective":"ship the release","reason":"the tag is missing","remaining":["tag it"]}},
			{"role":"user","content":"and the notes"}],
			"goal":{"object":"coddy.session_goal","sessionId":%q,"goal":%s,"version":11}}`, r.PathValue("id"), goalJSON)
	})
	mux.HandleFunc("GET /coddy/sessions/{id}/activity", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"sessionId":%q,"turnActive":false}`, r.PathValue("id"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	h, err := NewHandler(Options{BaseURL: srv.URL, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	sender := &collectSender{}
	h.SetServer(sender)
	if _, err := h.HandleSessionLoad(context.Background(), acp.SessionLoadParams{SessionID: "sess_goal"}); err != nil {
		t.Fatal(err)
	}
	turns := sender.goalTurns()
	if len(turns) != 2 || turns[0].Kind != acp.GoalTurnKickoff || turns[1].Kind != acp.GoalTurnContinue || turns[1].Reason != "the tag is missing" {
		t.Fatalf("replayed goal turns = %+v", turns)
	}
	for _, text := range sender.texts() {
		if strings.Contains(text, "Work toward the goal") || strings.Contains(text, "Continuation 1 of 10") {
			t.Fatalf("a goal turn's instruction was replayed as a message: %q", text)
		}
	}
	if texts := sender.texts(); len(texts) != 2 || texts[1] != "text:and the notes" {
		t.Fatalf("replayed messages = %q", texts)
	}
	held, err := h.SessionGoal("sess_goal")
	if err != nil || held.Goal == nil || held.Goal.Status != acp.GoalStatusBlocked || held.Version != 11 {
		t.Fatalf("held goal = %+v (%v)", held, err)
	}

	h.HandleSessionReady("sess_goal")
	goals := sender.goalUpdates()
	if len(goals) != 1 || goals[0].Version != 11 || goals[0].Goal == nil {
		t.Fatalf("ready sent %+v, want the session's goal", goals)
	}

	// A console started on the session (--session-id, -c) reopens it through
	// session/new: the goal comes along the same way.
	reopened, err := NewHandler(Options{BaseURL: srv.URL, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reopened.SetServer(&collectSender{})
	reopened.SetPreferredSessionID("sess_goal")
	if _, err := reopened.HandleSessionNew(context.Background(), acp.SessionNewParams{}); err != nil {
		t.Fatal(err)
	}
	if held, _ := reopened.SessionGoal("sess_goal"); held.Goal == nil || held.Version != 11 {
		t.Fatalf("a reopened session holds %+v", held)
	}
}

// The goal menu's reads and changes go through the server's goal routes, and
// the client keeps what they answer with. A session the server has not created
// yet has no goal.
func TestGoalRoutesReadPauseAndClear(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	status := "active"
	version := uint64(20)
	answer := func(w http.ResponseWriter, r *http.Request, goal string) {
		mu.Lock()
		version++
		v := version
		mu.Unlock()
		_, _ = fmt.Fprintf(w, `{"object":"coddy.session_goal","sessionId":%q,"goal":%s,"version":%d,"notice":""}`, r.PathValue("id"), goal, v)
	}
	goalOf := func() string {
		mu.Lock()
		defer mu.Unlock()
		return `{"id":"goal_1","objective":"ship","status":"` + status + `","continuations":0,"maxContinuations":10}`
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /coddy/sessions/{id}/goal", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "sess_new" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"session not found"}}`))
			return
		}
		mu.Lock()
		calls = append(calls, "GET")
		mu.Unlock()
		answer(w, r, goalOf())
	})
	mux.HandleFunc("PATCH /coddy/sessions/{id}/goal", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]string
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		calls = append(calls, "PATCH "+body["status"])
		status = body["status"]
		mu.Unlock()
		answer(w, r, goalOf())
	})
	mux.HandleFunc("DELETE /coddy/sessions/{id}/goal", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, "DELETE")
		mu.Unlock()
		answer(w, r, "null")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	h, err := NewHandler(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	read, err := h.FetchSessionGoal(ctx, "sess_goal")
	if err != nil || read.Goal == nil || read.Goal.Status != acp.GoalStatusActive || read.Version != 21 {
		t.Fatalf("fetch = %+v (%v)", read, err)
	}
	paused, err := h.PauseSessionGoal(ctx, "sess_goal")
	if err != nil || paused.Goal == nil || paused.Goal.Status != acp.GoalStatusPaused || paused.Version != 22 {
		t.Fatalf("pause = %+v (%v)", paused, err)
	}
	if held, _ := h.SessionGoal("sess_goal"); held.Goal == nil || held.Goal.Status != acp.GoalStatusPaused {
		t.Fatalf("held after pause = %+v", held)
	}
	cleared, err := h.ClearSessionGoal(ctx, "sess_goal")
	if err != nil || cleared.Goal != nil || cleared.Version != 23 || cleared.SessionUpdate != acp.UpdateTypeSessionGoal {
		t.Fatalf("clear = %+v (%v)", cleared, err)
	}
	if held, _ := h.SessionGoal("sess_goal"); held.Goal != nil || held.Version != 23 {
		t.Fatalf("held after clear = %+v", held)
	}
	mu.Lock()
	got := strings.Join(calls, ", ")
	mu.Unlock()
	if got != "GET, PATCH paused, DELETE" {
		t.Fatalf("the server saw %s", got)
	}

	fresh, err := h.FetchSessionGoal(ctx, "sess_new")
	if err != nil || fresh.Goal != nil {
		t.Fatalf("a session the server does not have yet: %+v (%v)", fresh, err)
	}
}

// Copies of one change reach the client down several streams: an older
// snapshot never replaces a newer one.
func TestAnOlderGoalSnapshotIsNotAdopted(t *testing.T) {
	h, err := NewHandler(Options{BaseURL: "http://127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	paused := &acp.SessionGoal{Objective: "ship", Status: acp.GoalStatusPaused}
	active := &acp.SessionGoal{Objective: "ship", Status: acp.GoalStatusActive}
	if !h.mirrorGoal("sess_goal", paused, 5) {
		t.Fatal("the first snapshot was not adopted")
	}
	if h.mirrorGoal("sess_goal", active, 4) {
		t.Fatal("an older snapshot was adopted")
	}
	held, _ := h.SessionGoal("sess_goal")
	if held.Goal == nil || held.Goal.Status != acp.GoalStatusPaused || held.Version != 5 {
		t.Fatalf("held = %+v", held)
	}
	// The mirror is a copy: changing what was handed in does not reach it.
	paused.Status = acp.GoalStatusComplete
	if held, _ := h.SessionGoal("sess_goal"); held.Goal.Status != acp.GoalStatusPaused {
		t.Fatal("the mirror shares the snapshot it adopted")
	}
}
