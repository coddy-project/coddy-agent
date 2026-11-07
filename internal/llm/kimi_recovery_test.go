package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestKimiRecoveryManaged401(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store := NewKimiCredentialStore(dir)
	id, err := store.BeginLogin(context.Background(), "global")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.CompleteLogin(context.Background(), "global", id, KimiCredential{AccessToken: "old", RefreshToken: "refresh", ExpiresAt: time.Now().Add(10 * time.Minute).Unix()}); err != nil {
		t.Fatal(err)
	}
	var rotations atomic.Int32
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rotations.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, `{"access_token":"new","refresh_token":"next","expires_in":3600}`); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer oauth.Close()
	refresher, err := NewKimiCredentialRefresher(store, "global", oauth.URL, oauth.Client())
	if err != nil {
		t.Fatal(err)
	}
	src := &KimiSubscriptionSource{refresher: refresher, region: "global"}
	var calls atomic.Int32
	inference := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		b, _ := io.ReadAll(r.Body)
		if string(b) != "body" {
			t.Error("body changed")
		}
		if r.Header.Get("Authorization") == "Bearer old" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer inference.Close()
	tr := &KimiInferenceTransport{endpoint: inference.URL, client: inference.Client(), source: func(ctx context.Context) (string, error) { v, _, e := src.AccessToken(ctx); return v, e }, recovery: src}
	resp, err := tr.Do(context.Background(), strings.NewReader("body"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || calls.Load() != 2 || rotations.Load() != 1 {
		t.Fatalf("status=%d calls=%d rotations=%d", resp.StatusCode, calls.Load(), rotations.Load())
	}
}

func TestKimiRecoveryConcurrentRejectedGeneration(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store := NewKimiCredentialStore(dir)
	id, err := store.BeginLogin(context.Background(), "global")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.CompleteLogin(context.Background(), "global", id, KimiCredential{AccessToken: "old", RefreshToken: "refresh", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	var rotations atomic.Int32
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rotations.Add(1)
		if _, err := io.WriteString(w, `{"access_token":"new","refresh_token":"next","expires_in":3600}`); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer oauth.Close()
	ref, err := NewKimiCredentialRefresher(store, "global", oauth.URL, oauth.Client())
	if err != nil {
		t.Fatal(err)
	}
	src := &KimiSubscriptionSource{refresher: ref, region: "global"}
	ticket, err := src.ticket(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	const n = 12
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			token, err := src.rejectedTicket(context.Background(), ticket)
			if err == nil && token != "new" {
				err = errors.New("wrong token")
			}
			errs <- err
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if rotations.Load() != 1 {
		t.Fatalf("rotations %d", rotations.Load())
	}
}

func TestKimiRecoveryCLIAndNon401(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	path := filepath.Join(home, "credentials", "kimi-code.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte(fmt.Sprintf(`{"access_token":"cli","refresh_token":"unchanged","expires_at":%d}`, time.Now().Add(time.Hour).Unix()))
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(status) }))
			defer srv.Close()
			src := &KimiSubscriptionSource{region: "china"}
			tr := &KimiInferenceTransport{endpoint: srv.URL, client: srv.Client(), recovery: src}
			resp, err := tr.Do(context.Background(), strings.NewReader("{}"))
			if resp != nil {
				_ = resp.Body.Close()
			}
			if status == http.StatusUnauthorized && !errors.Is(err, ErrKimiCLILoginRequired) {
				t.Fatalf("CLI 401: %v", err)
			}
			if status == http.StatusTooManyRequests && (err != nil || resp.StatusCode != http.StatusTooManyRequests) {
				t.Fatalf("429: %v", err)
			}
			if calls.Load() != 1 {
				t.Fatalf("calls %d", calls.Load())
			}
		})
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(data) {
		t.Fatal("CLI file changed")
	}
}

func TestKimiRecoverySecond401Stops(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store := NewKimiCredentialStore(dir)
	id, err := store.BeginLogin(context.Background(), "global")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.CompleteLogin(context.Background(), "global", id, KimiCredential{AccessToken: "old", RefreshToken: "refresh"}); err != nil {
		t.Fatal(err)
	}
	var rotations, calls atomic.Int32
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rotations.Add(1)
		if _, err := io.WriteString(w, `{"access_token":"new","refresh_token":"next","expires_in":3600}`); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer oauth.Close()
	ref, err := NewKimiCredentialRefresher(store, "global", oauth.URL, oauth.Client())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(http.StatusUnauthorized) }))
	defer srv.Close()
	tr := &KimiInferenceTransport{endpoint: srv.URL, client: srv.Client(), recovery: &KimiSubscriptionSource{refresher: ref, region: "global"}}
	resp, err := tr.Do(context.Background(), strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized || calls.Load() != 2 || rotations.Load() != 1 {
		t.Fatalf("status %d calls %d rotations %d", resp.StatusCode, calls.Load(), rotations.Load())
	}
}

func TestKimiRecoveryDoesNotRetryPartialStream(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	path := filepath.Join(home, "credentials", "kimi-code.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"access_token":"cli","expires_at":%d}`, time.Now().Add(time.Hour).Unix())), 0600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\ndata: {bad}\n\n"); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer srv.Close()
	tr := &KimiInferenceTransport{endpoint: srv.URL, client: srv.Client(), recovery: &KimiSubscriptionSource{region: "china"}}
	p := newKimiChatProvider(tr, ProviderInput{Model: "kimi"})
	chunks := 0
	_, err := p.Stream(context.Background(), nil, nil, func(StreamChunk) { chunks++ })
	if err == nil || chunks == 0 || calls.Load() != 1 {
		t.Fatalf("err=%v chunks=%d calls=%d", err, chunks, calls.Load())
	}
}
