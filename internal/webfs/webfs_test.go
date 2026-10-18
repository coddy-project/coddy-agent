package webfs

import "testing"

func TestHasDotSegmentSeesEveryKindOfSeparator(t *testing.T) {
	for p, want := range map[string]bool{
		"/site/index.html": false,
		"/site/.env":       true,
		"/.git/config":     true,
		`/assets\.env`:     true,
		`/a\b/c.js`:        false,
		"/a/./b":           true,
	} {
		if got := HasDotSegment(p); got != want {
			t.Errorf("HasDotSegment(%q) = %v, want %v", p, got, want)
		}
	}
}
