// Package webauth carries the credentials of the optional web sign-in: the
// argon2id password hash, the server-side browser sessions it opens, and the
// per-address throttle that makes guessing expensive.
//
// It is deliberately untagged. The HTTP surface that uses it is behind the
// `http` build tag, but `coddy serve set-password` is an ordinary command in
// every build, and both have to agree on the format of a hash down to the byte.
package webauth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// HashParams are the argon2id cost parameters a new hash is written with.
//
// The numbers are OWASP's recommended argon2id configuration (19 MiB, two
// passes, one lane): expensive enough that an offline guess costs real memory,
// cheap enough that a sign-in on a small VPS answers in tens of milliseconds
// and that a burst of attempts cannot exhaust the box's RAM. A hash carries its
// own parameters, so raising them later keeps verifying the hashes already on
// disk.
type HashParams struct {
	// Memory is the argon2id memory cost in KiB.
	Memory uint32
	// Time is the number of passes over that memory.
	Time uint32
	// Threads is the degree of parallelism.
	Threads uint8
	// SaltLen and KeyLen are the byte lengths of the salt and the derived key.
	SaltLen uint32
	KeyLen  uint32
}

// DefaultHashParams is what `coddy serve set-password` and an environment
// password are hashed with.
var DefaultHashParams = HashParams{Memory: 19 * 1024, Time: 2, Threads: 1, SaltLen: 16, KeyLen: 32}

// ErrMalformedHash is returned when a stored credential is not an argon2id hash
// this build can read. It is reported as a configuration problem, never as a
// wrong password, so an operator who pasted half a hash learns which it was.
var ErrMalformedHash = errors.New("not a valid argon2id password hash")

// HashPassword derives an argon2id hash of plain in the PHC string format
// ("$argon2id$v=19$m=...,t=...,p=...$<salt>$<key>"), the same shape passlib,
// Django and the argon2 reference CLI produce, so a hash may also come from
// somewhere else.
func HashPassword(plain string) (string, error) { return HashPasswordWith(plain, DefaultHashParams) }

// HashPasswordWith is HashPassword with explicit cost parameters (tests use the
// cheapest ones that still exercise the format).
func HashPasswordWith(plain string, p HashParams) (string, error) {
	if plain == "" {
		return "", errors.New("password is empty")
	}
	if p.SaltLen == 0 || p.KeyLen == 0 || p.Memory == 0 || p.Time == 0 || p.Threads == 0 {
		return "", errors.New("invalid argon2id parameters")
	}
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("salt: %w", err)
	}
	acquireHashSlot()
	key := argon2.IDKey([]byte(plain), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	releaseHashSlot()
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// hashSlots bounds how many argon2id derivations run at once.
//
// Each one allocates DefaultHashParams.Memory (19 MiB), which is the point of a
// memory-hard hash - and also the reason a flood of sign-in attempts could
// otherwise ask a small server for gigabytes at a stroke. Four at a time keeps
// the cost of a wrong password where it belongs, on the attacker's wall clock,
// rather than on the machine's memory. The per-address throttle in throttle.go
// is the other half of that answer.
var hashSlots = make(chan struct{}, 4)

func acquireHashSlot() { hashSlots <- struct{}{} }
func releaseHashSlot() { <-hashSlots }

// VerifyPassword reports whether plain is the password behind encoded.
//
// A false with a nil error is a wrong password; a non-nil error means the
// stored hash itself cannot be read, which is an operator's mistake rather than
// a visitor's.
func VerifyPassword(encoded, plain string) (bool, error) {
	params, salt, want, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}
	acquireHashSlot()
	got := argon2.IDKey([]byte(plain), salt, params.Time, params.Memory, params.Threads, uint32(len(want)))
	releaseHashSlot()
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// IsHash reports whether s looks like an argon2id hash this build can verify.
// `coddy serve set-password` uses it to refuse writing something that would
// only fail at the sign-in form.
func IsHash(s string) bool {
	_, _, _, err := decodeHash(s)
	return err == nil
}

func decodeHash(encoded string) (HashParams, []byte, []byte, error) {
	var p HashParams
	parts := strings.Split(strings.TrimSpace(encoded), "$")
	// "", "argon2id", "v=19", "m=..,t=..,p=..", salt, key
	if len(parts) != 6 || parts[0] != "" {
		return p, nil, nil, ErrMalformedHash
	}
	if parts[1] != "argon2id" {
		return p, nil, nil, fmt.Errorf("%w: unsupported algorithm %q", ErrMalformedHash, parts[1])
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return p, nil, nil, ErrMalformedHash
	}
	if version != argon2.Version {
		return p, nil, nil, fmt.Errorf("%w: unsupported version %d", ErrMalformedHash, version)
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil {
		return p, nil, nil, ErrMalformedHash
	}
	if p.Memory == 0 || p.Time == 0 || p.Threads == 0 {
		return p, nil, nil, ErrMalformedHash
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return p, nil, nil, ErrMalformedHash
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return p, nil, nil, ErrMalformedHash
	}
	p.SaltLen = uint32(len(salt))
	p.KeyLen = uint32(len(key))
	return p, salt, key, nil
}
