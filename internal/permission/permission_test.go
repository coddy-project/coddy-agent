package permission

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestMasterKeyActive(t *testing.T) {
	if MasterKeyActive(nil) {
		t.Fatal("nil cfg")
	}
	if MasterKeyActive(&config.Config{}) {
		t.Fatal("empty")
	}
	if !MasterKeyActive(&config.Config{Tools: config.Tools{PermissionMasterKey: "x"}}) {
		t.Fatal("should be active")
	}
}
