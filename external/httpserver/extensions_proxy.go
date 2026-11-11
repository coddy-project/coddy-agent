//go:build http

package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/extensions"
	"golang.org/x/net/http/httpguts"
)

const extensionRequestLimit = 4 << 20
const extensionResponseLimit = 8 << 20
const extensionEnvelopeLimit = 6 << 20
const extensionRequestTimeout = 30 * time.Second

type extensionProxyRequest struct {
	SHA256  string            `json:"sha256"`
	URL     string            `json:"url"`
	Method  string            `json:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    []byte            `json:"body,omitempty"`
}

type extensionProxyResponse struct {
	Status     int                 `json:"status"`
	StatusText string              `json:"status_text"`
	Headers    map[string][]string `json:"headers"`
	Body       []byte              `json:"body"`
}

// Each route set owns its in-flight calls. Serializing admission with disable
// and removal closes the gap between checking approval and starting a request.
type extensionProxy struct {
	mu      sync.Mutex
	pending map[string]map[*http.Request]context.CancelFunc
	client  *http.Client
}

func newExtensionProxy() *extensionProxy {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// The target is explicit. Do not borrow provider proxies, environment
	// proxies, browser cookies or the API request's authentication headers.
	transport.Proxy = nil
	transport.MaxIdleConns = 16
	transport.MaxIdleConnsPerHost = 2
	transport.IdleConnTimeout = 30 * time.Second
	transport.MaxResponseHeaderBytes = 64 << 10
	return &extensionProxy{pending: make(map[string]map[*http.Request]context.CancelFunc), client: &http.Client{
		Transport: transport,
		Timeout:   extensionRequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

func extensionProxyHeader(name string) bool {
	switch strings.ToLower(name) {
	case "host", "cookie", "cookie2", "set-cookie", "set-cookie2", "connection", "proxy-connection", "proxy-authorization", "proxy-authenticate", "keep-alive", "te", "trailer", "transfer-encoding", "upgrade", "content-length", "origin", "referer":
		return false
	}
	return !strings.HasPrefix(strings.ToLower(name), "sec-") && !strings.HasPrefix(strings.ToLower(name), "x-forwarded-") && !strings.EqualFold(name, "forwarded")
}

func (p *extensionProxy) mutate(key string, change func() error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := change(); err != nil {
		return err
	}
	for _, cancel := range p.pending[key] {
		cancel()
	}
	return nil
}

func (p *extensionProxy) serve(w http.ResponseWriter, r *http.Request, store *extensions.Store, key string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mediaType != "application/json" {
		http.Error(w, errorJSON("expected application/json"), http.StatusUnsupportedMediaType)
		return
	}
	var body extensionProxyRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, extensionEnvelopeLimit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		var tooLarge *http.MaxBytesError
		code := http.StatusBadRequest
		if errors.As(err, &tooLarge) {
			code = http.StatusRequestEntityTooLarge
		}
		http.Error(w, errorJSON("expected a proxy request with a base64 body, up to 6 MiB JSON"), code)
		return
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		http.Error(w, errorJSON("expected one JSON object"), http.StatusBadRequest)
		return
	}
	if len(body.Body) > extensionRequestLimit {
		http.Error(w, errorJSON("request body exceeds 4 MiB"), http.StatusRequestEntityTooLarge)
		return
	}
	u, err := url.Parse(body.URL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" || len(body.URL) > 8192 {
		http.Error(w, errorJSON("expected an absolute HTTP(S) URL without credentials or fragment"), http.StatusBadRequest)
		return
	}
	if body.Method == "" {
		body.Method = http.MethodGet
	}
	switch body.Method {
	case http.MethodGet, http.MethodHead:
		if len(body.Body) != 0 {
			http.Error(w, errorJSON("GET and HEAD cannot carry a body"), http.StatusBadRequest)
			return
		}
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions:
	default:
		http.Error(w, errorJSON("unsupported HTTP method"), http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), extensionRequestTimeout)
	defer cancel()
	upstream, err := http.NewRequestWithContext(ctx, body.Method, u.String(), bytes.NewReader(body.Body))
	if err != nil {
		http.Error(w, errorJSON("invalid HTTP request"), http.StatusBadRequest)
		return
	}
	headerSize := 0
	for name, value := range body.Headers {
		headerSize += len(name) + len(value)
		if !extensionProxyHeader(name) || !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(value) || headerSize > 32<<10 {
			http.Error(w, errorJSON("invalid or forbidden proxy header, or headers exceed 32 KiB"), http.StatusBadRequest)
			return
		}
		upstream.Header.Set(name, value)
	}
	p.mu.Lock()
	err = store.CheckEnabled(r.PathValue("id"), body.SHA256)
	if err == nil {
		if p.pending[key] == nil {
			p.pending[key] = make(map[*http.Request]context.CancelFunc)
		}
		p.pending[key][r] = cancel
	}
	p.mu.Unlock()
	if err != nil {
		http.Error(w, errorJSON("extension is unavailable, disabled or its digest changed"), http.StatusConflict)
		return
	}
	defer func() {
		p.mu.Lock()
		delete(p.pending[key], r)
		if len(p.pending[key]) == 0 {
			delete(p.pending, key)
		}
		p.mu.Unlock()
	}()
	response, err := p.client.Do(upstream)
	if err != nil {
		p.fail(w, ctx, "upstream request failed")
		return
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode > 599 {
		http.Error(w, errorJSON("unsupported upstream response status"), http.StatusBadGateway)
		return
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, extensionResponseLimit+1))
	if err != nil {
		p.fail(w, ctx, "upstream response could not be read")
		return
	}
	if len(data) > extensionResponseLimit {
		http.Error(w, errorJSON("upstream response exceeds 8 MiB"), http.StatusBadGateway)
		return
	}
	// Recheck after the read: another Coddy process may share the store. Already
	// dispatched side effects cannot be undone by revoking a package.
	if err := store.CheckEnabled(r.PathValue("id"), body.SHA256); err != nil {
		http.Error(w, errorJSON("extension approval was revoked"), http.StatusConflict)
		return
	}
	headers := make(map[string][]string)
	for _, name := range strings.Split(response.Header.Get("Connection"), ",") {
		response.Header.Del(strings.TrimSpace(name))
	}
	for name, values := range response.Header {
		if extensionProxyHeader(name) {
			headers[name] = values
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(extensionProxyResponse{Status: response.StatusCode, StatusText: http.StatusText(response.StatusCode), Headers: headers, Body: data})
}

func (p *extensionProxy) fail(w http.ResponseWriter, ctx context.Context, message string) {
	code := http.StatusBadGateway
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		code = http.StatusGatewayTimeout
	} else if ctx.Err() != nil {
		code = http.StatusConflict
	}
	http.Error(w, errorJSON(fmt.Sprintf("%s (request cancelled, timed out or target unavailable)", message)), code)
}
