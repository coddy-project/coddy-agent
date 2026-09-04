//go:build http

package httpserver

// GET /coddy/workspace/file serves one workspace text file split into lines, for
// the composer's line-range picker: the panel that opens on "@path:" needs to show
// the file so the user can point at the lines a ranged mention will attach.

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// workspaceFileDefaultMaxLines bounds the default response; the picker only ever
// shows a window of a file, and the 512 KiB read cap still applies underneath.
const (
	workspaceFileDefaultMaxLines = 2000
	workspaceFileMaxLines        = 5000
)

// splitWorkspaceFileLines splits decoded file text into display lines. A trailing
// newline does not add an empty last line, and CRLF files lose the stray "\r" so
// the picker shows what the editor shows.
func splitWorkspaceFileLines(text string) []string {
	if text == "" {
		return []string{}
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines
}

func (s *Server) coddyWorkspaceFileGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	pathRel := strings.TrimSpace(q.Get("path_rel"))
	if pathRel == "" {
		http.Error(w, `{"error":{"message":"path_rel query parameter is required"}}`, http.StatusBadRequest)
		return
	}
	maxLines := workspaceFileDefaultMaxLines
	if v := strings.TrimSpace(q.Get("max_lines")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > workspaceFileMaxLines {
			http.Error(w, `{"error":{"message":"max_lines must be between 1 and 5000"}}`, http.StatusBadRequest)
			return
		}
		maxLines = n
	}

	cwdAbs, ok := s.resolveSlashListCWD(w, r)
	if !ok {
		return
	}

	// ReadWorkspaceUTF8 owns path normalization, the traversal guard, the size cap
	// and legacy-encoding decoding, so this handler adds no path handling of its own.
	text, mime, err := session.ReadWorkspaceUTF8(cwdAbs, pathRel)
	if err != nil {
		switch {
		case errors.Is(err, os.ErrNotExist):
			http.Error(w, `{"error":{"message":"file not found"}}`, http.StatusNotFound)
		case errors.Is(err, session.ErrPathTraversal):
			http.Error(w, `{"error":{"message":"path escapes the workspace"}}`, http.StatusBadRequest)
		case errors.Is(err, session.ErrFolderAttach):
			http.Error(w, `{"error":{"message":"path is a directory"}}`, http.StatusBadRequest)
		case errors.Is(err, session.ErrNotDecodableText):
			http.Error(w, `{"error":{"message":"file is not decodable text"}}`, http.StatusBadRequest)
		default:
			http.Error(w, `{"error":{"message":"failed to read workspace file"}}`, http.StatusBadRequest)
		}
		return
	}

	lines := splitWorkspaceFileLines(text)
	total := len(lines)
	truncated := total > maxLines
	if truncated {
		lines = lines[:maxLines]
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":      "coddy.workspace_file",
		"path_rel":    pathRel,
		"mime_type":   mime,
		"lines":       lines,
		"total_lines": total,
		"truncated":   truncated,
	})
}
