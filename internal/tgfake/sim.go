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
//	GET  /sim/file/{id}
//	GET  /sim/chats
//	GET  /sim/state
//	POST /sim/webapp/launch     {chat_id, user_id, username, first_name, url, start_param, color_scheme, platform, version}
//	GET  /sim/webapp/theme/{light|dark}
//	POST /sim/fault             {method, code, description, retry_after, times} or {method, clear: true}
//	DELETE /sim/fault
//	POST /sim/reset
func (s *Server) registerSim(mux *http.ServeMux) {
	mux.HandleFunc("POST /sim/message", s.simMessage)
	mux.HandleFunc("POST /sim/callback", s.simCallback)
	mux.HandleFunc("GET /sim/outbox", s.simOutbox)
	mux.HandleFunc("GET /sim/outbox/count", s.simOutboxCount)
	mux.HandleFunc("GET /sim/chat/{id}", s.simChat)
	mux.HandleFunc("GET /sim/file/{id}", s.simFile)
	mux.HandleFunc("GET /sim/chats", s.simChats)
	mux.HandleFunc("GET /sim/state", s.simState)
	mux.HandleFunc("POST /sim/webapp/launch", s.simWebAppLaunch)
	mux.HandleFunc("GET /sim/webapp/theme/{scheme}", s.simWebAppTheme)
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

// simFile serves the bytes the bot uploaded under a file_id, so the page can
// show a photo or download a document.
func (s *Server) simFile(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	f := s.files[r.PathValue("id")]
	s.mu.Unlock()
	if f == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such file"})
		return
	}
	w.Header().Set("Content-Type", f.mimeType)
	_, _ = w.Write(f.data)
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
		"menu_button":     s.MenuButton(0),
		"faults":          s.Faults(),
		"calls":           len(s.Calls("")),
	})
}

// simWebAppLaunch opens a Mini App the way the person would: from a web_app
// button (url) or from the chat's menu button (no url). The answer is the
// address the client loads and the signed launch data in it.
func (s *Server) simWebAppLaunch(w http.ResponseWriter, r *http.Request) {
	var in WebAppLaunch
	if !readJSON(w, r, &in) {
		return
	}
	launched, err := s.LaunchWebApp(in)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, launched)
}

// simWebAppTheme serves the theme parameters of a Telegram theme, what the
// page sends an open Mini App in theme_changed when the person switches it.
func (s *Server) simWebAppTheme(w http.ResponseWriter, r *http.Request) {
	scheme := r.PathValue("scheme")
	if scheme != "light" && scheme != "dark" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "scheme is light or dark"})
		return
	}
	writeJSON(w, http.StatusOK, ThemeParams(scheme))
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
