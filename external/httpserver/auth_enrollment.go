//go:build http

package httpserver

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/webauth"
	"rsc.io/qr"
)

func (s *Server) enrollmentAuth(w http.ResponseWriter, r *http.Request) bool {
	if _, ok := s.sessionFromRequest(r, s.loginPolicyNow()); !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "authentication required"})
		return false
	}
	if !isSameOriginRequest(r) {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "cross-site request refused"})
		return false
	}
	return true
}

func enrollmentBody(r *http.Request, v interface{}) error {
	return json.NewDecoder(io.LimitReader(r.Body, maxLoginBodyBytes)).Decode(v)
}

func enrollmentQR(uri string) (string, error) {
	code, err := qr.Encode(uri, qr.M)
	if err != nil {
		return "", err
	}
	const scale, border = 5, 4
	n := (code.Size + 2*border) * scale
	img := image.NewGray(image.Rect(0, 0, n, n))
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			v := color.Gray{Y: 255}
			cx, cy := x/scale-border, y/scale-border
			if cx >= 0 && cy >= 0 && cx < code.Size && cy < code.Size && code.Black(cx, cy) {
				v.Y = 0
			}
			img.SetGray(x, y, v)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes()), nil
}

func (s *Server) enrollmentSetup(w http.ResponseWriter, r *http.Request) {
	if !s.enrollmentAuth(w, r) {
		return
	}
	var req struct {
		Secret string `json:"secret"`
	}
	if err := enrollmentBody(r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, 400, map[string]interface{}{"error": "invalid request"})
		return
	}
	secret := req.Secret
	if secret == "" {
		var err error
		secret, err = webauth.GenerateTOTPSecret()
		if err != nil {
			writeJSON(w, 500, map[string]interface{}{"error": "setup failed"})
			return
		}
	}
	uri := webauth.TOTPKeyURI(s.loginPolicyNow().account.user, secret)
	if uri == "" {
		writeJSON(w, 400, map[string]interface{}{"error": "invalid secret"})
		return
	}
	u, err := url.Parse(uri)
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"error": "setup failed"})
		return
	}
	image, err := enrollmentQR(uri)
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"error": "setup failed"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]interface{}{"secret": u.Query().Get("secret"), "uri": uri, "qr_png": image})
}

func (s *Server) enrollmentChange(w http.ResponseWriter, r *http.Request) {
	if !s.enrollmentAuth(w, r) {
		return
	}
	var req struct {
		Secret string `json:"secret"`
		Code   string `json:"code"`
	}
	if r.URL.Path == "/coddy/auth/totp/enable" {
		if err := enrollmentBody(r, &req); err != nil || webauth.ValidateTOTPSecret(req.Secret) != nil {
			writeJSON(w, 400, map[string]interface{}{"error": "invalid enrollment"})
			return
		}
		if _, ok := webauth.VerifyTOTP(req.Secret, req.Code, s.authNow()); !ok {
			writeJSON(w, 400, map[string]interface{}{"error": "invalid code"})
			return
		}
		uri := webauth.TOTPKeyURI("account", req.Secret)
		u, _ := url.Parse(uri)
		req.Secret = u.Query().Get("secret")
	}
	var codes []string
	err := config.WithConfigFileLock(func() error {
		if _, ok := s.sessionFromRequest(r, s.loginPolicyNow()); !ok {
			return errEnrollmentSession
		}
		c := s.activeCfg()
		if c == nil {
			return errors.New("configuration unavailable")
		}
		next := *c
		switch r.URL.Path {
		case "/coddy/auth/totp/enable":
			state, plain, err := webauth.GenerateBackupCodes(10)
			if err != nil {
				return err
			}
			next.HTTPServer.Login.TOTPSecret = req.Secret
			next.HTTPServer.Login.BackupCodes = state
			codes = plain
		case "/coddy/auth/totp/disable":
			next.HTTPServer.Login.TOTPSecret = ""
			next.HTTPServer.Login.BackupCodes = webauth.BackupState{}
		case "/coddy/auth/backup-codes/regenerate":
			if c.HTTPServer.Login.TOTPSecret == "" {
				return errEnrollmentNotEnabled
			}
			state, plain, err := webauth.GenerateBackupCodes(10)
			if err != nil {
				return err
			}
			next.HTTPServer.Login.BackupCodes = state
			codes = plain
		}
		doc, err := json.Marshal(config.ConfigToJSONDTO(&next))
		if err != nil {
			return err
		}
		if _, err := config.ParseConfigJSONPreservingSecrets(doc, next.Paths, &next); err != nil {
			return err
		}
		path := c.Paths.ConfigPath
		data, err := config.MarshalConfigYAMLForEdit(&next, c, c, path)
		if err != nil {
			return err
		}
		if err = config.BackupCurrent(path); err != nil {
			return err
		}
		if err = config.AtomicWriteConfigYAML(path, data); err != nil {
			return err
		}
		loaded, err := config.LoadWithPaths(c.Paths)
		if err == nil {
			err = s.mgr.InstallConfig(loaded)
		}
		if err != nil {
			if old, e := os.ReadFile(config.BackupPath(path)); e == nil {
				if e = config.AtomicWriteConfigYAML(path, old); e != nil {
					s.log.Error("authentication config rollback failed")
				}
			} else {
				s.log.Error("authentication config backup could not be read")
			}
			return err
		}
		return nil
	})
	if errors.Is(err, errEnrollmentSession) {
		writeJSON(w, 401, map[string]interface{}{"error": "authentication required"})
		return
	}
	if errors.Is(err, errEnrollmentNotEnabled) {
		writeJSON(w, 400, map[string]interface{}{"error": "TOTP is not enabled"})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"error": "could not update authentication"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if codes != nil {
		writeJSON(w, 200, map[string]interface{}{"codes": codes})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"ok": true})
}

var errEnrollmentNotEnabled = errors.New("TOTP not enabled")
var errEnrollmentSession = errors.New("session expired")
