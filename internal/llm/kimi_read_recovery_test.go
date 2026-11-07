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
	"sync/atomic"
	"testing"
	"time"
)

func TestKimiReadRecoveryManaged(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	for _, operation := range []string{"models", "usage"} {
		t.Run(operation, func(t *testing.T) {
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
			oauth := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rotations.Add(1)
				_, _ = io.WriteString(w, `{"access_token":"new","refresh_token":"next","expires_in":3600}`)
			}))
			defer oauth.Close()
			ref, err := NewKimiCredentialRefresher(store, "global", oauth.URL, oauth.Client())
			if err != nil {
				t.Fatal(err)
			}
			src := &KimiSubscriptionSource{refresher: ref, region: "global"}
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("Authorization") == "Bearer old" {
					w.WriteHeader(401)
					return
				}
				if operation == "models" {
					_, _ = io.WriteString(w, `{"data":[]}`)
				} else {
					_, _ = io.WriteString(w, `{"usages":{}}`)
				}
			}))
			defer srv.Close()
			_, _, err = src.WithCredential(context.Background(), func(token string) error {
				if operation == "models" {
					_, e := fetchKimiModels(context.Background(), "subscription", token, srv.URL, srv.Client())
					return e
				}
				_, e := fetchKimiUsage(context.Background(), token, srv.URL, srv.Client())
				return e
			})
			if err != nil || calls.Load() != 2 || rotations.Load() != 1 {
				t.Fatalf("err=%v calls=%d rotations=%d", err, calls.Load(), rotations.Load())
			}
		})
	}
}

func TestKimiModelsStatusTyped(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Retry-After", "17"); w.WriteHeader(429) }))
	defer srv.Close()
	_, err := fetchKimiModels(context.Background(), "subscription", "fixture", srv.URL, srv.Client())
	if UpstreamStatus(err) != 429 {
		t.Fatalf("status: %v", err)
	}
	d, ok := UpstreamRetryAfter(err)
	if !ok || d.Seconds() != 17 {
		t.Fatalf("retry after %v %v", d, ok)
	}
	if errors.Is(err, ErrKimiCLILoginRequired) {
		t.Fatal("unexpected CLI error")
	}
}

func TestKimiReadRecoveryCLI401(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	path := filepath.Join(home, "credentials", "kimi-code.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte(fmt.Sprintf(`{"access_token":"cli","refresh_token":"untouched","expires_at":%d}`, time.Now().Add(time.Hour).Unix()))
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"models", "usage"} {
		t.Run(op, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(401) }))
			defer srv.Close()
			src := &KimiSubscriptionSource{region: "china"}
			_, _, err := src.WithCredential(context.Background(), func(token string) error {
				if op == "models" {
					_, e := fetchKimiModels(context.Background(), "subscription", token, srv.URL, srv.Client())
					return e
				}
				_, e := fetchKimiUsage(context.Background(), token, srv.URL, srv.Client())
				return e
			})
			if !errors.Is(err, ErrKimiCLILoginRequired) || calls.Load() != 1 {
				t.Fatalf("err=%v calls=%d", err, calls.Load())
			}
		})
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(data) {
		t.Fatal("CLI file changed")
	}
}

func TestKimiReadRecoveryStops(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed Kimi storage requires secure durable locking")
	}
	for _, op := range []string{"models", "usage"} {
		for _, status := range []int{401, 429} {
			t.Run(fmt.Sprintf("%s/%d", op, status), func(t *testing.T) {
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
				oauth := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					rotations.Add(1)
					_, _ = io.WriteString(w, `{"access_token":"new","refresh_token":"next","expires_in":3600}`)
				}))
				defer oauth.Close()
				ref, err := NewKimiCredentialRefresher(store, "global", oauth.URL, oauth.Client())
				if err != nil {
					t.Fatal(err)
				}
				srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					w.Header().Set("Retry-After", "17")
					w.WriteHeader(status)
				}))
				defer srv.Close()
				src := &KimiSubscriptionSource{refresher: ref, region: "global"}
				_, _, err = src.WithCredential(context.Background(), func(token string) error {
					if op == "models" {
						_, e := fetchKimiModels(context.Background(), "subscription", token, srv.URL, srv.Client())
						return e
					}
					_, e := fetchKimiUsage(context.Background(), token, srv.URL, srv.Client())
					return e
				})
				want := int32(1)
				if status == 401 {
					want = 2
				}
				if err == nil || calls.Load() != want || rotations.Load() != want-1 {
					t.Fatalf("err=%v calls=%d refresh=%d", err, calls.Load(), rotations.Load())
				}
			})
		}
	}
}

func TestKimiReadRecoveryConcurrent401(t *testing.T) {
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
	var rotations atomic.Int32
	oauth := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rotations.Add(1)
		_, _ = io.WriteString(w, `{"access_token":"new","refresh_token":"next","expires_in":3600}`)
	}))
	defer oauth.Close()
	ref, err := NewKimiCredentialRefresher(store, "global", oauth.URL, oauth.Client())
	if err != nil {
		t.Fatal(err)
	}
	src := &KimiSubscriptionSource{refresher: ref, region: "global"}
	const n = 6
	entered := make(chan struct{}, n)
	release := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer old" {
			entered <- struct{}{}
			<-release
			w.WriteHeader(401)
			return
		}
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			_, _, e := src.WithCredential(context.Background(), func(token string) error {
				_, e := fetchKimiModels(context.Background(), "subscription", token, srv.URL, srv.Client())
				return e
			})
			errs <- e
		}()
	}
	for i := 0; i < n; i++ {
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			close(release)
			t.Fatal("requests did not arrive")
		}
	}
	close(release)
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if rotations.Load() != 1 {
		t.Fatalf("refreshes %d", rotations.Load())
	}
}
