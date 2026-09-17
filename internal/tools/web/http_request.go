package web

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html/charset"
	"golang.org/x/net/http/httpguts"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
	toolfs "github.com/EvilFreelancer/coddy-agent/internal/tools/fs"
)

// ToolHTTPRequest is the name the model calls the tool by.
const ToolHTTPRequest = "http_request"

const (
	defaultHTTPTimeout = 30 * time.Second
	maxHTTPTimeout     = 600 * time.Second
	// maxHTTPBodyDisplayBytes caps the body read into the answer. The output
	// limits cut what reaches the model far below this; reading more would only
	// hold memory for bytes nobody sees.
	maxHTTPBodyDisplayBytes = 1 << 20
	// maxHTTPDownloadBytes caps a body saved to output_file.
	maxHTTPDownloadBytes = 1 << 30
	// describeBodyChars caps the body preview in the permission prompt.
	describeBodyChars = 4000
)

// HTTPRequestTool returns the http_request built-in tool: a request the model
// shapes itself, answered the way curl -i prints it.
func HTTPRequestTool() *tooling.Tool {
	scalarNote := "A value is a string, a number, a boolean, or an array of those for a repeated name."
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolHTTPRequest,
			Description: "Send an HTTP or HTTPS request, like curl: any method, query parameters, any headers, and a body - raw text, base64 bytes, JSON, " +
				"an urlencoded form, multipart form data with file uploads, or a local file as the whole body. " +
				"Returns the status line, the response headers and the body, or saves the body to output_file. " +
				"A proxy and the TLS certificate check can be chosen per request. " +
				"Use it for APIs, uploads, downloads and local services; use webfetch to read an article as Markdown. " +
				"The operator is asked for permission unless the destination is allowed; the prompt shows the address, headers, body, files, proxy and certificate check.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"url": map[string]interface{}{
						"type":        "string",
						"description": "Absolute http:// or https:// URL. Query parameters may be written here, passed in query, or both.",
					},
					"method": map[string]interface{}{
						"type":        "string",
						"description": "HTTP method, any token: GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS or a custom one. Default GET, or POST when a body is given.",
					},
					"query": map[string]interface{}{
						"type":        "object",
						"description": "Query parameters appended to the URL. " + scalarNote,
					},
					"headers": map[string]interface{}{
						"type": "object",
						"description": "Request headers by name. They override every default, User-Agent, Content-Type and Host included. " +
							"An empty value removes a header the tool would otherwise send. Content-Length must match the body; Transfer-Encoding accepts chunked.",
						"additionalProperties": map[string]interface{}{"type": "string"},
					},
					"body": map[string]interface{}{
						"type":        "string",
						"description": "Raw request body, sent as is.",
					},
					"body_base64": map[string]interface{}{
						"type":        "string",
						"description": "Binary request body, base64-encoded.",
					},
					"body_file": map[string]interface{}{
						"type":        "string",
						"description": "Path of a local file (relative to the workspace) sent as the whole body. Content-Type follows the file extension unless headers sets it.",
					},
					"json": map[string]interface{}{
						"description": "Any JSON value, sent as the body with Content-Type application/json.",
					},
					"form": map[string]interface{}{
						"type":        "object",
						"description": "Fields sent as an application/x-www-form-urlencoded body. " + scalarNote,
					},
					"form_data": map[string]interface{}{
						"type":        "array",
						"description": "multipart/form-data parts, in order. A part carries value for a text field or file for a local file to upload.",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"name":         map[string]interface{}{"type": "string", "description": "Field name."},
								"value":        map[string]interface{}{"type": "string", "description": "Text value of the field."},
								"file":         map[string]interface{}{"type": "string", "description": "Path of a local file to upload (relative to the workspace)."},
								"filename":     map[string]interface{}{"type": "string", "description": "File name sent to the server (default: the file's own name)."},
								"content_type": map[string]interface{}{"type": "string", "description": "Content-Type of the part (default: from the file extension)."},
							},
							"required": []string{"name"},
						},
					},
					"output_file": map[string]interface{}{
						"type":        "string",
						"description": "Save the response body to this path (relative to the workspace) instead of returning it. Use it for binary or large downloads.",
					},
					"follow_redirects": map[string]interface{}{
						"type":        "boolean",
						"description": "Follow redirects that stay on the same origin (default false: a 3xx answer is returned with its Location). A redirect to another origin is never followed.",
					},
					"proxy": map[string]interface{}{
						"type":        "string",
						"description": "Proxy URL for this request: http://, https://, socks5:// or socks5h://, credentials allowed. \"direct\" ignores the proxies set in the environment; unset uses them.",
					},
					"verify_tls": map[string]interface{}{
						"type":        "boolean",
						"description": "Verify the server's TLS certificate (default true). false accepts any certificate, like curl -k.",
					},
					"timeout_seconds": map[string]interface{}{
						"type":        "integer",
						"description": "Timeout for the whole exchange in seconds (default 30, max 600).",
					},
					"permission_rationale": map[string]interface{}{
						"type":        "string",
						"description": "Optional text shown at the top of the permission dialog.",
					},
				},
				"required": []string{"url"},
			},
		},
		RequiresPermission: true,
		Execute:            executeHTTPRequest,
	}
}

