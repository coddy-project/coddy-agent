package modelcatalog

import (
	"os"
	"testing"
)

// fixture parses testdata/api.json, a catalog built around what the real one
// holds: providers with and without an api field, local and templated
// endpoints, two providers behind one endpoint, broken entries.
func fixture(t *testing.T) Index {
	t.Helper()
	f, err := os.Open("testdata/api.json")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	idx, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	return idx
}
