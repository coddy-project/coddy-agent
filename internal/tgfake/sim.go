package tgfake

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// The simulation API is the other side of the fake: where the person in the
// chat would be. It injects what a user does and reads back what the bot did.
//
//	POST /sim/message           {chat_id, chat_type, user_id, username, text, reply_to_message_id, mention}
//	POST /sim/callback          {chat_id, user_id, message_id, data} or {chat_id, label}
//	GET  /sim/outbox?method=&since=
//	GET  /sim/outbox/count?method=
//	GET  /sim/chat/{id}?format=text
//	GET  /sim/chats
//	GET  /sim/state
//	POST /sim/fault             {method, code, description, retry_after, times} or {method, clear: true}
//	DELETE /sim/fault
//	POST /sim/reset
func (s *Server) registerSim(mux *http.ServeMux) {
	mux.HandleFunc("POST /sim/message", s.simMessage)
	mux.HandleFunc("POST /sim/callback", s.simCallback)
	mux.HandleFunc("GET /sim/outbox", s.simOutbox)
	mux.HandleFunc("GET /sim/outbox/count", s.simOutboxCount)
	mux.HandleFunc("GET /sim/chat/{id}", s.simChat)
	mux.HandleFunc("GET /sim/chats", s.simChats)
	mux.HandleFunc("GET /sim/state", s.simState)
	mux.HandleFunc("POST /sim/fault", s.simFault)
	mux.HandleFunc("DELETE /sim/fault", s.simClearFaults)
	mux.HandleFunc("POST /sim/reset", s.simReset)
}

func (s *Server) simMessage(w http.ResponseWriter, r *http.Request) {
	var in IncomingMessage
	if !readJSON(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Text) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "text is empty"})
		return
	}
	upd, msgID := s.InjectMessage(in)
	writeJSON(w, http.StatusOK, map[string]any{"update_id": upd, "message_id": msgID})
}

func (s *Server) simCallback(w http.ResponseWriter, r *http.Request) {
	var in IncomingCallback
	if !readJSON(w, r, &in) {
		return
	}
	if in.Data == "" && in.Label == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "data or label is required"})
		return
	}
	upd, id, err := s.InjectCallback(in)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"update_id": upd, "callback_query_id": id})
}

func (s *Server) simOutbox(w http.ResponseWriter, r *http.Request) {
	since := atoi(r.URL.Query().Get("since"))
	calls := s.Calls(r.URL.Query().Get("method"))
	out := make([]Call, 0, len(calls))
	for _, c := range calls {
		if c.Seq > since {
			out = append(out, c)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"calls": out})
}

func (s *Server) simOutboxCount(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"count": len(s.Calls(r.URL.Query().Get("method")))})
}

func (s *Server) simChat(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "chat id must be an integer"})
		return
	}
	view := s.Chat(id)
	if r.URL.Query().Get("format") == "text" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(view.Text()))
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) simChats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"chats": s.Chats()})
}

func (s *Server) simState(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	pending := len(s.pending)
	next := s.nextUpdate
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"bot":             s.me(),
		"pending_updates": pending,
		"next_update_id":  next,
		"allowed_updates": s.AllowedUpdates(),
		"commands":        s.Commands(),
		"faults":          s.Faults(),
		"calls":           len(s.Calls("")),
	})
}

func (s *Server) simFault(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Fault
		Clear bool `json:"clear"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Clear {
		s.ClearFault(in.Method)
	} else {
		s.SetFault(in.Fault)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "faults": s.Faults()})
}

func (s *Server) simClearFaults(w http.ResponseWriter, _ *http.Request) {
	s.ClearFaults()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) simReset(w http.ResponseWriter, _ *http.Request) {
	s.Reset()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func readJSON(w http.ResponseWriter, r *http.Request, into any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(into); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body: " + err.Error()})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
