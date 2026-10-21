// Package mcptest serves MCP servers for tests, over every transport Coddy
// connects to: stdio (the test binary re-executed as the server's command,
// see Stdio and Main, or a local npm package started through npx, see
// NPX), streamable HTTP answering with JSON bodies (NewHTTPServer) or with
// event streams (NewEventStreamServer), and the legacy HTTP+SSE transport
// (NewSSEServer). Each offers Tool, whose call answers with the server's
// token, so a test can tell which server a call reached and that it came
// back; all but the npx one also offer ScreenshotTool, which answers with
// the token and a picture, the way a browser automation server returns a
// screenshot.
//
// A test package that runs a stdio server defines the helper test the
// command re-executes:
//
//	func TestHelperMCPTestServer(t *testing.T) { mcptest.Main() }
//
// Main returns at once in an ordinary test run, where the token variable is
// not set.
package mcptest

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/png"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// Tool is the tool every server of this package offers.
const Tool = "get_token"

// ScreenshotTool answers with the server's token as a text block and a PNG as
// an image block: Picture(token), or LargePicture(token) when the call's
// arguments are {"large": true}; {"count": n} returns Pictures(token, n)
// instead. The npx server does not offer it.
const ScreenshotTool = "take_screenshot"

// Picture is the PNG a call of ScreenshotTool returns: 16x12 pixels of one
// colour taken from the token, so the servers of a test return different
// pictures.
func Picture(token string) []byte {
	sum := tokenSeed(token)
	img := image.NewNRGBA(image.Rect(0, 0, 16, 12))
	fill := color.NRGBA{R: uint8(sum), G: uint8(sum >> 8), B: uint8(sum >> 16), A: 255}
	for y := 0; y < 12; y++ {
		for x := 0; x < 16; x++ {
			img.SetNRGBA(x, y, fill)
		}
	}
	return encodePNG(img)
}

// Pictures are the n PNGs a call of ScreenshotTool with {"count": n}
// returns: Picture(token) first, then a picture of another colour each.
func Pictures(token string, n int) [][]byte {
	out := make([][]byte, 0, n)
	for i := 1; i <= n; i++ {
		if i == 1 {
			out = append(out, Picture(token))
			continue
		}
		out = append(out, Picture(fmt.Sprintf("%s#%d", token, i)))
	}
	return out
}

var largePictures sync.Map // token -> []byte

// LargePicture is the PNG a call of ScreenshotTool with {"large": true}
// returns: 1280x900 pixels of noise seeded by the token, about 3.3 MiB, under
// the 3.75 MiB a model takes and over 4 MiB once base64 encoded - more than
// one line of an event stream used to carry.
func LargePicture(token string) []byte {
	if data, ok := largePictures.Load(token); ok {
		return data.([]byte)
	}
	img := image.NewNRGBA(image.Rect(0, 0, 1280, 900))
	r := rand.New(rand.NewSource(int64(tokenSeed(token))))
	for i := 0; i < len(img.Pix); i += 4 {
		v := r.Uint32()
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = uint8(v), uint8(v>>8), uint8(v>>16), 255
	}
	data, _ := largePictures.LoadOrStore(token, encodePNG(img))
	return data.([]byte)
}

func tokenSeed(token string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(token))
	return h.Sum32()
}

func encodePNG(img image.Image) []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// TokenEnv carries the token of a stdio server into the re-executed test
// binary; Main serves only when it is set.
const TokenEnv = "CODDY_MCPTEST_TOKEN"

// HelperTest is the name of the test a stdio server's command runs.
const HelperTest = "TestHelperMCPTestServer"

// Stdio declares a stdio server named name that answers Tool with token: the
// current test binary, re-executed to run HelperTest, which calls Main.
func Stdio(name, token string) config.MCPServerConfig {
	return config.MCPServerConfig{
		Type:    "stdio",
		Name:    name,
		Command: os.Args[0],
		Args:    []string{"-test.run=^" + HelperTest + "$"},
		Env:     []config.EnvVarConfig{{Name: TokenEnv, Value: token}},
	}
}

