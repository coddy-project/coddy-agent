package llm

import (
	"strings"
	"testing"
)

func TestHTTPClientForOptionalProxy(t *testing.T) {
	t.Parallel()
	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		c, err := HTTPClientForOptionalProxy("")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if c != nil {
			t.Fatalf("expected nil client")
		}
	})
	t.Run("whitespace", func(t *testing.T) {
		t.Parallel()
		c, err := HTTPClientForOptionalProxy("  \t  ")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if c != nil {
			t.Fatalf("expected nil client")
		}
	})
	t.Run("bad_url", func(t *testing.T) {
		t.Parallel()
		_, err := HTTPClientForOptionalProxy("http://%zz")
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("bad_scheme", func(t *testing.T) {
		t.Parallel()
		_, err := HTTPClientForOptionalProxy("ftp://127.0.0.1:21")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "unsupported proxy scheme") {
			t.Fatalf("unexpected err: %v", err)
		}
	})
	t.Run("http_ok", func(t *testing.T) {
		t.Parallel()
		c, err := HTTPClientForOptionalProxy("http://127.0.0.1:3128")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if c == nil || c.Transport == nil {
			t.Fatal("expected non-nil client and transport")
		}
	})
	t.Run("socks5_ok", func(t *testing.T) {
		t.Parallel()
		c, err := HTTPClientForOptionalProxy("socks5://127.0.0.1:1080")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if c == nil || c.Transport == nil {
			t.Fatal("expected non-nil client and transport")
		}
	})
}
