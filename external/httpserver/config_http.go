//go:build http

package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// Sentinel failures of the PUT transaction, mapped to distinct HTTP replies.
var (
	errCoddyConfigUnavailable = errors.New("coddy config unavailable")
	errCoddyConfigParse       = errors.New("coddy config parse failed")
	errCoddyConfigSerialize   = errors.New("coddy config serialize failed")
	errCoddyConfigBackup      = errors.New("coddy config backup failed")
	errCoddyConfigWrite       = errors.New("coddy config write failed")
)

func (s *Server) registerConfigRoutes() {
	s.mux.HandleFunc("GET /coddy/config/schema", s.coddyConfigSchemaGet)
	s.mux.HandleFunc("GET /coddy/config/reasoning-levels", s.coddyConfigReasoningLevelsGet)
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
	// Report the effective auth state (YAML token or out-of-band --auth-token / CODDY_HTTP_TOKEN),
	// not just the config-file token, so the UI can reflect that auth is on regardless of source.
	dto.HTTPServer.AuthConfigured = s.authPolicyNow().enabled
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
	if _, err := config.ParseConfigJSONPreservingSecrets(body, c.Paths, c); err != nil {
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
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeCoddyConfigErr(w, http.StatusBadRequest, "read body")
		return
	}
	// The whole transaction - reading the current config the secret-preserving
	// parse merges into, backup, write, reload, and installing the result into
	// the server and session manager - runs under the process-wide config file
	// lock shared with the agent's config_commit / config_rollback tools.
	// Anything less lets two writers interleave and install runtime state that
	// no longer matches the file.
	var cfgPath string
	txErr := config.WithConfigFileLock(func() error {
		c := s.activeCfg()
		if c == nil {
			return errCoddyConfigUnavailable
		}
		paths := c.Paths
		cfgPath = paths.ConfigPath
		newCfg, err := config.ParseConfigJSONPreservingSecrets(body, paths, c)
		if err != nil {
			return fmt.Errorf("%w: %s", errCoddyConfigParse, err.Error())
		}
		yb, err := config.MarshalConfigYAML(newCfg)
		if err != nil {
			return errCoddyConfigSerialize
		}
		if err := config.BackupCurrent(cfgPath); err != nil {
			s.log.Error("coddy config backup", "error", err)
			return errCoddyConfigBackup
		}
		if err := config.AtomicWriteConfigYAML(cfgPath, yb); err != nil {
			s.log.Error("coddy config write", "error", err)
			return errCoddyConfigWrite
		}
		reloaded, err := config.LoadWithPaths(paths)
		if err != nil {
			s.log.Error("coddy config reload after write", "error", err)
			if bak, er2 := os.ReadFile(config.BackupPath(cfgPath)); er2 == nil {
				if er3 := config.AtomicWriteConfigYAML(cfgPath, bak); er3 != nil {
					s.log.Error("coddy config rollback", "error", er3)
				}
			}
			return err
		}
		s.ReplaceConfig(reloaded)
		s.mgr.ReplaceConfig(reloaded)
		return nil
	})
	switch {
	case errors.Is(txErr, errCoddyConfigUnavailable):
		writeCoddyConfigErr(w, http.StatusInternalServerError, "config unavailable")
		return
	case errors.Is(txErr, errCoddyConfigParse):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"error": strings.TrimPrefix(txErr.Error(), errCoddyConfigParse.Error()+": "),
		})
		return
	case errors.Is(txErr, errCoddyConfigSerialize):
		writeCoddyConfigErr(w, http.StatusInternalServerError, "serialize yaml")
		return
	case errors.Is(txErr, errCoddyConfigBackup):
		writeCoddyConfigErr(w, http.StatusInternalServerError, "backup failed")
		return
	case errors.Is(txErr, errCoddyConfigWrite):
		writeCoddyConfigErr(w, http.StatusInternalServerError, "write failed")
		return
	case txErr != nil:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"error": txErr.Error(),
		})
		return
	}
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