// HTTPRequest is one validated http_request call: where it goes, what it
// carries and how it travels. The tool sends it, and the permission gate
// decides on the same value, so what the operator approves is what is sent.
type HTTPRequest struct {
	// Method is upper-cased.
	Method string
	// URL carries the query parameters merged in; its fragment is dropped.
	URL *url.URL
	// Header is what the request sends beyond what net/http adds itself. A
	// User-Agent with an empty value means none is sent.
	Header http.Header
	// Host overrides the Host header when set.
	Host string
	// Files lists the local files the request uploads, as absolute paths.
	Files []LocalFile
	// OutputFile is the absolute path the response body is saved to, if any.
	OutputFile string
	// FollowRedirects follows redirects within the origin.
	FollowRedirects bool
	// Proxy is the proxy the request goes through; nil with Direct false
	// defers to the environment.
	Proxy *url.URL
	// Direct ignores the environment's proxies.
	Direct bool
	// InsecureTLS skips the server certificate check.
	InsecureTLS bool
	// Timeout bounds the whole exchange.
	Timeout time.Duration
	// Rationale is the model's own words for the permission prompt.
	Rationale string

	body requestBody
}

// LocalFile is one workspace file a request uploads.
type LocalFile struct {
	// Field is the form_data part name; empty for body_file.
	Field string
	// Path is absolute.
	Path string
	Size int64
}

// requestBody is the payload, ready to be opened as many times as the
// transport asks for it (a 307 or 308 redirect sends it again).
type requestBody struct {
	// kind names the argument the payload came from; empty means no body.
	kind        string
	contentType string
	segments    []bodySegment
	// chunked sends the body with an unknown length.
	chunked bool
}

// bodySegment is either bytes or a file of known size.
type bodySegment struct {
	data []byte
	file string
	size int64
}

func (b *requestBody) length() int64 {
	var n int64
	for _, s := range b.segments {
		if s.file != "" {
			n += s.size
		} else {
			n += int64(len(s.data))
		}
	}
	return n
}

// bytes returns the payload when it holds no file.
func (b *requestBody) bytes() []byte {
	var out []byte
	for _, s := range b.segments {
		if s.file != "" {
			return nil
		}
		out = append(out, s.data...)
	}
	return out
}

func (b *requestBody) open() (io.ReadCloser, error) {
	readers := make([]io.Reader, 0, len(b.segments))
	var files []*os.File
	for _, s := range b.segments {
		if s.file == "" {
			readers = append(readers, bytes.NewReader(s.data))
			continue
		}
		f, err := os.Open(s.file)
		if err != nil {
			for _, open := range files {
				_ = open.Close()
			}
			return nil, err
		}
		files = append(files, f)
		readers = append(readers, f)
	}
	return &segmentReader{Reader: io.MultiReader(readers...), files: files}, nil
}

type segmentReader struct {
	io.Reader
	files []*os.File
}

