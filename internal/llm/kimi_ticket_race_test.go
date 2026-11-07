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

func TestKimiTicketInferencePinsCatalogCredential(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "stream"}[stream], func(t *testing.T) {
			var got string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Get("Authorization")
				w.Header().Set("Content-Type", "application/json")
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: [DONE]\n\n")
				} else {
					_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
				}
			}))
			defer srv.Close()
			ticket := kimiCredentialTicket{token: "catalog-A", kind: KimiSourceCLI}
			tr := &KimiInferenceTransport{endpoint: srv.URL, client: srv.Client(), source: func(context.Context) (string, error) { return "different-B", nil }, catalogTicket: &ticket}
			p := newKimiChatProvider(tr, ProviderInput{Model: "m"})
			if stream {
				_, _ = p.Stream(context.Background(), nil, nil, nil)
			} else {
				_, _ = p.Complete(context.Background(), nil, nil)
			}
			if got != "Bearer catalog-A" {
				t.Fatalf("sent %q instead of catalog credential", got)
			}
		})
	}
}

func TestKimiTicketRetryRevalidatesProtocol(t *testing.T) {
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
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"access_token":"new","refresh_token":"next","expires_in":3600}`)
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
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(401) }))
	defer srv.Close()
	tr := &KimiInferenceTransport{endpoint: srv.URL, client: srv.Client(), recovery: src, catalogTicket: &ticket, validateCredential: func(context.Context, string) error { return errors.New("catalog protocol changed") }}
	_, err = tr.Do(context.Background(), strings.NewReader("{}"))
	if err == nil || calls.Load() != 1 {
		t.Fatalf("err=%v requests=%d", err, calls.Load())
	}
}

func TestKimiTicketCatalogReplacement(t *testing.T) {
	for _, change := range []string{"cli-replace", "managed-relogin", "cli-to-managed"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", change, stream), func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("KIMI_CODE_HOME", home)
				path := filepath.Join(home, "credentials", "kimi-code.json")
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				writeCLI := func(token string) {
					t.Helper()
					if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"access_token":%q,"expires_at":%d}`, token, time.Now().Add(time.Hour).Unix())), 0600); err != nil {
						t.Fatal(err)
					}
				}
				writeCLI("cli-A")
				dir := t.TempDir()
				if err := os.Chmod(dir, 0700); err != nil {
					t.Fatal(err)
				}
				store := NewKimiCredentialStore(dir)
				login := func(token string) {
					t.Helper()
					id, err := store.BeginLogin(context.Background(), "china")
					if err != nil {
						t.Fatal(err)
					}
					if err = store.CompleteLogin(context.Background(), "china", id, KimiCredential{AccessToken: token, RefreshToken: "refresh"}); err != nil {
						t.Fatal(err)
					}
				}
				if change == "managed-relogin" {
					login("managed-A")
				}
				src, err := NewKimiSubscriptionSource(store, "subscription", "china", "none", false)
				if err != nil {
					t.Fatal(err)
				}
				ticket, err := src.catalogTicket(context.Background(), func(token string) error { return nil })
				if err != nil {
					t.Fatal(err)
				}
				switch change {
				case "cli-replace":
					writeCLI("cli-B")
				case "managed-relogin":
					if err := store.Remove("china"); err != nil {
						t.Fatal(err)
					}
					login("managed-B")
				case "cli-to-managed":
					login("managed-B")
				}
				var got string
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					got = r.Header.Get("Authorization")
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, "data: [DONE]\n\n")
					} else {
						_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
					}
				}))
				defer srv.Close()
				tr := &KimiInferenceTransport{endpoint: srv.URL, client: srv.Client(), recovery: src, catalogTicket: &ticket}
				p := newKimiChatProvider(tr, ProviderInput{Model: "m"})
				if stream {
					_, _ = p.Stream(context.Background(), nil, nil, nil)
				} else {
					_, _ = p.Complete(context.Background(), nil, nil)
				}
				want := "Bearer cli-A"
				if change == "managed-relogin" {
					want = "Bearer managed-A"
				}
				if got != want {
					t.Fatalf("wrong account sent")
				}
			})
		}
	}
}

func TestKimiTicket401PeerReplacement(t *testing.T) {
	for _, valid := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("valid=%t/stream=%t", valid, stream), func(t *testing.T) {
				dir := t.TempDir()
				if err := os.Chmod(dir, 0700); err != nil {
					t.Fatal(err)
				}
				store := NewKimiCredentialStore(dir)
				login := func(token string) {
					t.Helper()
					id, err := store.BeginLogin(context.Background(), "global")
					if err != nil {
						t.Fatal(err)
					}
					if err = store.CompleteLogin(context.Background(), "global", id, KimiCredential{AccessToken: token, RefreshToken: "refresh"}); err != nil {
						t.Fatal(err)
					}
				}
				login("old")
				var rotations atomic.Int32
				oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { rotations.Add(1); w.WriteHeader(500) }))
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
				var calls, checks atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					n := calls.Add(1)
					if n == 1 {
						login("peer")
						w.WriteHeader(401)
						return
					}
					if r.Header.Get("Authorization") != "Bearer peer" {
						t.Error("wrong replacement")
					}
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, "data: [DONE]\n\n")
					} else {
						_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
					}
				}))
				defer srv.Close()
				tr := &KimiInferenceTransport{endpoint: srv.URL, client: srv.Client(), recovery: src, catalogTicket: &ticket, validateCredential: func(_ context.Context, token string) error {
					checks.Add(1)
					if token != "peer" {
						return errors.New("wrong token")
					}
					if !valid {
						return errors.New("protocol changed")
					}
					return nil
				}}
				p := newKimiChatProvider(tr, ProviderInput{Model: "m"})
				if stream {
					_, err = p.Stream(context.Background(), nil, nil, nil)
				} else {
					_, err = p.Complete(context.Background(), nil, nil)
				}
				want := int32(1)
				if valid {
					want = 2
				}
				if (err != nil) == valid || calls.Load() != want || checks.Load() != 1 || rotations.Load() != 0 {
					t.Fatalf("err=%v calls=%d checks=%d rotations=%d", err, calls.Load(), checks.Load(), rotations.Load())
				}
			})
		}
	}
}

func TestKimiTicketRefreshSameProtocol(t *testing.T) {
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
	var rotates, calls, checks atomic.Int32
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rotates.Add(1)
		_, _ = io.WriteString(w, `{"access_token":"new","refresh_token":"next","expires_in":3600}`)
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") == "Bearer old" {
			w.WriteHeader(401)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	tr := &KimiInferenceTransport{endpoint: srv.URL, client: srv.Client(), recovery: src, catalogTicket: &ticket, validateCredential: func(_ context.Context, token string) error {
		checks.Add(1)
		if token != "new" {
			return errors.New("wrong credential")
		}
		return nil
	}}
	resp, err := tr.Do(context.Background(), strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 || calls.Load() != 2 || rotates.Load() != 1 || checks.Load() != 1 {
		t.Fatalf("status=%d calls=%d rotations=%d checks=%d", resp.StatusCode, calls.Load(), rotates.Load(), checks.Load())
	}
}
