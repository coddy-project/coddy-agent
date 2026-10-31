//go:build http

package httpserver

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"regexp"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/extensions"
)

// Event types an extension publishes share the contribution id shape, so a
// listener addresses one as ext.<id>.<type>.
var extensionEventTypePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// extensionRequest is one in-flight host request: the frame carries its nonce
// so only the holder of the published event can answer it (the endpoints on
// loopback are unauthenticated, as the IDE bridge's are).
type extensionRequest struct {
	owner string
	nonce string
	sent  time.Time
	ch    chan json.RawMessage
}

type extensionRequestBook struct {
	mu   sync.Mutex
	rows map[string]*extensionRequest
}

func (b *extensionRequestBook) add(row *extensionRequest) (string, string, error) {
	id := make([]byte, 16)
	nonce := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return "", "", err
	}
	if _, err := rand.Read(nonce); err != nil {
		return "", "", err
	}
	key, secret := hex.EncodeToString(id), hex.EncodeToString(nonce)
	row.nonce = secret
	b.mu.Lock()
	b.rows[key] = row
	b.mu.Unlock()
	return key, secret, nil
}

func (b *extensionRequestBook) drop(key string) {
	b.mu.Lock()
	delete(b.rows, key)
	b.mu.Unlock()
}

func (b *extensionRequestBook) get(key string) *extensionRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.rows[key]
}

