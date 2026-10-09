package jsoncanon

import "testing"

func TestCanonicalSortsKeysAndDropsSpace(t *testing.T) {
	for in, want := range map[string]string{
		`{"b": 1, "a": {"d": [1, 2, {"z": true, "y": null}], "c": "x"}}`: `{"a":{"c":"x","d":[1,2,{"y":null,"z":true}]},"b":1}`,
		`  [ "a" , 2.5 , false ]  `: `["a",2.5,false]`,
		`"just a string"`:           `"just a string"`,
		`42`:                        `42`,
		`{}`:                        `{}`,
		`[]`:                        `[]`,
		`{"k": "a<b & c"}`:          `{"k":"a<b & c"}`,
	} {
		got, err := Canonical([]byte(in))
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if string(got) != want {
			t.Errorf("%s:\n got  %s\n want %s", in, got, want)
		}
	}
}

// The same value written two ways is one canonical form.
func TestCanonicalIsTheSameForEquivalentDocuments(t *testing.T) {
	a, _ := Canonical([]byte(`{"path": "main.go", "limit": 10}`))
	b, _ := Canonical([]byte("{\n  \"limit\": 10,\n  \"path\": \"main.go\"\n}"))
	if string(a) != string(b) {
		t.Fatalf("%s != %s", a, b)
	}
}

func TestCanonicalRefusesWhatIsNotJSON(t *testing.T) {
	for _, in := range []string{``, `   `, `{"a":}`, `[1,`, `not json`, `{"a":1} trailing`} {
		if _, err := Canonical([]byte(in)); err == nil {
			t.Errorf("%q: want an error", in)
		}
	}
}