// Main serves a stdio MCP server on the process's stdin and stdout and exits
// when stdin closes, if TokenEnv is set; otherwise it returns at once.
func Main() {
	token := os.Getenv(TokenEnv)
	if token == "" {
		return
	}
	_ = ServeStdio(os.Stdin, os.Stdout, token)
	os.Exit(0)
}

// ServeStdio answers newline-delimited JSON-RPC requests read from in until
// it closes.
func ServeStdio(in io.Reader, out io.Writer, token string) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	encoder := json.NewEncoder(out)
	for scanner.Scan() {
		msg, ok := answer(scanner.Bytes(), token, nil)
		if !ok {
			continue
		}
		if err := encoder.Encode(msg); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// npxServer is the bin of the package NPX writes: the same server as
// ServeStdio, in Node, answering with the token in TokenEnv.
const npxServer = `#!/usr/bin/env node
const readline = require('readline');
const token = process.env.` + TokenEnv + ` || '';
readline.createInterface({ input: process.stdin }).on('line', (line) => {
  let req;
  try { req = JSON.parse(line); } catch (e) { return; }
  if (req.id === undefined || req.id === null) return;
  let result = {};
  if (req.method === 'initialize') {
    result = { protocolVersion: '2024-11-05', capabilities: { tools: {} }, serverInfo: { name: 'mcptest-npx', version: '1' } };
  } else if (req.method === 'tools/list') {
    result = { tools: [{ name: '` + Tool + `', description: "Answer with this server's token", inputSchema: { type: 'object' } }] };
  } else if (req.method === 'tools/call') {
    result = { content: [{ type: 'text', text: token }] };
  }
  process.stdout.write(JSON.stringify({ jsonrpc: '2.0', id: req.id, result }) + '\n');
});
`

// NPX declares a stdio server named name that npx starts from a local npm
// package written under dir, and that answers Tool with token. The command
// is `npx -y <package folder>`: npx installs the folder into its cache and
// runs its bin, the way it runs a package from the registry, without
// contacting one. npx gets a cache of its own under dir, so the user's npm
// cache is neither read nor written.
func NPX(name, token, dir string) (config.MCPServerConfig, error) {
	pkg := filepath.Join(dir, "package")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		return config.MCPServerConfig{}, err
	}
	manifest := `{"name":"coddy-mcptest-npx","version":"1.0.0","bin":{"coddy-mcptest-npx":"server.js"}}` + "\n"
	if err := os.WriteFile(filepath.Join(pkg, "package.json"), []byte(manifest), 0o644); err != nil {
		return config.MCPServerConfig{}, err
	}
	if err := os.WriteFile(filepath.Join(pkg, "server.js"), []byte(npxServer), 0o755); err != nil {
		return config.MCPServerConfig{}, err
	}
	return config.MCPServerConfig{
		Type:    "stdio",
		Name:    name,
		Command: "npx",
		Args:    []string{"-y", pkg},
		Env: []config.EnvVarConfig{
			{Name: TokenEnv, Value: token},
			{Name: "npm_config_cache", Value: filepath.Join(dir, "npm-cache")},
			{Name: "npm_config_update_notifier", Value: "false"},
			{Name: "npm_config_fund", Value: "false"},
			{Name: "npm_config_audit", Value: "false"},
		},
	}, nil
}

// request is the part of a JSON-RPC message the servers read.
type request struct {
	ID     any    `json:"id"`
	Method string `json:"method"`
	Params struct {
		Name      string `json:"name"`
		Arguments struct {
			Large bool `json:"large"`
			Count int  `json:"count"`
		} `json:"arguments"`
	} `json:"params"`
}