func (s *Server) registerExtensionRoutes() {
	proxy := newExtensionProxy()
	requests := &extensionRequestBook{rows: map[string]*extensionRequest{}}
	store := func() *extensions.Store { return extensions.NewStore(s.activeCfg().Paths.Home) }
	proxyKey := func(id string) string { return s.activeCfg().Paths.Home + "\x00" + id }
	reply := func(w http.ResponseWriter, code int, value any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(value)
	}
	fail := func(w http.ResponseWriter, err error) {
		code := http.StatusInternalServerError
		switch {
		case errors.Is(err, extensions.ErrInvalid):
			code = http.StatusBadRequest
		case errors.Is(err, extensions.ErrConflict), errors.Is(err, extensions.ErrUnavailable):
			code = http.StatusConflict
		case errors.Is(err, os.ErrNotExist):
			code = http.StatusNotFound
		}
		http.Error(w, errorJSON(err.Error()), code)
	}
	s.mux.HandleFunc("GET /coddy/extensions", func(w http.ResponseWriter, r *http.Request) {
		rows, err := store().List()
		if err != nil {
			fail(w, err)
			return
		}
		reply(w, 200, map[string]any{"items": rows})
	})
	s.mux.HandleFunc("POST /coddy/extensions", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, extensions.MaxArchive)
		data, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, errorJSON("ZIP exceeds 8 MiB or cannot be read"), http.StatusRequestEntityTooLarge)
			return
		}
		row, err := store().Install(data)
		if err != nil {
			fail(w, err)
			return
		}
		reply(w, http.StatusCreated, row)
	})
	s.mux.HandleFunc("PUT /coddy/extensions/{id}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			SHA256     string `json:"sha256"`
			Enabled    *bool  `json:"enabled"`
			AcceptRisk bool   `json:"accept_risk"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			http.Error(w, errorJSON("expected sha256, enabled and accept_risk"), 400)
			return
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			http.Error(w, errorJSON("expected one JSON object"), 400)
			return
		}
		if body.Enabled == nil || body.SHA256 == "" {
			http.Error(w, errorJSON("sha256 and enabled are required"), http.StatusBadRequest)
			return
		}
		if *body.Enabled && !body.AcceptRisk {
			http.Error(w, errorJSON("enabling JavaScript requires accept_risk: true"), 400)
			return
		}
		var row extensions.Entry
		err := proxy.mutate(proxyKey(r.PathValue("id")), func() error {
			var err error
			row, err = store().SetEnabled(r.PathValue("id"), body.SHA256, *body.Enabled)
			return err
		})
		if err != nil {
			fail(w, err)
			return
		}
		reply(w, 200, row)
	})
	s.mux.HandleFunc("DELETE /coddy/extensions/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := proxy.mutate(proxyKey(r.PathValue("id")), func() error {
			return store().Remove(r.PathValue("id"))
		}); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	s.mux.HandleFunc("POST /coddy/extensions/{id}/fetch", func(w http.ResponseWriter, r *http.Request) {
		proxy.serve(w, r, store(), proxyKey(r.PathValue("id")))
	})
	s.mux.HandleFunc("POST /coddy/extensions/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		// An extension publishes one host event: `extension_event` on the
		// shared stream, for hook handlers and an IDE bridge to answer.
		id := r.PathValue("id")
		var body struct {
			SHA256 string          `json:"sha256"`
			Type   string          `json:"type"`
			Data   json.RawMessage `json:"data"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			http.Error(w, errorJSON("expected sha256, type and data"), http.StatusBadRequest)
			return
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			http.Error(w, errorJSON("expected one JSON object"), http.StatusBadRequest)
			return
		}
		if err := store().CheckEnabled(id, body.SHA256); err != nil {
			fail(w, err)
			return
		}
		if !extensionEventTypePattern.MatchString(body.Type) {
			http.Error(w, errorJSON("type must match ^[a-z][a-z0-9-]{0,63}$"), http.StatusBadRequest)
			return
		}
		data := body.Data
		if len(data) == 0 {
			data = json.RawMessage("null")
		}
		var probe any
		if err := json.Unmarshal(data, &probe); err != nil {
			http.Error(w, errorJSON("data must be one JSON value"), http.StatusBadRequest)
			return
		}
		payload, err := json.Marshal(map[string]any{
			"object":    "coddy.extension_event",
			"extension": id,
			"type":      body.Type,
			"data":      probe,
		})
		if err != nil {
			fail(w, err)
			return
		}
		if s.events != nil {
			frame := make([]byte, 0, len(payload)+32)
			frame = append(frame, "event: extension_event\ndata: "...)
			frame = append(frame, payload...)
			frame = append(frame, "\n\n"...)
			s.events.publish(frame)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	s.mux.HandleFunc("POST /coddy/extensions/{id}/request", func(w http.ResponseWriter, r *http.Request) {
		// One host request: `extension_request` on the shared stream with a
		// nonce, and the answer of whoever handles it - a hook handler or an
		// IDE bridge - resolves the wait. A timeout answers with nothing.
		id := r.PathValue("id")
		var body struct {
			SHA256    string          `json:"sha256"`
			Type      string          `json:"type"`
			Data      json.RawMessage `json:"data"`
			TimeoutMS int             `json:"timeout_ms"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			http.Error(w, errorJSON("expected sha256, type and optional data and timeout_ms"), http.StatusBadRequest)
			return
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			http.Error(w, errorJSON("expected one JSON object"), http.StatusBadRequest)
			return
		}
		if err := store().CheckEnabled(id, body.SHA256); err != nil {
			fail(w, err)
			return
		}
		if !extensionEventTypePattern.MatchString(body.Type) {
			http.Error(w, errorJSON("type must match ^[a-z][a-z0-9-]{0,63}$"), http.StatusBadRequest)
			return
		}
		data := body.Data
		if len(data) == 0 {
			data = json.RawMessage("null")
		}
		var probe any
		if err := json.Unmarshal(data, &probe); err != nil {
			http.Error(w, errorJSON("data must be one JSON value"), http.StatusBadRequest)
			return
		}
		timeout := 30 * time.Second
		if body.TimeoutMS != 0 {
			if body.TimeoutMS < 1 || body.TimeoutMS > 60000 {
				http.Error(w, errorJSON("timeout_ms must be between 1 and 60000"), http.StatusBadRequest)
				return
			}
			timeout = time.Duration(body.TimeoutMS) * time.Millisecond
		}
		row := &extensionRequest{
			owner: id,
			sent:  time.Now(),
			ch:    make(chan json.RawMessage, 1),
		}
		key, nonce, err := requests.add(row)
		if err != nil {
			fail(w, err)
			return
		}
		defer requests.drop(key)
		payload, err := json.Marshal(map[string]any{
			"object":     "coddy.extension_request",
			"extension":  id,
			"request_id": key,
			"nonce":      nonce,
			"type":       body.Type,
			"data":       probe,
		})
		if err != nil {
			fail(w, err)
			return
		}
		if s.events != nil {
			frame := make([]byte, 0, len(payload)+32)
			frame = append(frame, "event: extension_request\ndata: "...)
			frame = append(frame, payload...)
			frame = append(frame, "\n\n"...)
			s.events.publish(frame)
		}
		select {
		case answer := <-row.ch:
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(append(append([]byte(`{"request_id":"`+key+`","answered":true,"data":`), answer...), '}'))
		case <-time.After(timeout):
			reply(w, 200, map[string]any{"request_id": key, "answered": false})
		case <-r.Context().Done():
		}
	})
	s.mux.HandleFunc("POST /coddy/extensions/{id}/requests/{requestId}", func(w http.ResponseWriter, r *http.Request) {
		// The answer to one published request. The nonce rides in the frame,
		// so a caller that did not see the event cannot complete the request.
		id, key := r.PathValue("id"), r.PathValue("requestId")
		var body struct {
			Nonce string          `json:"nonce"`
			Data  json.RawMessage `json:"data"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			http.Error(w, errorJSON("expected nonce and data"), http.StatusBadRequest)
			return
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			http.Error(w, errorJSON("expected one JSON object"), http.StatusBadRequest)
			return
		}
		row := requests.get(key)
		if row == nil || row.owner != id {
			http.Error(w, errorJSON("no such request"), http.StatusNotFound)
			return
		}
		if body.Nonce != row.nonce {
			http.Error(w, errorJSON("wrong nonce"), http.StatusForbidden)
			return
		}
		data := body.Data
		if len(data) == 0 {
			data = json.RawMessage("null")
		}
		var probe any
		if err := json.Unmarshal(data, &probe); err != nil {
			http.Error(w, errorJSON("data must be one JSON value"), http.StatusBadRequest)
			return
		}
		select {
		case row.ch <- data:
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, errorJSON("request already answered"), http.StatusConflict)
		}
	})
	s.mux.HandleFunc("GET /coddy/extensions/{id}/assets/{file...}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("file")
		data, err := store().Asset(r.PathValue("id"), r.URL.Query().Get("sha256"), name)
		if err != nil {
			fail(w, err)
			return
		}
		contentType := mime.TypeByExtension(path.Ext(name))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		// Assets are fetched as bytes by the SDK, never navigated to as an active
		// document. Installation and enabling remain separate operator actions.
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", "attachment")
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(data) //nolint:gosec // G705: enabled bundle bytes are attachments with sandbox CSP and nosniff; only the approved UI loader executes them.
	})
}
