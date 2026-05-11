//go:build scheduler

package schedulerops

import "testing"

func TestValidateJobID(t *testing.T) {
	cases := []struct {
		id      string
		wantErr bool
	}{
		{"nightly", false},
		{"", true},
		{"a/b", true},
		{"..", true},
		{".hidden", true},
	}
	for _, tc := range cases {
		err := ValidateJobID(tc.id)
		if tc.wantErr && err == nil {
			t.Fatalf("%q: want error", tc.id)
		}
		if !tc.wantErr && err != nil {
			t.Fatalf("%q: %v", tc.id, err)
		}
	}
}
