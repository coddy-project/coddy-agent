package localipc

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"runtime"
	"testing"
	"time"
)

func TestOneOwnerServesAnAuthenticatedLocalEndpoint(t *testing.T) {
	home := t.TempDir()
	owner, err := Claim(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := Claim(home); !errors.Is(err, ErrAlreadyOwned) {
		t.Fatalf("a second runtime claimed the same home: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- owner.Serve(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get(Header); got != "" {
				t.Errorf("the IPC credential reached the application: %q", got)
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(w, "shared runtime")
		}))
	}()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Serve did not stop")
		}
	}()

	client, err := Connect(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://local/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := client.HTTPClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK || string(body) != "shared runtime" {
		t.Fatalf("answer = %s %q", res.Status, body)
	}

	// Closing the local client is not a runtime lifecycle operation.
	res, err = client.HTTPClient().Get("http://local/")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	select {
	case err := <-done:
		t.Fatalf("a client closed the owner: %v", err)
	default:
	}
	if runtime.GOOS != "windows" {
		info, err := os.Lstat(recordPath(home))
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatalf("record mode = %v", info.Mode())
		}
	}
}

func TestAClientFollowsAnOwnerRestart(t *testing.T) {
	home := t.TempDir()
	first, err := Claim(home)
	if err != nil {
		t.Fatal(err)
	}
	serveOwner := func(owner *Owner, text string) (context.CancelFunc, chan error) {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- owner.Serve(ctx, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, text) }))
		}()
		return cancel, done
	}
	cancelFirst, doneFirst := serveOwner(first, "first")
	client, err := Connect(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	cancelFirst()
	if err := <-doneFirst; err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Claim(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := second.Close(); err != nil {
			t.Error(err)
		}
	}()
	cancelSecond, doneSecond := serveOwner(second, "second")
	defer func() {
		cancelSecond()
		if err := <-doneSecond; err != nil {
			t.Error(err)
		}
	}()
	res, err := client.HTTPClient().Get("http://local/after-restart")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if string(body) != "second" {
		t.Fatalf("the client kept the old owner credential: %q", body)
	}
}

func TestConnectRefusesAForgedOrUnauthenticatedEndpoint(t *testing.T) {
	home := t.TempDir()
	if _, err := Connect(context.Background(), home); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("an absent owner answered %v", err)
	}
	owner, err := Claim(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	}()

	rec, err := readRecord(home)
	if err != nil {
		t.Fatal(err)
	}
	rec.Token = "definitely-not-the-owner-token"
	if err := writeRecord(home, rec); err != nil {
		t.Fatal(err)
	}
	if _, err := Connect(context.Background(), home); err == nil {
		t.Fatal("a record pointing at an endpoint that does not know its token was trusted")
	}
}

func TestConnectRefusesAnUnsafeRecord(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits are not the Windows access check")
	}
	home := t.TempDir()
	owner, err := Claim(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := os.Chmod(recordPath(home), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Connect(context.Background(), home); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("a world-readable credential record was accepted: %v", err)
	}
}
