package agent

import (
	"reflect"
	"testing"
)

func TestParsePluginCommand(t *testing.T) {
	tests := []struct {
		in     string
		want   []string
		wantOK bool
	}{
		{"/plugin", nil, true},
		{"  /plugin  ", nil, true},
		{"/plugin marketplace list", []string{"marketplace", "list"}, true},
		{"/plugin   install   owner/repo", []string{"install", "owner/repo"}, true},
		{"/plugin\tmarketplace\tadd x", []string{"marketplace", "add", "x"}, true},
		{"/plugincast", nil, false},   // must not match a longer word
		{"say /plugin later", nil, false}, // only when it leads the message
		{"hello world", nil, false},
	}
	for _, tc := range tests {
		got, ok := parsePluginCommand(tc.in)
		if ok != tc.wantOK {
			t.Errorf("parsePluginCommand(%q) ok=%v, want %v", tc.in, ok, tc.wantOK)
			continue
		}
		if ok && !reflect.DeepEqual(got, tc.want) {
			t.Errorf("parsePluginCommand(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
