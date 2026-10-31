//go:build http

package httpserver

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/EvilFreelancer/coddy-agent/internal/wallpaper"
)

func (s *Server) registerWallpaperRoutes() {
	s.mux.HandleFunc("GET /coddy/wallpapers", s.wallpapersGet)
	s.mux.HandleFunc("GET /coddy/wallpapers/{name}", s.wallpaperGet)
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
	w.Header().Set("Content-Type", picture.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(picture.Bytes)))
	if r.Method != http.MethodHead {
		_, _ = w.Write(picture.Bytes)
	}
}

func writeWallpaperError(w http.ResponseWriter, err error) {
	status, message := http.StatusInternalServerError, "wallpaper gallery unavailable"
	switch {
	case errors.Is(err, wallpaper.ErrInvalidName):
		status, message = http.StatusBadRequest, "invalid wallpaper name"
	case errors.Is(err, wallpaper.ErrNotFound):
		status, message = http.StatusNotFound, "wallpaper not found"
	case errors.Is(err, wallpaper.ErrTooLarge):
		status, message = http.StatusRequestEntityTooLarge, "wallpaper exceeds 20 MiB"
	case errors.Is(err, wallpaper.ErrInvalidImage):
		status, message = http.StatusUnsupportedMediaType, "invalid or unsupported wallpaper image"
	}
	http.Error(w, message, status)
}
