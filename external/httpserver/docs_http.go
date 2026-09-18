//go:build http

package httpserver

// Documentation surface of the REST API: the contents, one page and a search
// over Coddy's own documentation, built into the binary (internal/docs). The
// web UI's reader is its client; the pages are the ones this very binary
// was built with, so the reader needs no site.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/docs"
)

const (
	docsSearchDefaultLimit = 10
	docsSearchMaxLimit     = 50
)

func (s *Server) registerDocsRoutes() {
	s.mux.HandleFunc("GET /coddy/docs", s.coddyDocsContents)
	s.mux.HandleFunc("GET /coddy/docs/page", s.coddyDocsPage)
	s.mux.HandleFunc("GET /coddy/docs/search", s.coddyDocsSearch)
}

type docsPageRef struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
}

type docsGroupJSON struct {
	ID      string        `json:"id"`
	Title   string        `json:"title"`
	Summary string        `json:"summary"`
	Pages   []docsPageRef `json:"pages"`
}

type docsHeadingJSON struct {
	Level  int    `json:"level"`
	Text   string `json:"text"`
	Anchor string `json:"anchor"`
}

type docsHitJSON struct {
	Slug    string          `json:"slug"`
	Title   string          `json:"title"`
	Group   string          `json:"group"`
	Anchor  string          `json:"anchor,omitempty"`
	Heading string          `json:"heading,omitempty"`
	Snippet []docs.Fragment `json:"snippet"`
}

func writeDocsError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = fmt.Fprintf(w, `{"error":{"message":%q}}`, msg)
}

func writeDocsJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) docsLibrary(w http.ResponseWriter) *docs.Library {
	lib, err := docs.Default()
	if err != nil {
		writeDocsError(w, http.StatusInternalServerError, "the built-in documentation does not load: "+err.Error())
		return nil
	}
	return lib
}

func (s *Server) coddyDocsContents(w http.ResponseWriter, _ *http.Request) {
	lib := s.docsLibrary(w)
	if lib == nil {
		return
	}
	groups := make([]docsGroupJSON, 0, len(lib.Groups))
	for _, g := range lib.Groups {
		gj := docsGroupJSON{ID: g.ID, Title: g.Title, Summary: g.Summary, Pages: make([]docsPageRef, 0, len(g.Pages))}
		for _, p := range g.Pages {
			gj.Pages = append(gj.Pages, docsPageRef{Slug: p.Slug, Title: p.Title, Summary: p.Summary})
		}
		groups = append(groups, gj)
	}
	writeDocsJSON(w, map[string]interface{}{
		"object":  "coddy.docs",
		"version": lib.Version,
		"groups":  groups,
	})
}

func neighbour(p *docs.Page) *docsPageRef {
	if p == nil {
		return nil
	}
	return &docsPageRef{Slug: p.Slug, Title: p.Title}
}

func (s *Server) coddyDocsPage(w http.ResponseWriter, r *http.Request) {
	lib := s.docsLibrary(w)
	if lib == nil {
		return
	}
	ref := strings.TrimSpace(r.URL.Query().Get("ref"))
	if ref == "" {
		writeDocsError(w, http.StatusBadRequest, "ref is required: a page such as features/mentions, optionally with #section")
		return
	}
	page, anchor, err := lib.Resolve(ref)
	if err != nil {
		writeDocsError(w, http.StatusNotFound, err.Error())
		return
	}
	headings := make([]docsHeadingJSON, 0, len(page.Headings))
	for _, h := range page.Headings {
		headings = append(headings, docsHeadingJSON{Level: h.Level, Text: h.Text, Anchor: h.Anchor})
	}
	writeDocsJSON(w, map[string]interface{}{
		"object":   "coddy.docs_page",
		"version":  lib.Version,
		"slug":     page.Slug,
		"title":    page.Title,
		"summary":  page.Summary,
		"group":    map[string]string{"id": page.Group.ID, "title": page.Group.Title},
		"anchor":   anchor,
		"markdown": page.Markdown,
		"headings": headings,
		"prev":     neighbour(lib.Prev(page)),
		"next":     neighbour(lib.Next(page)),
		"url":      page.SiteURL(),
	})
}

func (s *Server) coddyDocsSearch(w http.ResponseWriter, r *http.Request) {
	lib := s.docsLibrary(w)
	if lib == nil {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := docsSearchDefaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > docsSearchMaxLimit {
			writeDocsError(w, http.StatusBadRequest, fmt.Sprintf("limit must be an integer from 1 to %d", docsSearchMaxLimit))
			return
		}
		limit = n
	}
	hits := make([]docsHitJSON, 0, limit)
	for _, h := range lib.Search(q, limit) {
		hits = append(hits, docsHitJSON{Slug: h.Slug, Title: h.Title, Group: h.Group, Anchor: h.Anchor, Heading: h.Heading, Snippet: h.Snippet})
	}
	writeDocsJSON(w, map[string]interface{}{
		"object":  "coddy.docs_search",
		"version": lib.Version,
		"query":   q,
		"hits":    hits,
	})
}
