//go:build http

package httpserver

import (
	"net/http"
	"strings"
	"testing"
)

// The title model sees the words of a first message and nothing it carries: an
// image sent with "What is in this picture?" never reaches it. The prompt says
// so, and tells it to name the request rather than answer it.
func TestCoddyDescribeTellsTheModelItNamesTheRequestWithoutItsAttachments(t *testing.T) {
	model := &scriptedTitleModel{reply: "What the picture shows\ntags: images"}
	ts, _, _ := describeServer(t, model)
	status, short, _ := postDescribe(t, ts, "What is in this picture?", nil, nil)
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if short != "What the picture shows" {
		t.Fatalf("short = %q", short)
	}
	if !strings.Contains(model.system, describeRequestGuidance) {
		t.Fatalf("the title prompt does not say the attachments are not shown:\n%s", model.system)
	}
}

// A model that answers the message, or refuses it for want of the image, has
// not named the chat: the title falls back to the user's own words, and the
// tags it did give are kept.
func TestCoddyDescribeAnswerOrRefusalFallsBackToTheUsersWords(t *testing.T) {
	model := &scriptedTitleModel{reply: "No image has been provided in your message, so I'm unable to describe it.\ntags: images"}
	ts, _, _ := describeServer(t, model)
	status, short, tags := postDescribe(t, ts, "What is in this picture?", nil, nil)
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if short != "What is in this picture?" {
		t.Fatalf("short = %q, want the user's words", short)
	}
	if len(tags) != 1 || tags[0] != "images" {
		t.Fatalf("tags = %v", tags)
	}
}

func TestDescribePickPhraseRejectsAnswerSentences(t *testing.T) {
	user := strings.Fields("What is in this picture?")
	want := "What is in this picture?"
	for _, reply := range []string{
		"No image has been provided in your message, so I'm unable to",
		"I'm unable to see the image you attached",
		"I can't view images, please describe it",
		"I cannot see any picture here",
		"Sorry, I don't see an attachment in your message",
		"Unfortunately there is no image in this conversation",
		"There is no image attached to your message",
		"Please provide the image you want described",
		"As an AI, I cannot view pictures",
		"К сожалению, я не вижу изображения в сообщении",
		"Я не могу посмотреть картинку",
	} {
		if got := describePickPhraseFromLLM(reply, user); got != want {
			t.Errorf("reply %q: title %q, want %q", reply, got, want)
		}
	}
	// A refusal next to a real phrase leaves the phrase.
	if got := describePickPhraseFromLLM("I'm unable to see the image\nWhat the picture shows", user); got != "What the picture shows" {
		t.Errorf("title %q, want the phrase next to the refusal", got)
	}
	// Titles that merely contain such words are titles.
	for _, title := range []string{
		"Image upload not provided error",
		"Fix sorry page layout",
		"Inline images in the composer",
	} {
		if got := describePickPhraseFromLLM(title, user); got != title {
			t.Errorf("title %q was rejected as an answer (got %q)", title, got)
		}
	}
}