func (r *segmentReader) Close() error {
	var first error
	for _, f := range r.files {
		if err := f.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

type formPartArgs struct {
	Name        string  `json:"name"`
	Value       *string `json:"value"`
	File        string  `json:"file"`
	Filename    string  `json:"filename"`
	ContentType string  `json:"content_type"`
}

type httpRequestArgs struct {
	URL                 string                     `json:"url"`
	Method              string                     `json:"method"`
	Query               map[string]json.RawMessage `json:"query"`
	Headers             map[string]json.RawMessage `json:"headers"`
	Body                string                     `json:"body"`
	BodyBase64          string                     `json:"body_base64"`
	BodyFile            string                     `json:"body_file"`
	JSON                json.RawMessage            `json:"json"`
	Form                map[string]json.RawMessage `json:"form"`
	FormData            []formPartArgs             `json:"form_data"`
	OutputFile          string                     `json:"output_file"`
	FollowRedirects     bool                       `json:"follow_redirects"`
	Proxy               string                     `json:"proxy"`
	VerifyTLS           *bool                      `json:"verify_tls"`
	TimeoutSeconds      int                        `json:"timeout_seconds"`
	PermissionRationale string                     `json:"permission_rationale"`
}

// ParseHTTPRequest validates http_request arguments against the workspace cwd.
// Local files are checked to exist but not read.
func ParseHTTPRequest(argsJSON, cwd string) (*HTTPRequest, error) {
	args, err := tooling.ParseArgs[httpRequestArgs](argsJSON)
	if err != nil {
		return nil, err
	}
	u, err := parseRequestURL(args.URL)
	if err != nil {
		return nil, err
	}
	if len(args.Query) > 0 {
		extra, err := encodeValues("query", args.Query)
		if err != nil {
			return nil, err
		}
		if u.RawQuery == "" {
			u.RawQuery = extra
		} else if extra != "" {
			u.RawQuery += "&" + extra
		}
	}
	req := &HTTPRequest{
		URL:             u,
		Header:          http.Header{},
		FollowRedirects: args.FollowRedirects,
		Rationale:       strings.TrimSpace(args.PermissionRationale),
		Timeout:         defaultHTTPTimeout,
	}
	if args.TimeoutSeconds > 0 {
		req.Timeout = time.Duration(args.TimeoutSeconds) * time.Second
		if req.Timeout > maxHTTPTimeout {
			req.Timeout = maxHTTPTimeout
		}
	}
	if err := req.parseBody(args, cwd); err != nil {
		return nil, err
	}
	req.Method = strings.ToUpper(strings.TrimSpace(args.Method))
	if req.Method == "" {
		req.Method = http.MethodGet
		if req.body.kind != "" {
			req.Method = http.MethodPost
		}
	}
	if !httpguts.ValidHeaderFieldName(req.Method) {
		return nil, fmt.Errorf("method %q is not a valid HTTP method token", args.Method)
	}
	if err := req.parseHeaders(args.Headers); err != nil {
		return nil, err
	}
	if out := strings.TrimSpace(args.OutputFile); out != "" {
		req.OutputFile = absPath(out, cwd)
	}
	if err := req.parseRoute(args); err != nil {
		return nil, err
	}
	return req, nil
}

func parseRequestURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("url is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("url: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("url %q: the scheme must be http or https", raw)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("url %q has no host", raw)
	}
	u.Scheme = scheme
	u.Fragment, u.RawFragment = "", ""
	return u, nil
}

func (r *HTTPRequest) parseBody(args httpRequestArgs, cwd string) error {
	var given []string
	if args.Body != "" {
		given = append(given, "body")
	}
	if args.BodyBase64 != "" {
		given = append(given, "body_base64")
	}
	if strings.TrimSpace(args.BodyFile) != "" {
		given = append(given, "body_file")
	}
	jsonGiven := len(bytes.TrimSpace(args.JSON)) > 0 && string(bytes.TrimSpace(args.JSON)) != "null"
	if jsonGiven {
		given = append(given, "json")
	}
	if len(args.Form) > 0 {
		given = append(given, "form")
	}
	if len(args.FormData) > 0 {
		given = append(given, "form_data")
	}
	if len(given) > 1 {
		return fmt.Errorf("pass one payload, got %s", strings.Join(given, " and "))
	}
	if len(given) == 0 {
		return nil
	}
	b := &r.body
	b.kind = given[0]
	switch b.kind {
	case "body":
		b.segments = []bodySegment{{data: []byte(args.Body)}}
	case "body_base64":
		data, err := decodeBase64(args.BodyBase64)
		if err != nil {
			return fmt.Errorf("body_base64: %w", err)
		}
		b.segments = []bodySegment{{data: data}}
		b.contentType = "application/octet-stream"
	case "body_file":
		path := absPath(args.BodyFile, cwd)
		size, err := regularFileSize(path)
		if err != nil {
			return fmt.Errorf("body_file: %w", err)
		}
		b.segments = []bodySegment{{file: path, size: size}}
		b.contentType = contentTypeByName(path)
		r.Files = append(r.Files, LocalFile{Path: path, Size: size})
	case "json":
		data, err := compactJSON(args.JSON)
		if err != nil {
			return err
		}
		b.segments = []bodySegment{{data: data}}
		b.contentType = "application/json"
	case "form":
		encoded, err := encodeValues("form", args.Form)
		if err != nil {
			return err
		}
		b.segments = []bodySegment{{data: []byte(encoded)}}
		b.contentType = "application/x-www-form-urlencoded"
	case "form_data":
		return r.parseMultipart(args.FormData, cwd)
	}
	return nil
}

// parseMultipart lays the multipart envelope out once, with the files as
// placeholders of known size, so the body has an exact Content-Length and
// can be opened again for a redirect without reading any file twice now.
func (r *HTTPRequest) parseMultipart(parts []formPartArgs, cwd string) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	var segments []bodySegment
	flush := func() {
		if buf.Len() > 0 {
			segments = append(segments, bodySegment{data: bytes.Clone(buf.Bytes())})
			buf.Reset()
		}
	}
	for i, p := range parts {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			return fmt.Errorf("form_data[%d]: name is required", i)
		}
		hasFile := strings.TrimSpace(p.File) != ""
		if (p.Value != nil) == hasFile {
			return fmt.Errorf("form_data[%d] %q: give either value or file", i, name)
		}
		h := make(textproto.MIMEHeader)
		if !hasFile {
			h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"`, escapeQuotes(name)))
			if ct := strings.TrimSpace(p.ContentType); ct != "" {
				h.Set("Content-Type", ct)
			}
			w, err := mw.CreatePart(h)
			if err != nil {
				return err
			}
			if _, err := io.WriteString(w, *p.Value); err != nil {
				return err
			}
			continue
		}
		path := absPath(p.File, cwd)
		size, err := regularFileSize(path)
		if err != nil {
			return fmt.Errorf("form_data[%d] %q: %w", i, name, err)
		}
		filename := strings.TrimSpace(p.Filename)
		if filename == "" {
			filename = filepath.Base(path)
		}
		ct := strings.TrimSpace(p.ContentType)
		if ct == "" {
			ct = contentTypeByName(path)
		}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, escapeQuotes(name), escapeQuotes(filename)))
		h.Set("Content-Type", ct)
		if _, err := mw.CreatePart(h); err != nil {
			return err
		}
		flush()
		segments = append(segments, bodySegment{file: path, size: size})
		r.Files = append(r.Files, LocalFile{Field: name, Path: path, Size: size})
	}
	if err := mw.Close(); err != nil {
		return err
	}
	flush()
	r.body.segments = segments
	r.body.contentType = mw.FormDataContentType()
	return nil
}

