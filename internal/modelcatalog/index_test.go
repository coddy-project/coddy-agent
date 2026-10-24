package modelcatalog

import (
	"strings"
	"testing"
)

func TestParseKeepsWhatASuggestionNeeds(t *testing.T) {
	idx := fixture(t)
	if got := idx["openai"].Models["gpt-5"]; got != (Entry{Context: 400000, Input: 272000, Output: 128000, Image: true}) {
		t.Errorf("gpt-5 = %+v", got)
	}
	if got := idx["openai"].Models["text-only"]; got.Image {
		t.Errorf("a text-only model reads images: %+v", got)
	}
	if got := idx["openrouter"].API; got != "https://openrouter.ai/api/v1" {
		t.Errorf("openrouter api = %q", got)
	}
}

// One broken entry costs only itself: the provider and its other models stay.
func TestParseDropsBrokenModelsOneByOne(t *testing.T) {
	broken, ok := fixture(t)["broken"]
	if !ok {
		t.Fatal("a provider with broken models was dropped whole")
	}
	for _, id := range []string{"words", "negative", "absurd", "fraction", "no-limit"} {
		if e, ok := broken.Models[id]; ok {
			t.Errorf("model %q with no usable window was kept: %+v", id, e)
		}
	}
	if got := broken.Models["fine"]; got != (Entry{Context: 8192}) {
		t.Errorf("a broken output took its model with it or survived: %+v", got)
	}
	if got := broken.Models["input-too-big"]; got != (Entry{Context: 1000, Output: 100}) {
		t.Errorf("an input part larger than the window was kept: %+v", got)
	}
}

func TestParseSkipsWhatIsNotAProvider(t *testing.T) {
	idx := fixture(t)
	for _, id := range []string{"not-a-provider", "empty"} {
		if _, ok := idx[id]; ok {
			t.Errorf("%q is in the index", id)
		}
	}
}

func TestParseRefusesWhatIsNotACatalog(t *testing.T) {
	for _, doc := range []string{"", "[]", "<html>", `{"openai":`} {
		if _, err := Parse(strings.NewReader(doc)); err == nil {
			t.Errorf("Parse(%q) succeeded", doc)
		}
	}
}
