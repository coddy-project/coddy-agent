package update

import "testing"

func TestNormalizeSemver(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want string
	}{
		{"0.9.3", "0.9.3"},
		{"v0.9.3", "0.9.3"},
		{"0.9.2-5-gb6b7d31-dirty", "0.9.2"},
		{"dev", ""},
		{"", ""},
	}
	for _, tc := range tests {
		if got := NormalizeSemver(tc.in); got != tc.want {
			t.Errorf("NormalizeSemver(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCompareSemver(t *testing.T) {
	t.Parallel()
	if CompareSemver("0.9.2", "0.9.3") >= 0 {
		t.Fatal("0.9.2 should be less than 0.9.3")
	}
	if CompareSemver("0.9.3", "0.9.3") != 0 {
		t.Fatal("equal versions should compare to 0")
	}
	if CompareSemver("1.0.0", "0.9.9") <= 0 {
		t.Fatal("1.0.0 should be greater than 0.9.9")
	}
	if CompareSemver("0.9.2-5-gdirty", "0.9.3") >= 0 {
		t.Fatal("dirty 0.9.2 should be less than 0.9.3")
	}
}