// answer is the response to one JSON-RPC message, and false for a
// notification or a message that is not JSON. called is counted on a call
// of Tool.
func answer(line []byte, token string, called *atomic.Int64) (map[string]any, bool) {
	var req request
	if err := json.Unmarshal(line, &req); err != nil || req.ID == nil {
		return nil, false
	}
	var result any = map[string]any{}
	switch req.Method {
	case "initialize":
		result = map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "mcptest", "version": "1"},
		}
	case "tools/list":
		result = map[string]any{"tools": []map[string]any{{
			"name":        Tool,
			"description": "Answer with this server's token",
			"inputSchema": map[string]any{"type": "object"},
		}, {
			"name":        ScreenshotTool,
			"description": "Answer with this server's token and a screenshot",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"large": map[string]any{"type": "boolean"}, "count": map[string]any{"type": "integer"}}},
		}}}
	case "tools/call":
		if called != nil {
			called.Add(1)
		}
		content := []map[string]any{{"type": "text", "text": token}}
		if req.Params.Name == ScreenshotTool {
			pictures := [][]byte{Picture(token)}
			switch {
			case req.Params.Arguments.Count > 1:
				pictures = Pictures(token, req.Params.Arguments.Count)
			case req.Params.Arguments.Large:
				pictures = [][]byte{LargePicture(token)}
			}
			for _, picture := range pictures {
				content = append(content, map[string]any{"type": "image", "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(picture)})
			}
		}
		result = map[string]any{"content": content}
	}
	return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}, true
}

// Server is a remote MCP server on a local port.
type Server struct {
	*httptest.Server
	// Token is what a call of Tool answers.
	Token string
	// URL is the address to configure: the POST endpoint for streamable
	// HTTP, the event stream for SSE.
	URL   string
	calls atomic.Int64
}

// Calls reports how many times Tool was called.
func (s *Server) Calls() int64 { return s.calls.Load() }

// Close shuts the server down. The connections clients still hold - an SSE
// client keeps its event stream open for as long as it lives - are closed
// first, since httptest waits for every request to finish.
func (s *Server) Close() {
	s.CloseClientConnections()
	s.Server.Close()
}

// NewHTTPServer serves the streamable HTTP transport at URL: every JSON-RPC
// message is POSTed there and answered with an application/json body.
func NewHTTPServer(token string) *Server {
	s := &Server{Token: token}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		msg, ok := answer(body, s.Token, &s.calls)
		if !ok {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(msg)
	}))
	s.URL = s.Server.URL + "/mcp"
	return s
}

// NewEventStreamServer serves the streamable HTTP transport at URL the way
// the TypeScript SDK does by default: every request POSTed there is answered
// with a text/event-stream body carrying the response as one event, on one
// data line however long it is.
func NewEventStreamServer(token string) *Server {
	s := &Server{Token: token}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		msg, ok := answer(body, s.Token, &s.calls)
		if !ok {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		data, _ := json.Marshal(msg)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
	}))
	s.URL = s.Server.URL + "/mcp"
	return s
}

// NewSSEServer serves the legacy HTTP+SSE transport: a GET of URL opens an
// event stream that first names the endpoint to POST messages to, then
// carries every response; the POSTs are answered 202.
func NewSSEServer(token string) *Server {
	s := &Server{Token: token}
	var (
		mu       sync.Mutex
		next     int
		sessions = map[string]chan []byte{}
	)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /sse", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		out := make(chan []byte, 16)
		mu.Lock()
		next++
		id := strconv.Itoa(next)
		sessions[id] = out
		mu.Unlock()
		defer func() {
			mu.Lock()
			delete(sessions, id)
			mu.Unlock()
		}()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "event: endpoint\ndata: /messages?session=%s\n\n", id)
		flusher.Flush()
		for {
			select {
			case msg := <-out:
				_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", msg)
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	})
	mux.HandleFunc("POST /messages", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		out := sessions[r.URL.Query().Get("session")]
		mu.Unlock()
		if out == nil {
			http.Error(w, "unknown session", http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		msg, ok := answer(body, s.Token, &s.calls)
		if !ok {
			return
		}
		data, _ := json.Marshal(msg)
		select {
		case out <- data:
		case <-r.Context().Done():
		}
	})
	s.Server = httptest.NewServer(mux)
	s.URL = s.Server.URL + "/sse"
	return s
}
