package llm

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"strconv"
	"strings"
)

// The reasoning signature envelope of the coddy wire.
//
// A reasoning signature (Anthropic thinking, the Codex reasoning carrier) is
// valid only for the model that issued it. The remote that shares a model
// under an alias can point that alias at another model later, and a history
// that already holds signatures would then be refused by the new model on
// every call. So a signature leaves the remote inside an envelope that names
// the model it came from by a tag, and the remote opens the envelope on the way
// back: a signature of another model, or one that never had an envelope, is
// dropped before the provider is built and is never an error.
//
// The functions are pure: the key material is passed in by the server, which
// owns it. The tag is derived only from the model the alias points at, not
// from its capabilities, so widening a row does not invalidate a history.

const (
	sigEnvelopeVersion = "coddy1"
	// tokenBytes is how much of an HMAC the opaque tokens keep: 12 bytes are
	// 16 base64url characters.
	tokenBytes = 12
	// sigChecksumBytes guards an envelope against corruption on its way
	// through a transcript; it is not a secret and proves nothing about who
	// wrote it, the upstream provider checks the signature itself.
	sigChecksumBytes = 8
)

// ModelTag is the opaque tag of the model an alias points at: a keyed hash, so
// the upstream model id cannot be recovered from it, and the same before and
// after a restart of the remote while the key and the model stay put. The
// caller passes whatever names the model a signature is valid for (provider
// and model id), and a key that outlives the process.
func ModelTag(key []byte, upstreamModelID string) string {
	return keyedToken(key, "coddy-model-tag/1", upstreamModelID)
}

// Revision is the opaque revision of a shared row: a keyed hash of every
// field a client can observe about it and of the model the alias points at,
// the same before and after a restart of the remote while nothing changed.
func Revision(key []byte, fields ...string) string {
	return keyedToken(key, "coddy-revision/1", fields...)
}

// keyedToken hashes the domain and the length-prefixed fields under the key, so
// a tag and a revision never collide and moving a character from one field to
// the next is another value.
func keyedToken(key []byte, domain string, fields ...string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(domain))
	for _, f := range fields {
		mac.Write([]byte{0})
		mac.Write([]byte(strconv.Itoa(len(f))))
		mac.Write([]byte{':'})
		mac.Write([]byte(f))
	}
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:tokenBytes])
}

// SealReasoningSignature wraps a provider's raw signature in an envelope tagged
// with the model that issued it. An empty signature has nothing to wrap and
// stays empty.
func SealReasoningSignature(raw, modelTag string) string {
	if raw == "" {
		return ""
	}
	return strings.Join([]string{
		sigEnvelopeVersion,
		modelTag,
		base64.RawURLEncoding.EncodeToString([]byte(raw)),
		sigChecksum(modelTag, raw),
	}, ":")
}

// OpenReasoningSignature unwraps an envelope for the model whose tag is
// currentTag. It reports ok only for an envelope of this version, tagged with
// currentTag and intact; anything else (no envelope, another model's tag, a
// damaged one) returns "" and false, and the caller drops the signature.
func OpenReasoningSignature(envelope, currentTag string) (string, bool) {
	if envelope == "" || currentTag == "" {
		return "", false
	}
	parts := strings.Split(envelope, ":")
	if len(parts) != 4 || parts[0] != sigEnvelopeVersion {
		return "", false
	}
	if subtle.ConstantTimeCompare([]byte(parts[1]), []byte(currentTag)) != 1 {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(raw) == 0 {
		return "", false
	}
	if subtle.ConstantTimeCompare([]byte(parts[3]), []byte(sigChecksum(parts[1], string(raw)))) != 1 {
		return "", false
	}
	return string(raw), true
}

func sigChecksum(tag, raw string) string {
	sum := sha256.Sum256([]byte(tag + "\x00" + raw))
	return base64.RawURLEncoding.EncodeToString(sum[:sigChecksumBytes])
}

// OpenMessageSignatures opens, in place, the envelope of every reasoning
// signature in msgs for the model whose tag is currentTag. A signature whose
// envelope belongs to another model, that is damaged, or that never had one is
// cleared, never refused: the provider then replays the thinking block without
// it (the Anthropic provider omits one that has no signature) and the call
// succeeds.
func OpenMessageSignatures(msgs []Message, currentTag string) {
	for i := range msgs {
		if msgs[i].ReasoningSignature == "" {
			continue
		}
		raw, _ := OpenReasoningSignature(msgs[i].ReasoningSignature, currentTag)
		msgs[i].ReasoningSignature = raw
	}
}

// SealResponseSignature returns resp with its reasoning signature sealed for
// the wire; resp itself is not changed.
func SealResponseSignature(resp *Response, modelTag string) *Response {
	if resp == nil {
		return nil
	}
	sealed := *resp
	sealed.ReasoningSignature = SealReasoningSignature(resp.ReasoningSignature, modelTag)
	return &sealed
}
