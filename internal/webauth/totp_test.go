package webauth

import (
	"encoding/base32"
	"net/url"
	"strings"
	"testing"
	"time"
)

// rfcSecret is the SHA-1 key of RFC 6238's test vectors, "12345678901234567890".
var rfcSecret = base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))

func TestTOTPCodeMatchesRFC6238Vectors(t *testing.T) {
	// The RFC prints eight digits; the six an app shows are their last six.
	cases := []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	}
	for _, c := range cases {
		got, err := TOTPCode(rfcSecret, time.Unix(c.unix, 0))
		if err != nil {
			t.Fatalf("TOTPCode(%d): %v", c.unix, err)
		}
		if got != c.want {
			t.Errorf("TOTPCode(%d) = %s, want %s", c.unix, got, c.want)
		}
	}
}

func TestVerifyTOTPAcceptsOneStepEitherSide(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	for _, d := range []time.Duration{-TOTPPeriod, 0, TOTPPeriod} {
		code, _ := TOTPCode(rfcSecret, now.Add(d))
		step, ok := VerifyTOTP(rfcSecret, code, now)
		if !ok {
			t.Fatalf("code from %v away was refused", d)
		}
		if want := totpStep(now.Add(d)); step != want {
			t.Fatalf("matched step %d, want %d", step, want)
		}
	}
	for _, d := range []time.Duration{-2 * TOTPPeriod, 2 * TOTPPeriod} {
		code, _ := TOTPCode(rfcSecret, now.Add(d))
		if _, ok := VerifyTOTP(rfcSecret, code, now); ok {
			t.Fatalf("code from %v away was accepted", d)
		}
	}
}

func TestVerifyTOTPRefusesMalformedInput(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	code, _ := TOTPCode(rfcSecret, now)
	cases := map[string]struct{ secret, code string }{
		"empty code":         {rfcSecret, ""},
		"short code":         {rfcSecret, code[:5]},
		"long code":          {rfcSecret, code + "0"},
		"letters":            {rfcSecret, "abcdef"},
		"empty secret":       {"", code},
		"not base32":         {"not-a-secret!", code},
		"too short a secret": {"GEZDGNBV", code},
	}
	for name, c := range cases {
		if _, ok := VerifyTOTP(c.secret, c.code, now); ok {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestVerifyTOTPReadsSpacedCodesAndSecrets(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	code, _ := TOTPCode(rfcSecret, now)
	spaced := strings.ToLower(rfcSecret[:4] + " " + rfcSecret[4:8] + "-" + rfcSecret[8:])
	if _, ok := VerifyTOTP(spaced, code[:3]+" "+code[3:], now); !ok {
		t.Fatal("a secret and a code in groups were refused")
	}
}

func TestGenerateTOTPSecretIsFreshAndValid(t *testing.T) {
	a, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := GenerateTOTPSecret()
	if a == b {
		t.Fatal("two secrets were equal")
	}
	if err := ValidateTOTPSecret(a); err != nil {
		t.Fatalf("generated secret invalid: %v", err)
	}
	if len(a) != 32 {
		t.Fatalf("secret %q is not 160 bits of base32", a)
	}
}

func TestTOTPKeyURICarriesIssuerAccountAndImage(t *testing.T) {
	raw := TOTPKeyURI("ops team", "jbsw y3dp ehpk 3pxp")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "otpauth" || u.Host != "totp" {
		t.Fatalf("not an otpauth totp link: %s", raw)
	}
	if u.Path != "/Coddy:ops team" {
		t.Fatalf("label = %q", u.Path)
	}
	q := u.Query()
	want := map[string]string{
		"secret": "JBSWY3DPEHPK3PXP", "issuer": "Coddy", "algorithm": "SHA1",
		"digits": "6", "period": "30", "image": TOTPImageURL,
	}
	for k, v := range want {
		if got := q.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

func TestTOTPReplayGuardRefusesTheSameOrAnOlderStep(t *testing.T) {
	var g TOTPReplayGuard
	if !g.Accept("a", 10) {
		t.Fatal("first step refused")
	}
	if g.Accept("a", 10) {
		t.Fatal("the same step was accepted twice")
	}
	if g.Accept("a", 9) {
		t.Fatal("an older step was accepted")
	}
	if !g.Accept("a", 11) || !g.Accept("b", 10) {
		t.Fatal("a newer step or another secret was refused")
	}
}

func TestAccountFingerprintFollowsTheSecondFactor(t *testing.T) {
	plain := CredentialFingerprint("operator", "hash")
	if got := AccountFingerprint("operator", "hash", ""); got != plain {
		t.Fatal("an account without codes changed its fingerprint")
	}
	with := AccountFingerprint("operator", "hash", "JBSWY3DPEHPK3PXP")
	if with == plain {
		t.Fatal("turning codes on kept the fingerprint")
	}
	if with != AccountFingerprint("operator", "hash", "jbsw y3dp ehpk 3pxp") {
		t.Fatal("two spellings of one secret differ")
	}
	if with == AccountFingerprint("operator", "hash", "GEZDGNBVGY3TQOJQ") {
		t.Fatal("a rotated secret kept the fingerprint")
	}
}
