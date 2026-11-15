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
	for _, goos := range []string{"android", "freebsd"} {
		for _, goarch := range []string{"arm64", "amd64"} {
			got, err = AssetFileName("1.2.30", goos, goarch)
			if err != nil {
				t.Fatal(err)
			}
			if want := "coddy_1.2.30_" + goos + "_" + goarch + ".tar.gz"; got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		}
	}
}

func TestAssetFileName_unsupported(t *testing.T) {
	t.Parallel()
	if _, err := AssetFileName("0.1.0", "openbsd", "amd64"); err == nil {
		t.Fatal("expected error for unsupported platform")
	}
	// Android and FreeBSD are published for their two 64-bit architectures.
	for _, goos := range []string{"android", "freebsd"} {
		for _, goarch := range []string{"arm", "386"} {
			if _, err := AssetFileName("0.1.0", goos, goarch); err == nil {
				t.Fatalf("expected error for %s/%s", goos, goarch)
			}
		}
	}
}
