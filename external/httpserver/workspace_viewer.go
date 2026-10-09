//go:build http

package httpserver

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/textenc"
)

const workspaceRawPattern = "GET /coddy/sessions/{id}/workspace/raw"
const workspaceMediaTTL = time.Hour

func (s *Server) registerWorkspaceViewerRoutes() {
	s.mux.HandleFunc("GET /coddy/sessions/{id}/workspace/tree", s.workspaceTree)
	s.mux.HandleFunc(workspaceRawPattern, s.workspaceRaw)
	s.mux.HandleFunc("GET /coddy/sessions/{id}/workspace/text", s.workspaceText)
	s.mux.HandleFunc("POST /coddy/sessions/{id}/workspace/media-token", s.workspaceMediaToken)
}

// Navigation filters are convenience, not access control: an authenticated
// operator may explicitly read a hidden file. Root confines every operation.
func (s *Server) workspaceRoot(w http.ResponseWriter, r *http.Request) (*os.Root, string) {
	st := s.coddyEnsureLoaded(w, r, r.PathValue("id"))
	if st == nil {
		return nil, ""
	}
	if bound, ok := r.Context().Value(workspaceCapabilityContextKey{}).(string); ok && bound != workspaceIdentity(st.GetCWD()) {
		writeSubagentsError(w, 401, "workspace capability no longer applies")
		return nil, ""
	}
	rel, err := session.NormalizeWorkspaceRelativePath(r.URL.Query().Get("path_rel"))
	if err != nil || (rel != "" && !filepath.IsLocal(rel)) {
		writeSubagentsError(w, 400, "invalid workspace path")
		return nil, ""
	}
	root, err := os.OpenRoot(st.GetCWD())
	if err != nil {
		writeSubagentsError(w, 404, "workspace unavailable")
		return nil, ""
	}
	return root, rel
}

type workspaceEntry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path_rel"`
	Kind    string    `json:"kind"`
	Size    int64     `json:"size_bytes"`
	ModTime time.Time `json:"mod_time"`
}

func (s *Server) workspaceTree(w http.ResponseWriter, r *http.Request) {
	root, rel := s.workspaceRoot(w, r)
	if root == nil {
		return
	}
	defer func() { _ = root.Close() }()
	if rel == "" {
		rel = "."
	}
	info, err := root.Lstat(rel)
	if err != nil || !info.IsDir() {
		writeSubagentsError(w, 404, "directory unavailable")
		return
	}
	f, err := root.OpenFile(rel, os.O_RDONLY|workspaceNonblockFlag, 0)
	if err != nil {
		writeSubagentsError(w, 404, "directory unavailable")
		return
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !opened.IsDir() || !os.SameFile(info, opened) {
		writeSubagentsError(w, 404, "directory changed while opening")
		return
	}
	entries, err := f.ReadDir(-1)
	if err != nil {
		writeSubagentsError(w, 500, "directory could not be read")
		return
	}
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, err = strconv.Atoi(v)
		if err != nil || limit < 1 || limit > 1000 {
			writeSubagentsError(w, 400, "limit must be between 1 and 1000")
			return
		}
	}
	afterGroup, afterName, ok := parseTreeCursor(r.URL.Query().Get("cursor"))
	if !ok {
		writeSubagentsError(w, 400, "invalid cursor")
		return
	}
	hidden := r.URL.Query().Get("include_hidden") == "1"
	// Folders first, then everything else, each group by name: the order a file
	// tree reads in. A page may end anywhere in it, so the cursor names the
	// group of its last row as well as the name.
	sort.Slice(entries, func(i, j int) bool {
		gi, gj := treeGroup(entries[i]), treeGroup(entries[j])
		if gi != gj {
			return gi < gj
		}
		return entries[i].Name() < entries[j].Name()
	})
	rows := make([]workspaceEntry, 0, limit)
	hasMore := false
	lastGroup := byte(0)
	for _, e := range entries {
		name := e.Name()
		group := treeGroup(e)
		if afterGroup != 0 && (group < afterGroup || (group == afterGroup && name <= afterName)) {
			continue
		}
		if !utf8.ValidString(name) || (!hidden && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor")) {
			continue
		}
		if len(rows) == limit {
			hasMore = true
			break
		}
		lastGroup = group
		p := filepath.Join(rel, name)
		info, err := root.Lstat(p)
		if err != nil {
			continue
		}
		kind, size := "file", info.Size()
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			kind = "symlink"
		case info.IsDir():
			kind = "directory"
			size = 0
		case !info.Mode().IsRegular():
			kind = "special"
		}
		rows = append(rows, workspaceEntry{name, filepath.ToSlash(p), kind, size, info.ModTime().UTC()})
	}
	next := ""
	if hasMore {
		next = string(lastGroup) + "/" + rows[len(rows)-1].Name
	}
	writeJSON(w, 200, map[string]any{"entries": rows, "next_cursor": next, "has_more": hasMore})
}

