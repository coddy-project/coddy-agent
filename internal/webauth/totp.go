package webauth

// Time-based one-time codes (RFC 6238) for the web sign-in: the second factor
// an authenticator app on the operator's phone answers with. The parameters are
// the ones every app agrees on - HMAC-SHA1, six digits, a 30-second step - so a
// code from Google Authenticator, FreeOTP, Aegis or a password manager is read
// the same way, and nothing here needs a library.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // G505: RFC 6238 defines TOTP over HMAC-SHA1, the only algorithm every authenticator app reads
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// TOTPDigits is the length of a code.
	TOTPDigits = 6
	// TOTPPeriod is the time step a code is valid for.
	TOTPPeriod = 30 * time.Second
	// totpSkew is how many steps either side of now a code is still taken in,
	// so a phone whose clock is half a minute off still signs in.
	totpSkew = 1
	// totpSecretBytes is the size of a generated secret: 160 bits, the size
	// RFC 4226 recommends and the one the apps expect from an HMAC-SHA1 key.
	totpSecretBytes = 20
	// minTOTPSecretBytes is the shortest secret accepted from a file: 80 bits,
	// the minimum RFC 4226 allows.
	minTOTPSecretBytes = 10
)

// TOTPIssuer is the name an authenticator app files the account under.
const TOTPIssuer = "Coddy"

// TOTPImageURL is the picture the enrolment link offers for the account. Only
// some apps read it (FreeOTP does); the rest choose their own icon by issuer
// and ignore the parameter, so it costs nothing where it is not understood.
const TOTPImageURL = "https://raw.githubusercontent.com/coddy-project/coddy-agent/main/external/ui/public/icon-512.png"

// totpEncoding is base32 without padding, the spelling of a secret in an
// otpauth link and the one people type into an app by hand.
var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateTOTPSecret returns a fresh random secret in base32.
func GenerateTOTPSecret() (string, error) {
	buf := make([]byte, totpSecretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate secret: %w", err)
	}
	return totpEncoding.EncodeToString(buf), nil
}

// NormalizeTOTPSecret is the canonical spelling of a secret: upper case, no
// spaces or dashes (apps print secrets in groups) and no padding.
func NormalizeTOTPSecret(secret string) string {
	s := strings.ToUpper(strings.TrimSpace(secret))
	s = strings.NewReplacer(" ", "", "-", "", "=", "").Replace(s)
	return s
}

// decodeTOTPSecret reads a secret into the HMAC key.
func decodeTOTPSecret(secret string) ([]byte, error) {
	key, err := totpEncoding.DecodeString(NormalizeTOTPSecret(secret))
	if err != nil {
		return nil, errors.New("not a base32 string")
	}
	if len(key) < minTOTPSecretBytes {
		return nil, fmt.Errorf("shorter than %d bits", minTOTPSecretBytes*8)
	}
	return key, nil
}

// ValidateTOTPSecret reports a secret no code could be checked against.
func ValidateTOTPSecret(secret string) error {
	_, err := decodeTOTPSecret(secret)
	return err
}

// totpStep is the counter of the step t falls into.
func totpStep(t time.Time) int64 {
	return t.Unix() / int64(TOTPPeriod/time.Second)
}

// hotp is RFC 4226's truncation of one counter value into a code.
func hotp(key []byte, counter int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(counter)) //nolint:gosec // G115: a step counter is never negative
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", TOTPDigits, bin%1_000_000)
}

// TOTPCode is the code an app shows at t for secret.
func TOTPCode(secret string, t time.Time) (string, error) {
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return "", err
	}
	return hotp(key, totpStep(t)), nil
}

// NormalizeTOTPCode drops the spaces an app or a person puts in the middle of
// a code ("123 456").
func NormalizeTOTPCode(code string) string {
	return strings.Join(strings.Fields(code), "")
}

// VerifyTOTP checks code against secret around now and returns the step it
// matched. Every candidate step is compared, in constant time, so the answer
// takes as long for a code that is one step off as for one that is wrong.
func VerifyTOTP(secret, code string, now time.Time) (int64, bool) {
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return 0, false
	}
	code = NormalizeTOTPCode(code)
	if len(code) != TOTPDigits {
		return 0, false
	}
	base := totpStep(now)
	matched, found := int64(0), false
	for d := int64(-totpSkew); d <= totpSkew; d++ {
		step := base + d
		if subtle.ConstantTimeCompare([]byte(hotp(key, step)), []byte(code)) == 1 && !found {
			matched, found = step, true
		}
	}
	return matched, found
}

// TOTPKeyURI is the otpauth link an app reads off the QR code: the issuer and
// the account name it shows, the secret, the parameters spelled out for the
// apps that do not assume them, and the picture for the apps that take one.
func TOTPKeyURI(account, secret string) string {
	label := url.PathEscape(TOTPIssuer) + ":" + url.PathEscape(account)
	q := url.Values{}
	q.Set("secret", NormalizeTOTPSecret(secret))
	q.Set("issuer", TOTPIssuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(TOTPDigits))
	q.Set("period", fmt.Sprint(int(TOTPPeriod/time.Second)))
	q.Set("image", TOTPImageURL)
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// TOTPReplayGuard remembers the last step each secret was accepted for, so a
// code read over the operator's shoulder, or replayed from a captured request,
// does not open a second session inside the same half minute.
//
// It is kept in memory: a restart forgets it, and the code it would have
// refused is at most a minute and a half old by then.
type TOTPReplayGuard struct {
	mu   sync.Mutex
	last map[string]int64
}

// Accept records step for the secret identified by key and reports whether it
// is newer than every step accepted for it before.
func (g *TOTPReplayGuard) Accept(key string, step int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.last == nil {
		g.last = make(map[string]int64)
	}
	if prev, ok := g.last[key]; ok && step <= prev {
		return false
	}
	g.last[key] = step
	return true
}