func (r *HTTPRequest) parseHeaders(raw map[string]json.RawMessage) error {
	names := make([]string, 0, len(raw))
	for name := range raw {
		names = append(names, name)
	}
	sort.Strings(names)
	given := map[string]bool{}
	var removed []string
	for _, rawName := range names {
		name := strings.TrimSpace(rawName)
		if !httpguts.ValidHeaderFieldName(name) {
			return fmt.Errorf("headers: %q is not a valid header name", rawName)
		}
		value, err := scalarValue(raw[rawName])
		if err != nil {
			return fmt.Errorf("headers %q: %w", name, err)
		}
		value = strings.TrimSpace(value)
		if !httpguts.ValidHeaderFieldValue(value) {
			return fmt.Errorf("headers %q: the value may not contain line breaks or control characters", name)
		}
		key := textproto.CanonicalMIMEHeaderKey(name)
		given[key] = true
		switch key {
		case "Host":
			if value == "" {
				return fmt.Errorf("headers: Host cannot be removed")
			}
			r.Host = value
			continue
		case "Content-Length":
			if value == "" {
				continue
			}
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || n != r.body.length() {
				return fmt.Errorf("headers: Content-Length %q does not match the %d-byte body; leave it out and it is computed", value, r.body.length())
			}
			continue
		case "Transfer-Encoding":
			switch strings.ToLower(value) {
			case "":
			case "chunked":
				r.body.chunked = true
			default:
				return fmt.Errorf("headers: Transfer-Encoding %q is not supported; only chunked is", value)
			}
			continue
		}
		if value == "" {
			removed = append(removed, key)
			continue
		}
		r.Header.Set(key, value)
	}
	if !given["User-Agent"] {
		r.Header.Set("User-Agent", userAgent)
	}
	if !given["Content-Type"] && r.body.contentType != "" {
		r.Header.Set("Content-Type", r.body.contentType)
	}
	for _, key := range removed {
		// net/http leaves User-Agent out only when the header is present and
		// empty; any other header is left out by not being set at all.
		if key == "User-Agent" {
			r.Header["User-Agent"] = []string{""}
		}
	}
	return nil
}

