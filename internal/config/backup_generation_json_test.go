package config

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/webauth"
)

func TestBackupGenerationSettingsMetadata(t *testing.T) {
	state, codes, err := webauth.GenerateBackupCodes(2)
	if err != nil {
		t.Fatal(err)
	}
	c := &Config{}
	c.HTTPServer.Login.BackupCodes = state
	encoded, err := json.Marshal(ConfigToJSONDTO(c))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		HTTPServer struct {
			Login struct {
				Generation string `json:"backup_codes_generation"`
			} `json:"login"`
		} `json:"httpserver"`
	}
	if err := json.Unmarshal(encoded, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.HTTPServer.Login.Generation != state.Generation || state.Generation == "" {
		t.Fatalf("wrong generation %q", doc.HTTPServer.Login.Generation)
	}
	for _, secret := range append(codes, state.Hashes...) {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("settings exposed backup code or hash")
		}
	}
}
