package session

import (
	"strings"
	"testing"
)

func TestValidateFolderSessionID(t *testing.T) {
	if err := ValidateFolderSessionID(`sess_deadbeefdeadbeefdeadbeefdead`); err != nil {
		t.Fatal(err)
	}
	if err := ValidateFolderSessionID(`bad/id`); err == nil {
		t.Fatal(`expected rejection for slash`)
	}
	if err := ValidateFolderSessionID(`.hidden`); err == nil {
		t.Fatal(`expected rejection for dot prefix`)
	}
}

func TestValidateToolCallID(t *testing.T) {
	for _, id := range []string{"call_abc123", "toolu_01A09q90qw90lq917835lq9", "chatcmpl-tool.9f0e", "0", "a-b_c.d"} {
		if err := ValidateToolCallID(id); err != nil {
			t.Fatalf("ValidateToolCallID(%q): %v", id, err)
		}
	}
	for _, id := range []string{
		``,
		`   `,
		`../x`,
		`../../escaped`,
		`a/b`,
		`/abs`,
		`a\b`,
		`.hidden`,
		`.`,
		`..`,
		`a:b`,
		`a b`,
		`a*b`,
		`a` + "\x00" + `b`,
		strings.Repeat("z", 300),
	} {
		if err := ValidateToolCallID(id); err == nil {
			t.Fatalf("ValidateToolCallID(%q): expected a rejection", id)
		}
	}
}
