package update

import "testing"

func TestAssetFileName(t *testing.T) {
	t.Parallel()
	got, err := AssetFileName("0.9.3", "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if got != "coddy_0.9.3_linux_amd64.tar.gz" {
		t.Fatalf("got %q", got)
	}
	got, err = AssetFileName("1.0.0", "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if got != "coddy_1.0.0_windows_amd64.zip" {
		t.Fatalf("got %q", got)
	}
}

func TestAssetFileName_unsupported(t *testing.T) {
	t.Parallel()
	if _, err := AssetFileName("0.1.0", "freebsd", "amd64"); err == nil {
		t.Fatal("expected error for unsupported platform")
	}
}
