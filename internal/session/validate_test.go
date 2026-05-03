package session

import "testing"

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