// treeGroup sorts a directory entry into the listing's groups: 'd' for a
// folder, 'f' for everything else (a link to a folder included: it is not
// followed, so it is not a folder of this tree).
func treeGroup(e fs.DirEntry) byte {
	if e.IsDir() {
		return 'd'
	}
	return 'f'
}

// parseTreeCursor reads a next_cursor back: the group of the last row listed,
// a slash, and its name. A slash cannot be part of a name, so the first one is
// the separator. An empty cursor starts at the top (group 0).
func parseTreeCursor(cursor string) (byte, string, bool) {
	if cursor == "" {
		return 0, "", true
	}
	if len(cursor) < 3 || cursor[1] != '/' || (cursor[0] != 'd' && cursor[0] != 'f') {
		return 0, "", false
	}
	return cursor[0], cursor[2:], true
}

func workspaceETag(rel string, info fs.FileInfo) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d", rel, info.Size(), info.ModTime().UnixNano())))
	return `W/"` + hex.EncodeToString(sum[:]) + `"`
}

func openWorkspaceFile(root *os.Root, rel string) (*os.File, fs.FileInfo, error) {
	if rel == "" {
		return nil, nil, fs.ErrNotExist
	}
	// Nonblocking Open also covers a regular file replaced by a FIFO after Lstat.
	info, err := root.Lstat(rel)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fs.ErrPermission
	}
	before := info
	f, err := root.OpenFile(rel, os.O_RDONLY|workspaceNonblockFlag, 0)
	if err != nil {
		return nil, nil, err
	}
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(before, info) {
		_ = f.Close()
		return nil, nil, fs.ErrPermission
	}
	return f, info, nil
}

// Inline types are fixed across platforms and checked against the bytes. HTML
// and SVG always download; a filename alone never grants an inline renderer.
func workspaceInlineType(rel string, head []byte) string {
	ext := strings.ToLower(filepath.Ext(rel))
	types := map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp", ".avif": "image/avif", ".mp4": "video/mp4", ".webm": "video/webm", ".mp3": "audio/mpeg", ".ogg": "audio/ogg", ".wav": "audio/wav", ".flac": "audio/flac", ".pdf": "application/pdf", ".txt": "text/plain; charset=utf-8"}
	want := types[ext]
	got := http.DetectContentType(head)
	if want == got {
		return want
	}
	if ext == ".wav" && got == "audio/wave" {
		return want
	}
	if ext == ".flac" && bytesPrefix(head, "fLaC") {
		return want
	}
	if ext == ".avif" && len(head) >= 12 && string(head[4:8]) == "ftyp" && (string(head[8:12]) == "avif" || string(head[8:12]) == "avis") {
		return want
	}
	return "application/octet-stream"
}
func bytesPrefix(data []byte, prefix string) bool {
	return len(data) >= len(prefix) && string(data[:len(prefix)]) == prefix
}

func (s *Server) workspaceRaw(w http.ResponseWriter, r *http.Request) {
	root, rel := s.workspaceRoot(w, r)
	if root == nil {
		return
	}
	defer func() { _ = root.Close() }()
	f, info, err := openWorkspaceFile(root, rel)
	if err != nil {
		writeSubagentsError(w, 404, "regular file unavailable")
		return
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, 512)
	n, err := io.ReadFull(f, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		writeSubagentsError(w, 500, "file could not be read")
		return
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		writeSubagentsError(w, 500, "file could not be read")
		return
	}
	ct := workspaceInlineType(rel, head[:n])
	disposition := "inline"
	if r.URL.Query().Get("download") == "1" || ct == "application/octet-stream" {
		disposition = "attachment"
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": filepath.Base(rel)}))
	w.Header().Set("ETag", workspaceETag(filepath.Join(root.Name(), rel), info))
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	http.ServeContent(w, r, filepath.Base(rel), info.ModTime(), f)
}

func (s *Server) workspaceText(w http.ResponseWriter, r *http.Request) {
	root, rel := s.workspaceRoot(w, r)
	if root == nil {
		return
	}
	defer func() { _ = root.Close() }()
	f, info, err := openWorkspaceFile(root, rel)
	if err != nil {
		writeSubagentsError(w, 404, "regular file unavailable")
		return
	}
	defer func() { _ = f.Close() }()
	versionPath := filepath.Join(root.Name(), rel)
	etag := workspaceETag(versionPath, info)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	if expected := r.URL.Query().Get("etag"); expected != "" && expected != etag {
		writeSubagentsError(w, 409, "file changed; reload before reading another page")
		return
	}
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(304)
		return
	}
	offset, limit := 0, 300
	for name, target := range map[string]*int{"offset": &offset, "max_lines": &limit} {
		if value := r.URL.Query().Get(name); value != "" {
			n, e := strconv.Atoi(value)
			if e != nil || n < 0 {
				writeSubagentsError(w, 400, "invalid line window")
				return
			}
			*target = n
		}
	}
	if limit < 1 || limit > 1000 || offset > 10000000 {
		writeSubagentsError(w, 400, "line window exceeds the limit")
		return
	}
	reader, charset, err := textenc.NewReader(f)
	if err != nil {
		writeSubagentsError(w, 415, "file is not decodable text")
		return
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 8192), 1024*1024)
	lines := make([]string, 0, limit)
	index, totalBytes := 0, 0
	hasMore := false
	for scanner.Scan() {
		if r.Context().Err() != nil {
			return
		}
		if index < offset {
			index++
			continue
		}
		line := scanner.Text()
		if len(lines) == limit || totalBytes+len(line) > 1024*1024 {
			hasMore = true
			break
		}
		lines = append(lines, line)
		totalBytes += len(line)
		index++
	}
	if err := scanner.Err(); err != nil {
		writeSubagentsError(w, 413, "text line exceeds the preview limit")
		return
	}
	// A concurrent rewrite must not splice two versions into a paged document.
	current, err := root.Lstat(rel)
	if err != nil || !os.SameFile(info, current) || workspaceETag(versionPath, current) != etag {
		writeSubagentsError(w, 409, "file changed while reading; reload")
		return
	}
	writeJSON(w, 200, map[string]any{"path_rel": filepath.ToSlash(rel), "lines": lines, "offset": offset, "next_offset": index, "has_more": hasMore, "total_lines_known": !hasMore, "mod_time": info.ModTime().UTC(), "size_bytes": info.Size(), "etag": etag, "charset": charset})
}

