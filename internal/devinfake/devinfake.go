// Package devinfake is an offline stand-in for the Devin API a devin provider
// talks to: the sign-in page and code exchange of `devin auth login`, the
// GetUserJwt handshake, the GetCliModelConfigs catalog and the GetChatMessage
// stream. Chat answers are scripted by the test. It carries its own protobuf
// codec on purpose: sharing the client's would let a symmetric encoding bug
// pass both ends unnoticed.
package devinfake

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Model is one catalog variant.
type Model struct {
	UID           string
	Label         string
	Family        string
	FamilyLabel   string
	Default       bool
	ContextWindow int
	MaxOutput     int
	Images        bool
}

// ToolCall is a scripted or received tool call.
type ToolCall struct {
	ID, Name, Args string
}

// Turn is one scripted chat answer. Text, Thinking and the tool call
// arguments are streamed in pieces, the way the real server does it.
type Turn struct {
	Thinking      string
	Signature     string
	SignatureType string
	Text          string
	ToolCalls     []ToolCall
	StopReason    int
	Input, Output int
	CacheRead     int
	// ErrorCode, when set, ends the stream with a Connect error trailer.
	ErrorCode, ErrorMessage string
}

// Prompt is one received ChatMessagePrompt.
type Prompt struct {
	Source        int
	Text          string
	ToolCalls     []ToolCall
	ToolCallID    string
	Thinking      string
	Signature     string
	SignatureType string
	Images        int
}

// ChatRequest is a received GetChatMessage request, decoded.
type ChatRequest struct {
	IDE       string
	APIKey    string
	JWT       string
	System    string
	Prompts   []Prompt
	Tools     []string
	ModelUID  string
	MaxTokens int
}

// Options configure a stand.
type Options struct {
	// SessionToken is the token the stand accepts (and issues on sign-in).
	SessionToken string
	// Email is the account the minted JWT names.
	Email  string
	Models []Model
	// Reply answers a chat request; nil repeats a plain "ok".
	Reply func(req ChatRequest, n int) Turn
}

// Server is the stand. Its handler serves every host the provider talks to:
// the web app (/auth/cli/continue), the Devin API (/auth/cli/token) and the
// API server (the Connect methods).
type Server struct {
	opts Options

	mu         sync.Mutex
	challenges map[string]string // code -> PKCE challenge
	chats      []ChatRequest
	jwtMints   int
	catalogIDE []string
	exchanges  int
	issuedJWT  string
}

// New builds a stand.
func New(opts Options) *Server {
	if opts.SessionToken == "" {
		opts.SessionToken = "devin-session-token$stand-token"
	}
	if opts.Email == "" {
		opts.Email = "dev@example.com"
	}
	return &Server{opts: opts, challenges: map[string]string{}}
}

// Chats returns the chat requests received so far.
func (s *Server) Chats() []ChatRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ChatRequest(nil), s.chats...)
}

// JWTMints counts GetUserJwt calls.
func (s *Server) JWTMints() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jwtMints
}

// CatalogIDEs lists the IDE names catalog calls presented.
func (s *Server) CatalogIDEs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.catalogIDE...)
}

// Exchanges counts code exchanges.
func (s *Server) Exchanges() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exchanges
}

// ServeHTTP routes a request.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/auth/cli/continue":
		s.serveContinue(w, r)
	case "/auth/cli/token":
		s.serveToken(w, r)
	case "/exa.auth_pb.AuthService/GetUserJwt":
		s.serveJWT(w, r)
	case "/exa.api_server_pb.ApiServerService/GetCliModelConfigs":
		s.serveCatalog(w, r)
	case "/exa.api_server_pb.ApiServerService/GetChatMessage":
		s.serveChat(w, r)
	default:
		http.NotFound(w, r)
	}
}

// serveContinue plays the sign-in page with the user already signed in: it
// records the PKCE challenge against a fresh code and sends the browser back
// to the redirect_uri with the code and the state.
func (s *Server) serveContinue(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirect, state, challenge := q.Get("redirect_uri"), q.Get("state"), q.Get("code_challenge")
	if redirect == "" || state == "" || challenge == "" || q.Get("code_challenge_method") != "S256" {
		http.Error(w, "missing sign-in parameters", http.StatusBadRequest)
		return
	}
	code := fmt.Sprintf("code-%d", time.Now().UnixNano())
	s.mu.Lock()
	s.challenges[code] = challenge
	s.mu.Unlock()
	u, err := url.Parse(redirect)
	if err != nil {
		http.Error(w, "bad redirect_uri", http.StatusBadRequest)
		return
	}
	back := u.Query()
	back.Set("code", code)
	back.Set("state", state)
	u.RawQuery = back.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// serveToken exchanges a code for the session token, checking the PKCE
// verifier against the challenge the sign-in page recorded.
func (s *Server) serveToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code     string `json:"code"`
		Verifier string `json:"code_verifier"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	challenge, ok := s.challenges[body.Code]
	delete(s.challenges, body.Code)
	s.exchanges++
	s.mu.Unlock()
	sum := sha256.Sum256([]byte(body.Verifier))
	if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"detail":"Invalid or expired code."}`)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	token := strings.TrimPrefix(s.opts.SessionToken, "devin-session-token$")
	_ = json.NewEncoder(w).Encode(map[string]string{"token": token})
}

