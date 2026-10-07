package llm

import (
	"strings"
	"testing"
)

func TestSignatureEnvelopeRoundTrip(t *testing.T) {
	tag := ModelTag([]byte("remote-secret"), "anthropic/claude-x")
	for _, raw := range []string{
		"sig-abc",
		`{"devin":true,"model":"m","signature":"s/+=="}`,
		strings.Repeat("A", 64<<10),
		"with : colons : inside",
	} {
		sealed := SealReasoningSignature(raw, tag)
		if sealed == "" || sealed == raw {
			t.Fatalf("sealed %q is not an envelope", sealed)
		}
		got, ok := OpenReasoningSignature(sealed, tag)
		if !ok || got != raw {
			t.Fatalf("open(seal(%q)) = %q, %v", raw[:min(len(raw), 20)], got[:min(len(got), 20)], ok)
		}
	}
}

func TestSignatureEnvelopeHidesNothingAboutTheRawSignatureButIsOpaque(t *testing.T) {
	tag := ModelTag([]byte("k"), "m")
	sealed := SealReasoningSignature("sig-abc", tag)
	if strings.ContainsAny(sealed, " \t\r\n\"\\") {
		t.Fatalf("an envelope is one token safe in JSON: %q", sealed)
	}
}

func TestSignatureEnvelopeOfAnotherModelIsDropped(t *testing.T) {
	key := []byte("remote-secret")
	older := ModelTag(key, "anthropic/claude-old")
	current := ModelTag(key, "anthropic/claude-new")
	if older == current {
		t.Fatal("two models share a tag")
	}
	sealed := SealReasoningSignature("sig-abc", older)
	if raw, ok := OpenReasoningSignature(sealed, current); ok || raw != "" {
		t.Fatalf("a signature of another model must be dropped, got %q, %v", raw, ok)
	}
}

func TestSignatureWithoutAnEnvelopeIsDropped(t *testing.T) {
	tag := ModelTag([]byte("k"), "m")
	for _, raw := range []string{"", "sig-abc", "coddy1", "coddy1:" + tag, "coddy1::", "coddy1:x:y:z:w"} {
		if got, ok := OpenReasoningSignature(raw, tag); ok || got != "" {
			t.Errorf("open(%q) = %q, %v; want a drop", raw, got, ok)
		}
	}
	if SealReasoningSignature("", tag) != "" {
		t.Error("nothing to seal gives nothing")
	}
}

func TestSignatureEnvelopeTamperIsDropped(t *testing.T) {
	tag := ModelTag([]byte("k"), "m")
	sealed := SealReasoningSignature("sig-abc-0123456789", tag)
	parts := strings.Split(sealed, ":")
	if len(parts) != 4 {
		t.Fatalf("envelope shape: %q", sealed)
	}
	flip := func(s string) string {
		b := []byte(s)
		if b[0] == 'A' {
			b[0] = 'B'
		} else {
			b[0] = 'A'
		}
		return string(b)
	}
	for name, tampered := range map[string]string{
		"payload":  strings.Join([]string{parts[0], parts[1], flip(parts[2]), parts[3]}, ":"),
		"checksum": strings.Join([]string{parts[0], parts[1], parts[2], flip(parts[3])}, ":"),
		"tag":      strings.Join([]string{parts[0], flip(parts[1]), parts[2], parts[3]}, ":"),
		"cut":      sealed[:len(sealed)-3],
		"extended": sealed + "AA",
		"version":  "coddy9" + strings.TrimPrefix(sealed, "coddy1"),
	} {
		if got, ok := OpenReasoningSignature(tampered, tag); ok {
			t.Errorf("%s tamper was accepted: %q", name, got)
		}
	}
}

func TestModelTagAndRevisionAreDeterministicAndKeyed(t *testing.T) {
	key := []byte("remote-secret")
	first, second := ModelTag(key, "m"), ModelTag(key, "m")
	if first != second {
		t.Fatal("tag is not deterministic")
	}
	if ModelTag(key, "m") == ModelTag([]byte("other"), "m") {
		t.Fatal("tag does not depend on the key")
	}
	if ModelTag(key, "m") == ModelTag(key, "m2") {
		t.Fatal("tag does not depend on the model")
	}
	if strings.Contains(ModelTag(key, "qwen3-secret"), "qwen") {
		t.Fatal("tag leaks the model id")
	}
	r := Revision(key, "coder", "200000", "low,high")
	if r != Revision(key, "coder", "200000", "low,high") {
		t.Fatal("revision is not deterministic")
	}
	if r == Revision(key, "coder", "100000", "low,high") {
		t.Fatal("revision ignores a field")
	}
	if r == Revision([]byte("other"), "coder", "200000", "low,high") {
		t.Fatal("revision does not depend on the key")
	}
	// Field boundaries count: moving a character across them is another row.
	if Revision(key, "ab", "c") == Revision(key, "a", "bc") {
		t.Fatal("revision confuses field boundaries")
	}
	if Revision(key, "x") == ModelTag(key, "x") {
		t.Fatal("tag and revision share a domain")
	}
	// An opaque, URL-safe token of a fixed short length.
	for _, s := range []string{r, ModelTag(key, "m")} {
		if len(s) != 16 || strings.ContainsAny(s, "+/=:") {
			t.Fatalf("token %q is not 16 base64url characters", s)
		}
	}
}
