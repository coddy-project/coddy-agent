package remote

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// followSender is collectSender that also takes the backend-local control
// updates the console opts into.
type followSender struct {
	collectSender
	controls []interface{}
}

func (c *followSender) SendControlUpdate(_ string, u any) error {
	c.mu.Lock()
	c.controls = append(c.controls, u)
	c.mu.Unlock()
	return nil
}

// wakeServer announces one background wake on its events stream and serves
// the woken turn's frames on the composer relay of that session.
type wakeServer struct {
	sessionID string
	mu        sync.Mutex
	followed  int
	answered  []string
	// answer is closed by the permission answer: the woken turn waits for it
	// before it goes on, as a real turn blocked on a prompt does.
	answer chan struct{}
}

func (s *wakeServer) handler(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/coddy/events":
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		_, _ = fmt.Fprint(w, "event: ready\ndata: {}\n\n")
		// A wake of a session this client never opened, then one it has open.
		for _, id := range []string{"sess_elsewhere", s.sessionID} {
			_, _ = fmt.Fprintf(w, "event: background_wake\ndata: {\"object\":\"coddy.background_wake\",\"sessionId\":%q,\"phase\":\"woken\","+
				"\"tasks\":[{\"id\":\"bg_3\",\"kind\":\"command\",\"label\":\"make test\",\"status\":\"failed\",\"exitCode\":2,\"durationMs\":90000}]}\n\n", id)
		}
		if fl != nil {
			fl.Flush()
		}
		<-r.Context().Done()
	case r.URL.Path == "/coddy/sessions/"+s.sessionID+"/composer-stream":
		s.mu.Lock()
		s.followed++
		s.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "id: 1\nevent: background_wake\ndata: {\"sessionUpdate\":\"background_wake\","+
			"\"tasks\":[{\"id\":\"bg_3\",\"kind\":\"command\",\"label\":\"make test\",\"status\":\"failed\",\"exitCode\":2,\"durationMs\":90000}]}\n\n")
		_, _ = fmt.Fprint(w, "id: 2\nevent: permission\ndata: {\"sessionId\":\""+s.sessionID+"\",\"toolCall\":{\"toolCallId\":\"call_fix\",\"title\":\"Run: make fix\"},"+
			"\"options\":[{\"optionId\":\"allow\",\"name\":\"Allow\",\"kind\":\"allow_once\"}]}\n\n")
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		select {
		case <-s.answer:
		case <-time.After(5 * time.Second):
		}
		_, _ = fmt.Fprint(w, "id: 3\nevent: tool_call_update\ndata: {\"sessionUpdate\":\"tool_call_update\",\"toolCallId\":\"call_fix\",\"status\":\"completed\"}\n\n")
		_, _ = fmt.Fprint(w, "id: 4\ndata: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"The tests failed.\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "id: 5\ndata: [DONE]\n\n")
	case r.URL.Path == "/coddy/sessions/"+s.sessionID+"/permission":
		s.mu.Lock()
		s.answered = append(s.answered, r.URL.Path)
		if len(s.answered) == 1 {
			close(s.answer)
		}
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	case strings.HasPrefix(r.URL.Path, "/coddy/sessions/"):
		http.NotFound(w, r)
	default:
		http.NotFound(w, r)
	}
}