func connectError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": msg})
}

// metadata reads field 1 of a request: the ide name, api key and jwt.
func metadata(body []byte) (ide, apiKey, jwt string) {
	for _, f := range fields(body) {
		if f.num != 1 || f.wire != 2 {
			continue
		}
		for _, m := range fields(f.raw) {
			switch m.num {
			case 1:
				ide = string(m.raw)
			case 3:
				apiKey = string(m.raw)
			case 21:
				jwt = string(m.raw)
			}
		}
	}
	return
}

func (s *Server) serveJWT(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	_, key, _ := metadata(body)
	if key != s.opts.SessionToken {
		connectError(w, http.StatusUnauthorized, "unauthenticated", "failed to validate Devin token: Invalid token")
		return
	}
	claims, _ := json.Marshal(map[string]any{"email": s.opts.Email, "name": "Stand User", "exp": time.Now().Add(15 * time.Minute).Unix()})
	jwt := "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(claims) + ".sig"
	s.mu.Lock()
	s.jwtMints++
	s.issuedJWT = jwt
	s.mu.Unlock()
	var out enc
	out.str(1, jwt)
	w.Header().Set("Content-Type", "application/proto")
	_, _ = w.Write(out.b)
}

func (s *Server) authorized(key, jwt string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return key == s.opts.SessionToken && jwt != "" && jwt == s.issuedJWT
}

func (s *Server) serveCatalog(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	ide, key, jwt := metadata(body)
	if !s.authorized(key, jwt) {
		connectError(w, http.StatusUnauthorized, "unauthenticated", "invalid credentials")
		return
	}
	s.mu.Lock()
	s.catalogIDE = append(s.catalogIDE, ide)
	s.mu.Unlock()
	var out enc
	if ide == "windsurf" {
		for _, m := range s.opts.Models {
			out.msg(1, encodeModel(m))
		}
	}
	w.Header().Set("Content-Type", "application/proto")
	_, _ = w.Write(out.b)
}

func encodeModel(m Model) []byte {
	var e enc
	e.str(1, m.Label)
	e.boolean(5, m.Images)
	e.uint(18, uint64(m.ContextWindow))
	e.str(22, m.UID)
	var info enc
	info.uint(4, uint64(m.ContextWindow))
	info.uint(13, uint64(m.MaxOutput))
	info.str(23, m.Family)
	e.msg(23, info.b)
	var fam enc
	fam.str(1, m.FamilyLabel)
	e.msg(30, fam.b)
	e.boolean(31, m.Default)
	return e.b
}

func (s *Server) serveChat(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Content-Type") != "application/connect+proto" {
		http.Error(w, "unsupported content type", http.StatusUnsupportedMediaType)
		return
	}
	raw, _ := io.ReadAll(r.Body)
	payload, err := unframe(raw)
	if err != nil {
		connectError(w, http.StatusBadRequest, "invalid_argument", err.Error())
		return
	}
	req := decodeChat(payload)
	if !s.authorized(req.APIKey, req.JWT) {
		connectError(w, http.StatusUnauthorized, "unauthenticated", "invalid credentials")
		return
	}
	s.mu.Lock()
	s.chats = append(s.chats, req)
	n := len(s.chats)
	s.mu.Unlock()
	turn := Turn{Text: "ok", StopReason: 2, Input: 10, Output: 1}
	if s.opts.Reply != nil {
		turn = s.opts.Reply(req, n)
	}
	w.Header().Set("Content-Type", "application/connect+proto")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	send := func(e enc) {
		_, _ = w.Write(frame(e.b, false))
		if flusher != nil {
			flusher.Flush()
		}
	}
	for _, piece := range pieces(turn.Thinking) {
		var e enc
		e.str(9, piece)
		send(e)
	}
	if turn.Signature != "" {
		var e enc
		e.str(10, turn.Signature)
		e.str(21, turn.SignatureType)
		send(e)
	}
	for _, piece := range pieces(turn.Text) {
		var e enc
		e.str(3, piece)
		send(e)
	}
	for _, tc := range turn.ToolCalls {
		var head, call enc
		call.str(1, tc.ID)
		call.str(2, tc.Name)
		head.msg(6, call.b)
		send(head)
		for _, piece := range pieces(tc.Args) {
			var e, frag enc
			frag.str(3, piece)
			e.msg(6, frag.b)
			send(e)
		}
	}
	if turn.StopReason != 0 {
		var e enc
		e.uint(5, uint64(turn.StopReason))
		send(e)
	}
	var usage, e enc
	usage.uint(2, uint64(turn.Input))
	usage.uint(3, uint64(turn.Output))
	usage.uint(5, uint64(turn.CacheRead))
	e.msg(7, usage.b)
	send(e)
	trailer := "{}"
	if turn.ErrorCode != "" {
		b, _ := json.Marshal(map[string]any{"error": map[string]string{"code": turn.ErrorCode, "message": turn.ErrorMessage}})
		trailer = string(b)
	}
	_, _ = w.Write(frame([]byte(trailer), true))
}

