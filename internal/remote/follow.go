package remote

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// FollowUpdate is a backend-local control notification for the console, not an
// ACP protocol extension: this client follows a turn the server started on its
// own (Active true), or that turn has ended (Active false). The turn's frames
// arrive in between as ordinary session updates.
type FollowUpdate struct {
	Active bool
}

// followState is the woken turns this client follows, one per session.
type followState struct {
	followMu  sync.Mutex
	following map[string]context.CancelFunc
	// followed is, per session, the woken turn read last and the last frame
	// of it applied: a follower cut off mid-turn that picks the same turn up
	// again - the reconnect's snapshot announces it anew - resumes after that
	// frame instead of showing the turn twice.
	followed map[string]followedTurn
	followWG sync.WaitGroup
}

// followedTurn names a woken turn by its start, as the server dates it on
// background_wake and on /activity, and keeps the last frame applied.
type followedTurn struct {
	turn    string
	lastSeq uint64
}

// relayFrom is where a follower asks the relay to start: after a frame of the
// same turn it already applied, or after the transcript this client loaded.
type relayFrom struct {
	lastEventID uint64
	sinceRev    uint64
	bySnapshot  bool
}

// applyWakeEvent follows the turn a background_wake event announces, when the
// session is one this client has open. A turn this client posted is read on
// its own response already; a woken turn nobody posted is read here, on the
// session's composer relay - the stream a browser watching the session reads -
// so the wake, the answer and a permission prompt the turn raises reach this
// console as they would for a turn it started.
func (h *Handler) applyWakeEvent(data string) {
	var payload struct {
		SessionID string `json:"sessionId"`
		At        string `json:"at"`
	}
	if json.Unmarshal([]byte(data), &payload) != nil || !h.knowsSession(payload.SessionID) {
		return
	}
	h.followTurn(payload.SessionID, payload.At)
}

// followTurn reads the session's running turn from its composer relay until
// the turn ends, the relay closes or the client does. One follower per session.
// turn names the turn by its start; a turn followed before is picked up after
// the last frame applied, a new one after the transcript this client loaded.
func (h *Handler) followTurn(sessionID, turn string) {
	sender := h.currentSender()
	if sender == nil || h.controlCtx.Err() != nil {
		return
	}
	h.mu.Lock()
	var from relayFrom
	if st := h.sessions[sessionID]; st != nil && st.historyLoaded {
		from.sinceRev, from.bySnapshot = st.historyRev, true
	}
	h.mu.Unlock()
	h.followMu.Lock()
	if h.following == nil {
		h.following = map[string]context.CancelFunc{}
	}
	if h.followed == nil {
		h.followed = map[string]followedTurn{}
	}
	if h.following[sessionID] != nil {
		h.followMu.Unlock()
		return
	}
	if prev := h.followed[sessionID]; turn != "" && prev.turn == turn {
		from.lastEventID = prev.lastSeq
	} else {
		h.followed[sessionID] = followedTurn{turn: turn}
	}
	ctx, cancel := context.WithCancel(h.controlCtx)
	h.following[sessionID] = cancel
	h.followWG.Add(1)
	h.followMu.Unlock()

	go func() {
		defer h.followWG.Done()
		h.sendFollow(sessionID, true)
		if err := h.readRelay(ctx, sessionID, turn, from, sender); err != nil && ctx.Err() == nil {
			h.log.Debug("remote woken turn: relay dropped", "session", sessionID, "error", err)
		}
		h.followMu.Lock()
		delete(h.following, sessionID)
		h.followMu.Unlock()
		// Cancelled before the end is announced: a question still open for
		// this turn is withdrawn first, so the surface takes it down on the
		// same signal that ends the turn.
		cancel()
		h.sendFollow(sessionID, false)
	}()
}

// readRelay translates the frames of the session's composer relay into session
// updates, answering permission and question frames the way a posted turn does.
// A frame cursor wins over the transcript's revision, as on the server.
func (h *Handler) readRelay(ctx context.Context, sessionID, turn string, from relayFrom, sender acp.UpdateSender) error {
	path := "/coddy/sessions/" + url.PathEscape(sessionID) + "/composer-stream"
	if from.lastEventID == 0 && from.bySnapshot {
		path += "?since_rev=" + strconv.FormatUint(from.sinceRev, 10)
	}
	req, err := h.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Coddy-Session-ID", sessionID)
	if from.lastEventID > 0 {
		req.Header.Set("Last-Event-ID", strconv.FormatUint(from.lastEventID, 10))
	}
	res, err := h.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
		return h.remoteError(res, body)
	}
	stream := &turnStream{h: h, ctx: ctx, sessionID: sessionID, sender: sender, follow: true}
	return readSSE(res.Body, func(f sseFrame) error {
		err := stream.onFrame(f)
		if f.id > 0 {
			h.noteFollowed(sessionID, turn, f.id)
		}
		return err
	})
}

// noteFollowed records the last frame of the turn a follower applied.
func (h *Handler) noteFollowed(sessionID, turn string, seq uint64) {
	h.followMu.Lock()
	defer h.followMu.Unlock()
	if prev, ok := h.followed[sessionID]; ok && prev.turn == turn && seq > prev.lastSeq {
		h.followed[sessionID] = followedTurn{turn: turn, lastSeq: seq}
	}
}

func (h *Handler) sendFollow(sessionID string, active bool) {
	if sender, ok := h.currentSender().(controlUpdateSender); ok {
		_ = sender.SendControlUpdate(sessionID, FollowUpdate{Active: active})
	}
}

// stopFollowing ends every follower and waits for them (Close).
func (h *Handler) stopFollowing() {
	h.followMu.Lock()
	for _, cancel := range h.following {
		cancel()
	}
	h.followMu.Unlock()
	h.followWG.Wait()
}
