//go:build http

package httpserver

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOpenAIImageCountBoundaryAcceptance(t *testing.T) {
	const image = `{"type":"image_url","image_url":{"url":"https://example.com/image.png"}}`
	for _, tc := range []struct {
		name      string
		count     int
		wantError bool
	}{
		{"at limit", 16, false},
		{"above limit", 17, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parts := make([]string, tc.count)
			for i := range parts {
				parts[i] = image
			}
			_, images, err := openAIContent(json.RawMessage("[" + strings.Join(parts, ",") + "]"))
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "at most 16 images") {
					t.Fatalf("error = %v, want image count limit", err)
				}
			} else if err != nil || len(images) != tc.count {
				t.Fatalf("images = %d, error = %v", len(images), err)
			}
		})
	}
}

func TestOpenAIImageURLSizeBoundaryAcceptance(t *testing.T) {
	const limit = 20 << 20
	const prefix = `[{"type":"image_url","image_url":{"url":"`
	const suffix = `"}}]`
	for _, tc := range []struct {
		name      string
		size      int
		wantError bool
	}{
		{"at limit", limit, false},
		{"above limit", limit + 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var raw strings.Builder
			raw.Grow(len(prefix) + tc.size + len(suffix))
			raw.WriteString(prefix)
			for remaining := tc.size; remaining > 0; {
				const block = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
				n := min(remaining, len(block))
				raw.WriteString(block[:n])
				remaining -= n
			}
			raw.WriteString(suffix)
			_, images, err := openAIContent(json.RawMessage(raw.String()))
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "exceeds") {
					t.Fatalf("error = %v, want image size limit", err)
				}
			} else if err != nil || len(images) != 1 || len(images[0].DataURL) != tc.size {
				t.Fatalf("image count = %d, error = %v", len(images), err)
			}
		})
	}
}
