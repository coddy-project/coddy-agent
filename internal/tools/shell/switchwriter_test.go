package shell

import (
	"bytes"
	"sync"
	"testing"
)

func TestSwitchWriterBuffersUntilRedirected(t *testing.T) {
	w := &switchWriter{}
	if _, err := w.Write([]byte("before")); err != nil {
		t.Fatal(err)
	}
	if got := string(w.Bytes()); got != "before" {
		t.Fatalf("buffered %q, want %q", got, "before")
	}

	var target bytes.Buffer
	prefix := w.Redirect(&target)
	if string(prefix) != "before" {
		t.Fatalf("Redirect returned %q, want the buffered prefix", prefix)
	}
	if got := target.String(); got != "before" {
		t.Fatalf("target holds %q, want the flushed prefix", got)
	}

	if _, err := w.Write([]byte("after")); err != nil {
		t.Fatal(err)
	}
	if got := target.String(); got != "beforeafter" {
		t.Fatalf("target holds %q, want the prefix followed by later writes", got)
	}
}

func TestSwitchWriterRedirectsOnce(t *testing.T) {
	w := &switchWriter{}
	if _, err := w.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}

	var first, second bytes.Buffer
	w.Redirect(&first)
	if prefix := w.Redirect(&second); prefix != nil {
		t.Fatalf("second Redirect returned %q, want nil: the first caller owns the stream", prefix)
	}
	if _, err := w.Write([]byte("y")); err != nil {
		t.Fatal(err)
	}
	if second.Len() != 0 {
		t.Fatalf("the second target received %q", second.String())
	}
	if got := first.String(); got != "xy" {
		t.Fatalf("the first target holds %q, want %q", got, "xy")
	}
}

func TestSwitchWriterIsSafeUnderConcurrentUse(t *testing.T) {
	w := &switchWriter{}
	var target bytes.Buffer
	var wg sync.WaitGroup

	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_, _ = w.Write([]byte("chunk"))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = w.Bytes()
		}
	}()
	go func() {
		defer wg.Done()
		w.Redirect(&target)
	}()
	wg.Wait()
}