// pieces splits s into chunks of up to eight bytes on rune boundaries.
func pieces(s string) []string {
	var out []string
	for len(s) > 0 {
		n := len(s)
		if n > 8 {
			n = 8
			for n > 1 && !utf8.RuneStart(s[n]) {
				n--
			}
		}
		out = append(out, s[:n])
		s = s[n:]
	}
	return out
}

func decodeChat(payload []byte) ChatRequest {
	var req ChatRequest
	req.IDE, req.APIKey, req.JWT = metadata(payload)
	for _, f := range fields(payload) {
		switch {
		case f.num == 2 && f.wire == 2:
			req.System = string(f.raw)
		case f.num == 3 && f.wire == 2:
			req.Prompts = append(req.Prompts, decodePrompt(f.raw))
		case f.num == 8 && f.wire == 2:
			for _, c := range fields(f.raw) {
				if c.num == 2 && c.wire == 0 {
					req.MaxTokens = int(c.v)
				}
			}
		case f.num == 10 && f.wire == 2:
			for _, t := range fields(f.raw) {
				if t.num == 1 {
					req.Tools = append(req.Tools, string(t.raw))
				}
			}
		case f.num == 21 && f.wire == 2:
			req.ModelUID = string(f.raw)
		}
	}
	return req
}

func decodePrompt(raw []byte) Prompt {
	var p Prompt
	for _, f := range fields(raw) {
		switch f.num {
		case 2:
			p.Source = int(f.v)
		case 3:
			p.Text = string(f.raw)
		case 6:
			var tc ToolCall
			for _, t := range fields(f.raw) {
				switch t.num {
				case 1:
					tc.ID = string(t.raw)
				case 2:
					tc.Name = string(t.raw)
				case 3:
					tc.Args = string(t.raw)
				}
			}
			p.ToolCalls = append(p.ToolCalls, tc)
		case 7:
			p.ToolCallID = string(f.raw)
		case 10:
			p.Images++
		case 11:
			p.Thinking = string(f.raw)
		case 12:
			p.Signature = string(f.raw)
		case 18:
			p.SignatureType = string(f.raw)
		}
	}
	return p
}

// The stand's own protobuf codec.

type enc struct{ b []byte }

func (e *enc) key(num, wire int) { e.b = binary.AppendUvarint(e.b, uint64(num<<3|wire)) }

func (e *enc) str(num int, s string) {
	if s == "" {
		return
	}
	e.key(num, 2)
	e.b = binary.AppendUvarint(e.b, uint64(len(s)))
	e.b = append(e.b, s...)
}

func (e *enc) msg(num int, b []byte) {
	e.key(num, 2)
	e.b = binary.AppendUvarint(e.b, uint64(len(b)))
	e.b = append(e.b, b...)
}

func (e *enc) uint(num int, v uint64) {
	if v == 0 {
		return
	}
	e.key(num, 0)
	e.b = binary.AppendUvarint(e.b, v)
}

func (e *enc) boolean(num int, v bool) {
	if v {
		e.uint(num, 1)
	}
}

type field struct {
	num, wire int
	v         uint64
	raw       []byte
}

// fields decodes the top level of a message; a malformed tail is dropped.
func fields(b []byte) []field {
	var out []field
	for len(b) > 0 {
		k, n := binary.Uvarint(b)
		if n <= 0 {
			return out
		}
		b = b[n:]
		f := field{num: int(k >> 3), wire: int(k & 7)}
		switch f.wire {
		case 0:
			v, m := binary.Uvarint(b)
			if m <= 0 {
				return out
			}
			f.v, b = v, b[m:]
		case 1:
			if len(b) < 8 {
				return out
			}
			f.raw, b = b[:8], b[8:]
		case 5:
			if len(b) < 4 {
				return out
			}
			f.raw, b = b[:4], b[4:]
		case 2:
			l, m := binary.Uvarint(b)
			if m <= 0 || int(l) > len(b)-m {
				return out
			}
			f.raw, b = b[m:m+int(l)], b[m+int(l):]
		default:
			return out
		}
		out = append(out, f)
	}
	return out
}

// frame wraps a payload in a gzip-compressed Connect envelope.
func frame(payload []byte, end bool) []byte {
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(payload)
	_ = zw.Close()
	flags := byte(1)
	if end {
		flags |= 2
	}
	out := []byte{flags, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(out[1:], uint32(gz.Len()))
	return append(out, gz.Bytes()...)
}

// unframe reads the single request envelope of a server-streaming call.
func unframe(raw []byte) ([]byte, error) {
	if len(raw) < 5 {
		return nil, fmt.Errorf("short envelope")
	}
	n := binary.BigEndian.Uint32(raw[1:5])
	if int(n) != len(raw)-5 {
		return nil, fmt.Errorf("envelope length %d does not match body %d", n, len(raw)-5)
	}
	payload := raw[5:]
	if raw[0]&1 != 0 {
		zr, err := gzip.NewReader(bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		return io.ReadAll(zr)
	}
	return payload, nil
}
