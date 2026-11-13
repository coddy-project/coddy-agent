package minisig

import (
	"strings"
	"testing"
)

func newKey(t *testing.T) (string, PublicKey) {
	t.Helper()
	secret, pub, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return secret, pub
}

func TestASignatureVerifiesAndCarriesItsComment(t *testing.T) {
	secret, pub := newKey(t)
	message := []byte("abc  coddy_1.0.0_linux_amd64.tar.gz\n")
	signature, err := Sign(secret, message, ReleaseComment("1.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParsePublicKey(pub.String())
	if err != nil {
		t.Fatal(err)
	}
	comment, err := Verify(parsed, message, signature)
	if err != nil {
		t.Fatal(err)
	}
	if comment != "release 1.0.0" {
		t.Fatalf("comment = %q", comment)
	}
}

func TestVerifyRefuses(t *testing.T) {
	secret, pub := newKey(t)
	_, other := newKey(t)
	message := []byte("the list\n")
	signature, err := Sign(secret, message, "release 1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		key       PublicKey
		message   []byte
		signature []byte
		want      string
	}{
		"a changed file":    {pub, []byte("the list!\n"), signature, "does not match the file"},
		"another key":       {other, message, signature, "another key"},
		"a changed comment": {pub, message, []byte(strings.Replace(string(signature), "release 1.0.0", "release 2.0.0", 1)), "trusted comment"},
		"no signature":      {pub, message, []byte("<html>not found</html>"), "not a minisign signature"},
		"an empty file":     {pub, message, nil, "not a minisign signature"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Verify(tc.key, tc.message, tc.signature)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one naming %q", err, tc.want)
			}
		})
	}
}

// A file the signer did not make with the comment it claims cannot be split on
// a line break to smuggle another one in.
func TestSignRefusesAMultilineComment(t *testing.T) {
	secret, _ := newKey(t)
	if _, err := Sign(secret, []byte("x"), "release 1.0.0\nrelease 2.0.0"); err == nil {
		t.Fatal("a comment of two lines was signed")
	}
}

// A signature the real minisign made, with a key thrown away after this one use:
// `minisign -G -W`, then `minisign -S -m list -t "release 1.0.0"`. The other
// direction was checked by hand with minisign 0.11: `minisign -Vm` takes a
// signature of Sign.
func TestVerifyReadsWhatMinisignWrites(t *testing.T) {
	pub, err := ParsePublicKey("RWSdkTJgm0P0sYBJZ6xqPafQ4OeUKMSOcox/CAU4n+FeJHkBUt2vXIXf")
	if err != nil {
		t.Fatal(err)
	}
	const signature = `untrusted comment: made by minisign for a test
RUSdkTJgm0P0sfdGe3q+TPsYfFPE2AGqe/N7b69wPB6csMl9LFFgrOgOrMbKuWXci2vEIQAMAHbgzxR+Xg1DyXdZnpgS1BPiyww=
trusted comment: release 1.0.0
CPWyIpiVDPHMayG67nKONJVlC9tb/t7w1W4x5oeMTafnY2h5RiZdIxJdeKzXrvGlT7i7GDSiLhWdUzRu1wbjBw==
`
	comment, err := Verify(pub, []byte("abc  coddy_1.0.0_linux_amd64.tar.gz\n"), []byte(signature))
	if err != nil {
		t.Fatal(err)
	}
	if comment != "release 1.0.0" {
		t.Fatalf("comment = %q", comment)
	}
}
