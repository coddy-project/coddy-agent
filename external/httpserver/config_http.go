//go:build http

package httpserver

import (
	"encoding/json"
	"io"
	"net/http"
	"os"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func (s *Server) registerConfigRoutes() {
	s.mux.HandleFunc("GET /coddy/config/schema", s.coddyConfigSchemaGet)
	s.mux.HandleFunc("GET /coddy/config", s.coddyConfigGet)
	s.mux.HandleFunc("POST /coddy/config/validate", s.coddyConfigValidatePost)
	s.mux.HandleFunc("PUT /coddy/config", s.coddyConfigPut)
}

func (s *Server) coddyConfigSchemaGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	data, err := config.UISchemaJSON()
	if err != nil {
		s.log.Error("coddy config schema", "error", err)
		writeCoddyConfigErr(w, http.StatusInternalServerError, "schema generation failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func (s *Server) coddyConfigGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	c := s.activeCfg()
	if c == nil {
		writeCoddyConfigErr(w, http.StatusInternalServerError, "config unavailable")
		return
	}
	dto := config.ConfigToJSONDTO(c)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(dto); err != nil {
		s.log.Error("coddy config get encode", "error", err)
	}
}

func (s *Server) coddyConfigValidatePost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	c := s.activeCfg()
	if c == nil {
		writeCoddyConfigErr(w, http.StatusInternalServerError, "config unavailable")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeCoddyConfigErr(w, http.StatusBadRequest, "read body")
		return
	}
	if _, err := config.ParseAndValidateConfigJSON(body, c.Paths); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

func (s *Server) coddyConfigPut(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.NotFound(w, r)
		return
	}
	c := s.activeCfg()
	if c == nil {
		writeCoddyConfigErr(w, http.StatusInternalServerError, "config unavailable")
		return
	}
	paths := c.Paths
	cfgPath := paths.ConfigPath
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeCoddyConfigErr(w, http.StatusBadRequest, "read body")
		return
	}
	newCfg, err := config.ParseAndValidateConfigJSON(body, paths)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}
	yb, err := config.MarshalConfigYAML(newCfg)
	if err != nil {
		writeCoddyConfigErr(w, http.StatusInternalServerError, "serialize yaml")
		return
	}
	if err := config.BackupCurrent(cfgPath); err != nil {
		s.log.Error("coddy config backup", "error", err)
		writeCoddyConfigErr(w, http.StatusInternalServerError, "backup failed")
		return
	}
	if err := config.AtomicWriteConfigYAML(cfgPath, yb); err != nil {
		s.log.Error("coddy config write", "error", err)
		writeCoddyConfigErr(w, http.StatusInternalServerError, "write failed")
		return
	}
	reloaded, err := config.LoadWithPaths(paths)
	if err != nil {
		s.log.Error("coddy config reload after write", "error", err)
		if bak, er2 := os.ReadFile(config.BackupPath(cfgPath)); er2 == nil {
			if er3 := config.AtomicWriteConfigYAML(cfgPath, bak); er3 != nil {
				s.log.Error("coddy config rollback", "error", er3)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}
	s.ReplaceConfig(reloaded)
	s.mgr.ReplaceConfig(reloaded)
	s.log.Info("config updated", "path", cfgPath)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

func writeCoddyConfigErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":    false,
		"error": msg,
	})
}
