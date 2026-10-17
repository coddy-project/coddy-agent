//go:build cli

package xutil_test

import (
	"errors"
	"testing"

	"github.com/jezek/xgb/xproto"

	"github.com/EvilFreelancer/coddy-agent/external/cli/xutil"
	"github.com/EvilFreelancer/coddy-agent/external/cli/xutil/xvfbtest"
)

func TestReadPropertyStopsAtItsLimit(t *testing.T) {
	display := xvfbtest.Start(t)
	c, err := xutil.Open(display)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	prop, err := c.Atom("CODDY_TEST")
	if err != nil {
		t.Fatal(err)
	}
	root := c.Screen.Root
	c.SetString(root, prop, xproto.AtomString, "0123456789")
	if _, _, _, err := c.ReadProperty(root, prop, false, 4); !errors.Is(err, xutil.ErrPropertyTooLarge) {
		t.Fatalf("a 10-byte property under a 4-byte limit: %v", err)
	}
	v, typ, format, err := c.ReadProperty(root, prop, true, 10)
	if err != nil || string(v) != "0123456789" || typ != xproto.AtomString || format != 8 {
		t.Fatalf("read %q %v %v %v", v, typ, format, err)
	}
	if v, _, _, _ := c.ReadProperty(root, prop, false, 0); v != nil {
		t.Fatalf("the property survived a read that deletes it: %q", v)
	}
}

func TestScaleFromResources(t *testing.T) {
	for res, want := range map[string]float64{
		"":                               1,
		"Xft.dpi:\t96\nXft.hinting: 1":   1,
		"Xft.antialias: 1\nXft.dpi: 192": 2,
		"Xft.dpi: 144":                   1.5,
		"Xft.dpi: 120":                   1.25,
		"Xft.dpi: 72":                    1,
		"Xft.dpi: 900":                   4,
		"Xft.dpi: lots":                  1,
	} {
		if got := xutil.ScaleFromResources(res); got != want {
			t.Errorf("%q: %v, want %v", res, got, want)
		}
	}
}