func (r *HTTPRequest) parseRoute(args httpRequestArgs) error {
	if args.VerifyTLS != nil && !*args.VerifyTLS {
		r.InsecureTLS = true
	}
	raw := strings.TrimSpace(args.Proxy)
	switch {
	case raw == "":
		return nil
	case strings.EqualFold(raw, "direct"):
		r.Direct = true
		return nil
	}
	p, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("proxy: %w", err)
	}
	switch strings.ToLower(p.Scheme) {
	case "http", "https", "socks5", "socks5h":
	default:
		return fmt.Errorf("proxy %q: the scheme must be http, https, socks5 or socks5h", p.Redacted())
	}
	if p.Hostname() == "" {
		return fmt.Errorf("proxy %q has no host", p.Redacted())
	}
	p.Scheme = strings.ToLower(p.Scheme)
	r.Proxy = p
	return nil
}

// Origin is scheme://host[:port], the unit an "always allow" grant covers.
func (r *HTTPRequest) Origin() string {
	return originOf(r.URL)
}

// Address is the origin and the path, without the query: the unit a
// narrower grant covers.
func (r *HTTPRequest) Address() string {
	return addressOf(r.URL)
}

func addressOf(u *url.URL) string {
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	return originOf(u) + path
}

// HTTPRequestDestination reads only the url of http_request arguments and
// returns its origin and address. A permission dialog names them before, and
// independently of, the rest of the arguments being valid.
func HTTPRequestDestination(argsJSON string) (origin, address string, ok bool) {
	var args struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", "", false
	}
	u, err := parseRequestURL(args.URL)
	if err != nil {
		return "", "", false
	}
	return originOf(u), addressOf(u), true
}

// ProxyOrigin is the proxy's scheme://host[:port], or empty when the request
// names no proxy of its own.
func (r *HTTPRequest) ProxyOrigin() string {
	if r.Proxy == nil {
		return ""
	}
	return originOf(r.Proxy)
}