// A console attached over --remote renders a turn the server woke in a session
// it has open: it follows the turn on the composer relay, so the wake, a
// permission prompt the turn raises and the answer reach it as they would for
// a turn it started. A wake of a session it never opened is somebody else's.
func TestAWokenTurnOfAnOpenSessionIsFollowed(t *testing.T) {
	ws := &wakeServer{sessionID: "sess_open", answer: make(chan struct{})}
	srv := httptest.NewServer(http.HandlerFunc(ws.handler))
	defer srv.Close()

	h, err := NewHandler(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	sender := &followSender{}
	h.SetServer(sender)
	h.session("sess_open")
	h.StartEvents()
	defer h.Close()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		sender.mu.Lock()
		done := false
		for _, c := range sender.controls {
			if f, ok := c.(FollowUpdate); ok && !f.Active {
				done = true
			}
		}
		sender.mu.Unlock()
		if done {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	var wakes []acp.BackgroundWakeUpdate
	for _, u := range sender.updates {
		if w, ok := u.(acp.BackgroundWakeUpdate); ok {
			wakes = append(wakes, w)
		}
	}
	if len(wakes) != 1 || len(wakes[0].Tasks) != 1 || wakes[0].Tasks[0].ID != "bg_3" || wakes[0].Tasks[0].ExitCode == nil || *wakes[0].Tasks[0].ExitCode != 2 {
		t.Fatalf("wakes = %+v, want the one of the open session", wakes)
	}
	var texts []string
	for _, u := range sender.updates {
		if chunk, ok := u.(acp.MessageChunkUpdate); ok {
			texts = append(texts, chunk.Content.Type+":"+chunk.Content.Text)
		}
	}
	if got := strings.Join(texts, "|"); got != "text:The tests failed." {
		t.Fatalf("texts = %q, want the woken turn's answer", got)
	}
	var follows []bool
	for _, c := range sender.controls {
		if f, ok := c.(FollowUpdate); ok {
			follows = append(follows, f.Active)
		}
	}
	if len(follows) != 2 || !follows[0] || follows[1] {
		t.Fatalf("follow controls = %v, want start then end", follows)
	}
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if ws.followed != 1 {
		t.Fatalf("the relay was read %d times, want once", ws.followed)
	}
	if len(ws.answered) != 1 {
		t.Fatalf("permission answers posted = %v, want the one the console gave", ws.answered)
	}
}

// A session loaded over REST replays a woken turn's first message as the wake
// it was, never as a message from the user.
func TestReplayShowsAWakeInsteadOfAUserMessage(t *testing.T) {
	h, err := NewHandler(Options{BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	sender := &collectSender{}
	h.SetServer(sender)
	two := 2
	h.replayMessages("sess_1", []messageRow{
		{Role: "user", Content: "build it"},
		{Role: "user", Content: "A background task you asked to be notified about has finished.", BackgroundWake: &llm.BackgroundWake{
			Tasks: []llm.BackgroundWakeTask{{ID: "bg_3", Kind: "command", Label: "make test", Status: "failed", ExitCode: &two, DurationMs: 90_000}},
		}},
		{Role: "assistant", Content: "The tests failed."},
	})
	sender.mu.Lock()
	defer sender.mu.Unlock()
	var users []string
	var wakes []acp.BackgroundWakeUpdate
	for _, u := range sender.updates {
		switch v := u.(type) {
		case acp.MessageChunkUpdate:
			if v.SessionUpdate == acp.UpdateTypeUserMessageChunk {
				users = append(users, v.Content.Text)
			}
		case acp.BackgroundWakeUpdate:
			wakes = append(wakes, v)
		}
	}
	if len(users) != 1 || users[0] != "build it" {
		t.Fatalf("user rows = %q, want only the typed one", users)
	}
	if len(wakes) != 1 || wakes[0].Tasks[0].ID != "bg_3" || *wakes[0].Tasks[0].ExitCode != 2 || wakes[0].Tasks[0].DurationMs != 90_000 {
		t.Fatalf("wakes = %+v", wakes)
	}
}

// blockingAsker holds a permission question on screen until it is withdrawn.
type blockingAsker struct {
	collectSender
	asked     chan struct{}
	withdrawn chan struct{}
}

func (b *blockingAsker) RequestPermission(ctx context.Context, _ acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	b.asked <- struct{}{}
	<-ctx.Done()
	b.withdrawn <- struct{}{}
	return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
}

// A followed turn's prompt answered somewhere else - a browser watching the
// same session - is taken down here when the stream reports the tool call's
// final status, and this client posts nothing for it.
func TestAFollowedPromptAnsweredElsewhereIsWithdrawn(t *testing.T) {
	var posted int
	var mu sync.Mutex
	settle := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/coddy/sessions/sess_open/composer-stream":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, "event: permission\ndata: {\"sessionId\":\"sess_open\",\"toolCall\":{\"toolCallId\":\"call_fix\",\"title\":\"Run: make fix\"},"+
				"\"options\":[{\"optionId\":\"allow\",\"name\":\"Allow\",\"kind\":\"allow_once\"}]}\n\n")
			w.(http.Flusher).Flush()
			<-settle
			_, _ = fmt.Fprint(w, "event: tool_call_update\ndata: {\"sessionUpdate\":\"tool_call_update\",\"toolCallId\":\"call_fix\",\"status\":\"completed\"}\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case "/coddy/sessions/sess_open/permission":
			mu.Lock()
			posted++
			mu.Unlock()
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	h, err := NewHandler(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	asker := &blockingAsker{asked: make(chan struct{}, 1), withdrawn: make(chan struct{}, 1)}
	h.SetServer(asker)
	h.session("sess_open")
	defer h.Close()
	h.followTurn("sess_open", "")

	select {
	case <-asker.asked:
	case <-time.After(5 * time.Second):
		t.Fatal("the followed turn's prompt never reached the surface")
	}
	close(settle)
	select {
	case <-asker.withdrawn:
	case <-time.After(5 * time.Second):
		t.Fatal("the prompt stayed on screen after the tool call was settled elsewhere")
	}
	mu.Lock()
	defer mu.Unlock()
	if posted != 0 {
		t.Fatalf("a withdrawn prompt posted %d answers", posted)
	}
}

// relayProbe is a server whose session runs a woken turn: it answers the
// transcript at messagesRev 7, reports the turn on /activity, and serves the
// relay, recording how each reader asked to be caught up.
type relayProbe struct {
	mu       sync.Mutex
	requests []string // "since_rev=<n>" or "last=<n>" or "all", one per relay read
	// drop ends the relay read after the first frame, as a network drop does.
	drop bool
}

func (p *relayProbe) handler(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/v1/models":
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"object":"list","data":[{"id":"stub/m","owned_by":"stub"}]}`)
	case "/coddy/sessions/sess_open/messages":
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"messages":[{"role":"user","content":"start the tests"},`+
			`{"role":"user","content":"A background task you asked to be notified about has finished.",`+
			`"background_wake":{"tasks":[{"id":"bg_3","status":"failed"}]}}],"messagesRev":7}`)
	case "/coddy/sessions/sess_open/activity":
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"sessionId":"sess_open","turnActive":true,"turnStartedAt":"2026-09-18T12:00:00.5Z",`+
			`"backgroundWake":{"tasks":[{"id":"bg_3","status":"failed"}]}}`)
	case "/coddy/sessions/sess_open/queue":
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"messages":[],"version":0}`)
	case "/coddy/sessions/sess_open/composer-stream":
		how := "all"
		if last := r.Header.Get("Last-Event-ID"); last != "" {
			how = "last=" + last
		} else if rev := r.URL.Query().Get("since_rev"); rev != "" {
			how = "since_rev=" + rev
		}
		p.mu.Lock()
		p.requests = append(p.requests, how)
		drop := p.drop && len(p.requests) == 1
		p.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Like the relay, a reader that names a frame is sent what follows it.
		after, _ := strconv.ParseUint(r.Header.Get("Last-Event-ID"), 10, 64)
		frames := []string{
			"id: 4\ndata: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"Fixing \"}}]}\n\n",
			"id: 5\ndata: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"the parser.\"}}]}\n\n",
			"id: 6\ndata: [DONE]\n\n",
		}
		for i, frame := range frames {
			if uint64(4+i) <= after {
				continue
			}
			_, _ = fmt.Fprint(w, frame)
			if drop {
				return
			}
		}
	default:
		http.NotFound(w, r)
	}
}

func (p *relayProbe) reads() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.requests...)
}

