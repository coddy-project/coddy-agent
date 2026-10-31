//go:build http

package httpserver

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/EvilFreelancer/coddy-agent/internal/wallpaper"
)

func (s *Server) registerWallpaperRoutes() {
	s.mux.HandleFunc("GET /coddy/wallpapers", s.wallpapersGet)
	s.mux.HandleFunc("GET /coddy/wallpapers/{name}", s.wallpaperGet)
	s.mux.HandleFunc("POST /coddy/wallpapers/{name}", s.wallpaperPost)
	s.mux.HandleFunc("DELETE /coddy/wallpapers/{name}", s.wallpaperDelete)
}

func (s *Server) wallpapersGet(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	rows, err := wallpaper.List(s.activeCfg().Paths.Home)
	if err != nil {
		writeWallpaperError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Wallpapers []wallpaper.Entry `json:"wallpapers"`
	}{Wallpapers: rows})
}

func (s *Server) wallpaperGet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	picture, err := wallpaper.Read(s.activeCfg().Paths.Home, r.PathValue("name"))
	if err != nil {
		writeWallpaperError(w, err)
		return
	}
	defer func() {
		if err := picture.File.Close(); err != nil {
			s.log.Warn("close wallpaper", "error", err)
		}
	}()
	w.Header().Set("Content-Type", picture.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.FormatInt(picture.Size, 10))
	if r.Method != http.MethodHead {
		if _, err := io.CopyN(w, picture.File, picture.Size); err != nil {
			s.log.Warn("stream wallpaper", "error", err)
		}
	}
}

func (s *Server) wallpaperPost(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	result, err := wallpaper.Upload(r.Context(), s.activeCfg().Paths.Home, r.PathValue("name"), r.Body)
	if err != nil {
		writeWallpaperError(w, err)
		return
	}
	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, result)
}

func (s *Server) wallpaperDelete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if err := wallpaper.Delete(r.Context(), s.activeCfg().Paths.Home, r.PathValue("name")); err != nil {
		writeWallpaperError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeWallpaperError(w http.ResponseWriter, err error) {
	status, message := http.StatusInternalServerError, "wallpaper gallery unavailable"
	switch {
	case errors.Is(err, wallpaper.ErrUnavailable):
		status, message = http.StatusInternalServerError, "wallpaper gallery unavailable"
	case errors.Is(err, wallpaper.ErrInvalidName):
		status, message = http.StatusBadRequest, "invalid wallpaper name"
	case errors.Is(err, wallpaper.ErrNotFound):
		status, message = http.StatusNotFound, "wallpaper not found"
	case errors.Is(err, wallpaper.ErrInvalidImage):
		status, message = http.StatusUnsupportedMediaType, "invalid or unsupported wallpaper image"
	}
	http.Error(w, message, status)
}
