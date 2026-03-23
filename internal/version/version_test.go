package version

import "testing"

func TestGetReturnsNonEmpty(t *testing.T) {
	v := Get()
	if v == "" {
		t.Fatal("Get() must never return an empty string")
	}
}

func TestGetFallback(t *testing.T) {
	original := Version
	defer func() { Version = original }()

	Version = ""
	if got := Get(); got != "dev" {
		t.Fatalf("expected fallback 'dev', got %q", got)
	}
}

func TestGetInjected(t *testing.T) {
	original := Version
	defer func() { Version = original }()

	Version = "v1.2.3"
	if got := Get(); got != "v1.2.3" {
		t.Fatalf("expected 'v1.2.3', got %q", got)
	}
}
