// Package minisig signs and verifies files in the format of minisign
// (https://jedisct1.github.io/minisign/): Ed25519 over the BLAKE2b-512 of the
// file, and a second signature that binds a trusted comment to the first. A
// signature made here verifies with `minisign -Vm <file> -P <public key>`.
//
// The secret key is not minisign's encrypted file: it is held by a CI secret,
// and this package reads it in its own plain form.
package minisig

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/blake2b"
)

const (
	keyIDSize = 8
	// algorithm names the signature minisign makes by default: over the
	// BLAKE2b-512 of the file. Two more bytes in front of the key id.
	algorithm = "ED"
	// keyAlgorithm is what a public key starts with, whichever signature the
	// key makes.
	keyAlgorithm = "Ed"

	untrustedPrefix = "untrusted comment: "
	trustedPrefix   = "trusted comment: "
)

// ReleaseComment is the trusted comment of a release's digest list. It binds
// the signature to the tag: a signed list of an old release, published again
// under a new tag, does not verify.
func ReleaseComment(tag string) string {
	return "release " + tag
}

// PublicKey is a key that verifies signatures.
type PublicKey struct {
	id  [keyIDSize]byte
	key ed25519.PublicKey
}

// ParsePublicKey reads the base64 line of a minisign public key.
func ParsePublicKey(s string) (PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return PublicKey{}, fmt.Errorf("public key: %w", err)
	}
	if len(raw) != len(keyAlgorithm)+keyIDSize+ed25519.PublicKeySize || string(raw[:len(keyAlgorithm)]) != keyAlgorithm {
		return PublicKey{}, errors.New("public key: not a minisign key")
	}
	var pub PublicKey
	copy(pub.id[:], raw[len(keyAlgorithm):])
	pub.key = bytes.Clone(raw[len(keyAlgorithm)+keyIDSize:])
	return pub, nil
}

// String is the base64 line ParsePublicKey reads.
func (k PublicKey) String() string {
	raw := append([]byte(keyAlgorithm), k.id[:]...)
	return base64.StdEncoding.EncodeToString(append(raw, k.key...))
}

// Verify checks a signature file against the message and returns its trusted
// comment, which is only meaningful once this returned no error.
func Verify(pub PublicKey, message, signature []byte) (string, error) {
	lines := strings.Split(strings.ReplaceAll(string(signature), "\r\n", "\n"), "\n")
	if len(lines) < 4 || !strings.HasPrefix(lines[0], untrustedPrefix) || !strings.HasPrefix(lines[2], trustedPrefix) {
		return "", errors.New("signature: not a minisign signature")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[1]))
	if err != nil || len(raw) != len(algorithm)+keyIDSize+ed25519.SignatureSize {
		return "", errors.New("signature: not a minisign signature")
	}
	if string(raw[:len(algorithm)]) != algorithm {
		return "", fmt.Errorf("signature: algorithm %q is not %q", raw[:len(algorithm)], algorithm)
	}
	if !bytes.Equal(raw[len(algorithm):len(algorithm)+keyIDSize], pub.id[:]) {
		return "", errors.New("signature: made by another key")
	}
	sig := raw[len(algorithm)+keyIDSize:]
	digest := blake2b.Sum512(message)
	if !ed25519.Verify(pub.key, digest[:], sig) {
		return "", errors.New("signature: does not match the file")
	}
	trusted := strings.TrimPrefix(lines[2], trustedPrefix)
	global, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[3]))
	if err != nil || len(global) != ed25519.SignatureSize {
		return "", errors.New("signature: not a minisign signature")
	}
	if !ed25519.Verify(pub.key, append(bytes.Clone(sig), trusted...), global) {
		return "", errors.New("signature: does not match its trusted comment")
	}
	return trusted, nil
}

// GenerateKey makes a key pair and returns the secret key in the plain form
// Sign reads.
func GenerateKey() (secret string, pub PublicKey, err error) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", PublicKey{}, err
	}
	if _, err := rand.Read(pub.id[:]); err != nil {
		return "", PublicKey{}, err
	}
	pub.key = public
	return base64.StdEncoding.EncodeToString(append(bytes.Clone(pub.id[:]), private.Seed()...)), pub, nil
}

// Sign makes the signature file of a message under a secret from GenerateKey.
func Sign(secret string, message []byte, trustedComment string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(secret))
	if err != nil || len(raw) != keyIDSize+ed25519.SeedSize {
		return nil, errors.New("secret key: not a key this package made")
	}
	if strings.ContainsAny(trustedComment, "\r\n") {
		return nil, errors.New("trusted comment: must be one line")
	}
	private := ed25519.NewKeyFromSeed(raw[keyIDSize:])
	digest := blake2b.Sum512(message)
	sig := ed25519.Sign(private, digest[:])
	head := append([]byte(algorithm), raw[:keyIDSize]...)
	global := ed25519.Sign(private, append(bytes.Clone(sig), trustedComment...))
	return fmt.Appendf(nil, "%ssignature from coddy release signing\n%s\n%s%s\n%s\n",
		untrustedPrefix,
		base64.StdEncoding.EncodeToString(append(head, sig...)),
		trustedPrefix, trustedComment,
		base64.StdEncoding.EncodeToString(global),
	), nil
}