// Describe renders what the request does for the permission prompt: where it
// goes and how, what it sends, and what it would write.
func (r *HTTPRequest) Describe() string {
	var b strings.Builder
	if r.Rationale != "" {
		b.WriteString(r.Rationale)
		b.WriteString("\n\n")
	}
	b.WriteString(r.Method + " " + r.URL.String() + "\n")
	var lines []string
	if r.Host != "" {
		lines = append(lines, "Host: "+r.Host)
	}
	for _, name := range sortedKeys(r.Header) {
		for _, v := range r.Header[name] {
			if v == "" {
				continue
			}
			// A boundary the tool generated is noise to the person deciding;
			// one the model wrote itself is shown as written.
			if name == "Content-Type" && r.body.kind == "form_data" && v == r.body.contentType {
				v = "multipart/form-data; boundary=<generated>"
			}
			lines = append(lines, name+": "+v)
		}
	}
	if r.body.chunked {
		lines = append(lines, "Transfer-Encoding: chunked")
	}
	if len(lines) > 0 {
		b.WriteString("Headers:\n")
		for _, l := range lines {
			b.WriteString("  " + l + "\n")
		}
	}
	r.describeBody(&b)
	switch {
	case r.Proxy != nil:
		b.WriteString("Proxy: " + r.Proxy.Redacted() + "\n")
	case r.Direct:
		b.WriteString("Proxy: none (direct, the environment's proxies are ignored)\n")
	}
	if r.InsecureTLS {
		b.WriteString("TLS certificate: NOT verified\n")
	}
	if r.FollowRedirects {
		b.WriteString("Follows redirects within " + r.Origin() + "\n")
	}
	if r.OutputFile != "" {
		b.WriteString("Saves the response body to " + r.OutputFile + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (r *HTTPRequest) describeBody(b *strings.Builder) {
	body := &r.body
	switch body.kind {
	case "":
		return
	case "body_file":
		fmt.Fprintf(b, "Body: the file %s (%d bytes)\n", r.Files[0].Path, r.Files[0].Size)
		return
	case "form_data":
		fmt.Fprintf(b, "Body: multipart form, %d bytes\n", body.length())
		files := map[string]LocalFile{}
		for _, f := range r.Files {
			files[f.Field] = f
		}
		// The envelope is re-read part by part so the prompt lists the text
		// fields next to the files in the order they are sent.
		for _, part := range describeParts(body) {
			if f, ok := files[part.name]; ok && part.file {
				fmt.Fprintf(b, "  %s: the file %s (%d bytes)\n", part.name, f.Path, f.Size)
				delete(files, part.name)
				continue
			}
			fmt.Fprintf(b, "  %s = %s\n", part.name, previewText([]byte(part.value), describeBodyChars))
		}
		return
	}
	data := body.bytes()
	label := map[string]string{"body": "", "body_base64": "", "json": "JSON, ", "form": "form, "}[body.kind]
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		fmt.Fprintf(b, "Body: %s%d bytes of binary data\n", label, len(data))
		return
	}
	fmt.Fprintf(b, "Body: %s%d bytes\n", label, len(data))
	for _, line := range strings.Split(previewText(data, describeBodyChars), "\n") {
		b.WriteString("  " + line + "\n")
	}
}

type describedPart struct {
	name, value string
	file        bool
}

// describeParts reads the part names and text values back out of the
// prepared envelope; a file part's content is a separate segment and is never read.
func describeParts(body *requestBody) []describedPart {
	var out []describedPart
	for _, s := range body.segments {
		if s.file != "" {
			continue
		}
		for _, chunk := range strings.Split(string(s.data), "Content-Disposition: form-data; ")[1:] {
			header, rest, _ := strings.Cut(chunk, "\r\n\r\n")
			part := describedPart{file: strings.Contains(header, "filename=")}
			if _, after, ok := strings.Cut(header, `name="`); ok {
				part.name, _, _ = strings.Cut(after, `"`)
			}
			if !part.file {
				part.value, _, _ = strings.Cut(rest, "\r\n--")
			}
			out = append(out, part)
		}
	}
	return out
}

func previewText(data []byte, limit int) string {
	text := string(data)
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + fmt.Sprintf("... (%d more bytes)", len(text)-cut)
}

// newRequest builds the net/http request, opening the payload.
func (r *HTTPRequest) newRequest(ctx context.Context) (*http.Request, error) {
	var body io.ReadCloser
	if r.body.kind != "" {
		opened, err := r.body.open()
		if err != nil {
			return nil, err
		}
		body = opened
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, r.URL.String(), body)
	if err != nil {
		if body != nil {
			_ = body.Close()
		}
		return nil, err
	}
	if r.body.kind != "" {
		req.ContentLength = r.body.length()
		if r.body.chunked {
			req.ContentLength = -1
		}
		req.GetBody = r.body.open
		if req.ContentLength == 0 {
			req.Body, req.GetBody = http.NoBody, nil
			_ = body.Close()
		}
	}
	req.Header = r.Header.Clone()
	if r.Host != "" {
		req.Host = r.Host
	}
	return req, nil
}

// send carries the request out under the policy of its caller.
func (r *HTTPRequest) send(ctx context.Context, p transferPolicy) (*transfer, error) {
	req, err := r.newRequest(ctx)
	if err != nil {
		return nil, err
	}
	p.route = route{proxy: r.Proxy, direct: r.Direct, insecure: r.InsecureTLS}
	return send(ctx, req, p)
}

func executeHTTPRequest(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
	cwd := ""
	if env != nil {
		cwd = env.CWD
	}
	req, err := ParseHTTPRequest(argsJSON, cwd)
	if err != nil {
		return "", err
	}
	tr, err := req.send(ctx, transferPolicy{
		timeout:         req.Timeout,
		followRedirects: req.FollowRedirects,
		sameOriginOnly:  true,
	})
	if err != nil {
		return "", err
	}
	defer tr.Close()
	return req.answer(tr)
}

// answer renders the exchange the way curl -i prints it: the status line, the
// headers, a blank line and the body; notes about what was not shown follow in
// brackets.
func (r *HTTPRequest) answer(tr *transfer) (string, error) {
	resp := tr.resp
	var b strings.Builder
	b.WriteString(resp.Proto + " " + resp.Status + "\n")
	for _, name := range sortedKeys(resp.Header) {
		for _, v := range resp.Header[name] {
			b.WriteString(name + ": " + v + "\n")
		}
	}
	var notes []string
	if len(tr.followed) > 0 {
		notes = append(notes, "followed redirects: "+r.URL.String()+" -> "+strings.Join(tr.followed, " -> "))
	}
	if tr.held != nil {
		notes = append(notes, fmt.Sprintf("redirect to %s not followed: it leaves %s; call http_request with that address to go there", tr.held, r.Origin()))
	}
	body, bodyNotes, err := r.readAnswerBody(resp)
	if err != nil {
		return "", err
	}
	notes = append(bodyNotes, notes...)
	if body != "" {
		b.WriteString("\n")
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
	}
	if len(notes) > 0 {
		b.WriteString("\n")
		for _, n := range notes {
			b.WriteString("[" + n + "]\n")
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func (r *HTTPRequest) readAnswerBody(resp *http.Response) (string, []string, error) {
	if r.OutputFile != "" {
		n, err := saveBody(r.OutputFile, resp.Body)
		if err != nil {
			return "", nil, fmt.Errorf("output_file: %w", err)
		}
		return "", []string{fmt.Sprintf("body: saved %d bytes to %s", n, r.OutputFile)}, nil
	}
	if r.Method == http.MethodHead {
		return "", nil, nil
	}
	reader, encoding, err := decodedBody(resp)
	if err != nil {
		return "", nil, err
	}
	data, truncated, err := readLimited(reader, maxHTTPBodyDisplayBytes)
	if err != nil {
		return "", nil, fmt.Errorf("read body: %w", err)
	}
	if len(data) == 0 {
		return "", nil, nil
	}
	var notes []string
	if encoding != "" {
		notes = append(notes, "body decoded from Content-Encoding "+encoding)
	}
	contentType := resp.Header.Get("Content-Type")
	text, ok := bodyText(data, contentType)
	if !ok {
		size := fmt.Sprintf("%d bytes", len(data))
		if truncated {
			size = fmt.Sprintf("more than %d bytes", len(data))
		}
		if contentType == "" {
			contentType = http.DetectContentType(data)
		}
		notes = append(notes, fmt.Sprintf("binary body: %s, %s; pass output_file to save it", size, contentType))
		return "", notes, nil
	}
	if truncated {
		notes = append(notes, fmt.Sprintf("body truncated after %d bytes; pass output_file to save the whole body", len(data)))
	}
	return text, notes, nil
}

// decodedBody undoes a Content-Encoding the transport left in place because
// the request asked for it itself.
func decodedBody(resp *http.Response) (io.Reader, string, error) {
	if resp.Uncompressed {
		return resp.Body, "", nil
	}
	switch enc := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding"))); enc {
	case "gzip", "x-gzip":
		zr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, "", fmt.Errorf("decode gzip body: %w", err)
		}
		return zr, enc, nil
	case "deflate":
		buffered := &peekReader{r: resp.Body}
		if zr, err := zlib.NewReader(buffered); err == nil {
			return zr, enc, nil
		}
		return flate.NewReader(io.MultiReader(bytes.NewReader(buffered.peeked), resp.Body)), enc, nil
	default:
		return resp.Body, "", nil
	}
}

// peekReader remembers what it read so a failed zlib header can be replayed as raw deflate.
type peekReader struct {
	r      io.Reader
	peeked []byte
}

func (p *peekReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.peeked = append(p.peeked, b[:n]...)
	return n, err
}

// bodyText turns a body into text when it is text: in its declared charset,
// or as UTF-8 when it is valid UTF-8 without NUL bytes.
func bodyText(data []byte, contentType string) (string, bool) {
	mediaType, params, _ := mime.ParseMediaType(contentType)
	if cs := strings.ToLower(params["charset"]); cs != "" && cs != "utf-8" && cs != "utf8" {
		if r, err := charset.NewReaderLabel(cs, bytes.NewReader(data)); err == nil {
			if decoded, err := io.ReadAll(r); err == nil {
				return string(decoded), true
			}
		}
	}
	if utf8.Valid(data) && bytes.IndexByte(data, 0) < 0 {
		return string(data), true
	}
	if textualMediaType(mediaType) {
		return strings.ToValidUTF8(string(data), "\uFFFD"), true
	}
	return "", false
}

func textualMediaType(mediaType string) bool {
	switch {
	case strings.HasPrefix(mediaType, "text/"),
		strings.HasSuffix(mediaType, "+json"), strings.HasSuffix(mediaType, "+xml"):
		return true
	}
	switch mediaType {
	case "application/json", "application/xml", "application/javascript", "application/x-www-form-urlencoded",
		"application/yaml", "application/x-yaml", "application/graphql", "application/x-ndjson":
		return true
	}
	return false
}

// saveBody writes a body next to its destination and renames it into place, so
// a failed or oversized download never leaves a half-written file under that name.
func saveBody(path string, r io.Reader) (int64, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(dir, ".coddy-download-*")
	if err != nil {
		return 0, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	n, err := io.Copy(tmp, io.LimitReader(r, maxHTTPDownloadBytes+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, err
	}
	if n > maxHTTPDownloadBytes {
		return 0, fmt.Errorf("the body exceeds %d bytes", maxHTTPDownloadBytes)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return 0, err
	}
	return n, nil
}

// encodeValues renders query or form fields, names in sorted order and the
// values of one name in the order given.
func encodeValues(arg string, fields map[string]json.RawMessage) (string, error) {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	var parts []string
	for _, name := range names {
		values, err := scalarValues(fields[name])
		if err != nil {
			return "", fmt.Errorf("%s %q: %w", arg, name, err)
		}
		for _, v := range values {
			parts = append(parts, url.QueryEscape(name)+"="+url.QueryEscape(v))
		}
	}
	return strings.Join(parts, "&"), nil
}

func scalarValues(raw json.RawMessage) ([]string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var items []json.RawMessage
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return nil, err
		}
		out := make([]string, 0, len(items))
		for _, item := range items {
			v, err := scalarValue(item)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	}
	v, err := scalarValue(trimmed)
	if err != nil {
		return nil, err
	}
	return []string{v}, nil
}

func scalarValue(raw json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return "", nil
	}
	switch trimmed[0] {
	case '"':
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return "", err
		}
		return s, nil
	case '{', '[':
		return "", fmt.Errorf("the value must be a string, a number or a boolean")
	default:
		return string(trimmed), nil
	}
}

// compactJSON renders the json argument. A model that wrote the document as a
// string holding JSON meant the document, so such a string is unwrapped; any
// other string stays a JSON string.
func compactJSON(raw json.RawMessage) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err == nil {
			inner := strings.TrimSpace(s)
			if (strings.HasPrefix(inner, "{") || strings.HasPrefix(inner, "[")) && json.Valid([]byte(inner)) {
				trimmed = []byte(inner)
			}
		}
	}
	var out bytes.Buffer
	if err := json.Compact(&out, trimmed); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}
	return out.Bytes(), nil
}

func decodeBase64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	var lastErr error
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		data, err := enc.DecodeString(s)
		if err == nil {
			return data, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func absPath(p, cwd string) string {
	resolved := toolfs.ResolvePath(strings.TrimSpace(p), cwd)
	if abs, err := filepath.Abs(resolved); err == nil {
		return abs
	}
	return resolved
}

func regularFileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("%s is not a regular file", path)
	}
	return info.Size(), nil
}

func contentTypeByName(path string) string {
	if ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

func escapeQuotes(s string) string {
	return quoteEscaper.Replace(s)
}

func sortedKeys(h http.Header) []string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
