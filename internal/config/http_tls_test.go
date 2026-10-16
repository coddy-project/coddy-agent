package config

import (
	"testing"
)

func TestASharedModelRequiresAToken(t *testing.T) {
	cfg := &Config{}
	cfg.Models = []ModelEntry{{Model: "stub/m", SharedAs: "coder"}}
	if err := SharedModelsAuthProblem(cfg, ExtraTokens{}); err == nil {
		t.Error("a shared model without a token is open")
	}
	cfg.HTTPServer.SharedModels.Tokens = []string{"share-1"}
	if err := SharedModelsAuthProblem(cfg, ExtraTokens{}); err != nil {
		t.Errorf("a shared-model token is the credential: %v", err)
	}
}