func waitFollowEnds(t *testing.T, sender *followSender, ends int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		sender.mu.Lock()
		n := 0
		for _, c := range sender.controls {
			if f, ok := c.(FollowUpdate); ok && !f.Active {
				n++
			}
		}
		sender.mu.Unlock()
		if n >= ends {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the follower did not end %d times", ends)
}

func streamedText(sender *followSender) string {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	var b strings.Builder
	for _, u := range sender.updates {
		if chunk, ok := u.(acp.MessageChunkUpdate); ok && chunk.SessionUpdate == acp.UpdateTypeAgentMessageChunk {
			b.WriteString(chunk.Content.Text)
		}
	}
	return b.String()
}

// A console that resumes a session while a woken turn runs there - it was
// closed when the task ended - follows that turn from where the transcript it
// loaded ends: the server says on /activity that the turn is a wake, and the
// relay is asked only for the frames the transcript lacks.
func TestResumingASessionMidWakeFollowsTheTurn(t *testing.T) {
	probe := &relayProbe{}
	srv := httptest.NewServer(http.HandlerFunc(probe.handler))
	defer srv.Close()
	h, err := NewHandler(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	sender := &followSender{}
	h.SetServer(sender)
	defer h.Close()

	if _, err := h.HandleSessionLoad(context.Background(), acp.SessionLoadParams{SessionID: "sess_open"}); err != nil {
		t.Fatal(err)
	}
	h.HandleSessionReady("sess_open")
	waitFollowEnds(t, sender, 1)

	if got := probe.reads(); len(got) != 1 || got[0] != "since_rev=7" {
		t.Fatalf("relay reads = %v, want one caught up from the loaded transcript", got)
	}
	if got := streamedText(sender); got != "Fixing the parser." {
		t.Fatalf("followed text = %q", got)
	}
}

// A follower cut off mid-turn picks the same turn up again after the last
// frame it applied - nothing is shown twice - while the wake of another turn
// is read from the start.
func TestAFollowerPicksTheSameTurnUpWhereItStopped(t *testing.T) {
	probe := &relayProbe{drop: true}
	srv := httptest.NewServer(http.HandlerFunc(probe.handler))
	defer srv.Close()
	h, err := NewHandler(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	sender := &followSender{}
	h.SetServer(sender)
	h.session("sess_open")
	defer h.Close()

	wakeEvent := func(at string) string {
		return `{"object":"coddy.background_wake","sessionId":"sess_open","phase":"woken","at":"` + at + `",` +
			`"tasks":[{"id":"bg_3","status":"failed"}]}`
	}
	h.applyWakeEvent(wakeEvent("2026-09-18T12:00:00.5Z"))
	waitFollowEnds(t, sender, 1)
	// The reconnect's snapshot announces the same turn again.
	h.applyWakeEvent(wakeEvent("2026-09-18T12:00:00.5Z"))
	waitFollowEnds(t, sender, 2)
	if got := streamedText(sender); got != "Fixing the parser." {
		t.Fatalf("followed text = %q, want every frame once", got)
	}
	// A later turn of the same session is a turn of its own.
	h.applyWakeEvent(wakeEvent("2026-09-18T12:05:00Z"))
	waitFollowEnds(t, sender, 3)

	if got := probe.reads(); len(got) != 3 || got[0] != "all" || got[1] != "last=4" || got[2] != "all" {
		t.Fatalf("relay reads = %v, want the turn, the same turn after frame 4, then a new turn", got)
	}
}