type workspaceCapability struct {
	Version   int    `json:"v"`
	Session   string `json:"session"`
	Path      string `json:"path"`
	Download  bool   `json:"download"`
	Expires   int64  `json:"exp"`
	Workspace string `json:"workspace"`
}

type workspaceCapabilityContextKey struct{}

func workspaceIdentity(cwd string) string {
	sum := sha256.Sum256([]byte(cwd))
	return hex.EncodeToString(sum[:])
}

func (s *Server) workspaceSigningKey() []byte {
	// Domain-separated API credentials provide a stable key on replicas and
	// across restarts. Cookie-only deployments use the process's random key.
	if pol := s.authPolicyNow(); len(pol.tokens) > 0 {
		sum := sha256.Sum256([]byte("coddy-workspace-media\x00" + pol.tokens[0]))
		return sum[:]
	}
	if c := s.activeCfg(); c != nil && c.HTTPServer.Login.PasswordHash != "" {
		sum := sha256.Sum256([]byte("coddy-workspace-media\x00" + c.HTTPServer.Login.PasswordHash))
		return sum[:]
	}
	return []byte(s.workspaceMediaKey)
}

func (s *Server) workspaceMediaToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path     string `json:"path_rel"`
		Download bool   `json:"download"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 16384)).Decode(&req) != nil {
		writeSubagentsError(w, 400, "invalid token request")
		return
	}
	rel, err := session.NormalizeWorkspaceRelativePath(req.Path)
	if err != nil || rel == "" || !filepath.IsLocal(rel) {
		writeSubagentsError(w, 400, "invalid workspace path")
		return
	}
	st := s.coddyEnsureLoaded(w, r, r.PathValue("id"))
	if st == nil {
		return
	}
	root, err := os.OpenRoot(st.GetCWD())
	if err != nil {
		writeSubagentsError(w, 404, "workspace unavailable")
		return
	}
	defer func() { _ = root.Close() }()
	f, _, err := openWorkspaceFile(root, rel)
	if err != nil {
		writeSubagentsError(w, 404, "regular file unavailable")
		return
	}
	_ = f.Close()
	exp := time.Now().Add(workspaceMediaTTL).Unix()
	claim := workspaceCapability{Version: 1, Session: st.GetID(), Path: filepath.ToSlash(rel), Download: req.Download, Expires: exp, Workspace: workspaceIdentity(st.GetCWD())}
	payload, err := json.Marshal(claim)
	if err != nil {
		writeSubagentsError(w, 500, "token unavailable")
		return
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, s.workspaceSigningKey())
	_, _ = mac.Write([]byte(encoded))
	token := encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"token": token, "expires_at": time.Unix(exp, 0).UTC()})
}

func (s *Server) acceptWorkspaceCapability(r *http.Request) bool {
	token := r.URL.Query().Get("access_token")
	if len(token) > 16384 {
		return false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, s.workspaceSigningKey())
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	var claim workspaceCapability
	if json.Unmarshal(payload, &claim) != nil {
		return false
	}
	pathParts := strings.Split(r.URL.Path, "/")
	rel, err := session.NormalizeWorkspaceRelativePath(r.URL.Query().Get("path_rel"))
	now := time.Now().Unix()
	valid := err == nil && len(pathParts) == 6 && claim.Version == 1 && claim.Session == pathParts[3] && claim.Path == filepath.ToSlash(rel) && claim.Download == (r.URL.Query().Get("download") == "1") && claim.Expires > now && claim.Expires <= now+int64(workspaceMediaTTL/time.Second) && claim.Workspace != ""
	if valid {
		*r = *r.WithContext(context.WithValue(r.Context(), workspaceCapabilityContextKey{}, claim.Workspace))
	}
	return valid
}
