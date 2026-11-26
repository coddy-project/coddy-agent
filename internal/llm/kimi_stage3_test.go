package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

const stage3DeviceID = "00000000-0000-4000-8000-000000000776"

func TestKimiInferenceCredentialDevice(t *testing.T) {
	for _, tc := range []struct {
		name, id     string
		cli, invalid bool
	}{
		{"managed", stage3DeviceID, false, false}, {"managed absent", "", false, false}, {"managed invalid", "unsafe\r\n", false, true},
		{"cli", stage3DeviceID + "\n", true, false}, {"cli absent", "", true, false}, {"cli invalid", "unsafe\r\n", true, false}, {"cli oversized", stage3DeviceID + strings.Repeat(" ", 1024), true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			kind := KimiSourceManaged
			var store *KimiCredentialStore
			if tc.cli {
				kind = KimiSourceCLI
				home := t.TempDir()
				t.Setenv("KIMI_CODE_HOME", home)
				if err := os.Mkdir(filepath.Join(home, "credentials"), 0700); err != nil {
					t.Fatal(err)
				}
				tokenPath := filepath.Join(home, "credentials", "kimi-code.json")
				fixture := []byte(`{"access_token":"fixture-token","refresh_token":"cli-owned"}`)
				if err := os.WriteFile(tokenPath, fixture, 0600); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(home, "device_id")
				if tc.id != "" {
					if err := os.WriteFile(path, []byte(tc.id), 0600); err != nil {
						t.Fatal(err)
					}
				}
				t.Cleanup(func() {
					data, err := os.ReadFile(path)
					if tc.id == "" {
						if !errors.Is(err, os.ErrNotExist) {
							t.Error("created CLI device file")
						}
					} else if err != nil || string(data) != tc.id {
						t.Error("changed CLI device file")
					}
					data, err = os.ReadFile(tokenPath)
					if err != nil || string(data) != string(fixture) {
						t.Error("changed CLI token file")
					}
				})
			} else {
				if !KimiRefreshSupported() {
					t.Skip("managed store unavailable")
				}
				store = NewKimiCredentialStore(privateKimiTestDir(t))
				cred := KimiCredential{AccessToken: "fixture-token", RefreshToken: "fixture-refresh", DeviceID: tc.id}
				if err := store.saveForTest("china", cred); err != nil {
					t.Fatal(err)
				}
			}
			fallback, err := kimiDeviceID()
			if err != nil {
				t.Fatal(err)
			}
			want := fallback
			if strings.TrimSpace(tc.id) == stage3DeviceID && tc.name != "cli oversized" {
				want = stage3DeviceID
			}
			hits := 0
			client := &http.Client{Transport: kimiRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				hits++
				if r.URL.Host != "api.kimi.com" || r.Header.Get("Authorization") != "Bearer fixture-token" || r.UserAgent() != "coddy/"+version.Get() || r.Header.Get("X-Msh-Platform") != "kimi_code_cli" || r.Header.Get("X-Msh-Version") != version.Get() || r.Header.Get("X-Msh-Device-Id") != want {
					t.Error("incorrect inference route or headers")
				}
				for _, key := range []string{"X-Msh-Device-Name", "X-Msh-Device-Model", "X-Msh-Os-Version"} {
					if r.Header.Get(key) != "" {
						t.Error("unexpected device metadata", key)
					}
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: http.Header{"X-Kimi-Credential-Source": []string{"forged"}}}, nil
			})}
			// Reconstruct store and transport to check that identity isn't held in either instance.
			for i := 0; i < 2; i++ {
				if store != nil {
					store = NewKimiCredentialStore(store.Dir())
				}
				source, err := NewKimiSubscriptionSource(store, "subscription", "china", "none", false)
				if err != nil {
					t.Fatal(err)
				}
				tr, err := newKimiInferenceTransport("subscription", "china", "kimi", func(context.Context) (string, error) { t.Error("did a second auth lookup"); return "", nil }, client)
				if err != nil {
					t.Fatal(err)
				}
				tr.recovery = source
				resp, err := tr.Do(ctx, strings.NewReader("{}"))
				if tc.invalid {
					_, refreshErr := source.refresher.ForceAccessToken(ctx)
					if err == nil || refreshErr == nil || err.Error() != refreshErr.Error() || hits != 0 {
						t.Fatalf("inference err=%v, refresh err=%v, hits=%d", err, refreshErr, hits)
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := resp.Body.Close(); err != nil {
					t.Fatal(err)
				}
				if got := kimiResponseCredentialSource(resp); got != string(kind) {
					t.Errorf("source=%q, want %q", got, kind)
				}
			}
		})
	}
}

func TestKimiResponseCredentialSourceIgnoresWireHeaders(t *testing.T) {
	if got := kimiResponseCredentialSource(&http.Response{Header: http.Header{"X-Kimi-Credential-Source": []string{"managed"}}}); got != "" {
		t.Fatalf("accepted upstream source: %q", got)
	}
	if kimiResponseCredentialSource(nil) != "" {
		t.Fatal("a nil response has no credential source")
	}
}

func TestKimiRefreshExplicitTemporaryRejectionKeepsLogin(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed store unavailable")
	}
	for _, status := range []int{http.StatusUnauthorized, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			store := NewKimiCredentialStore(privateKimiTestDir(t))
			original := expiringKimiCredential()
			if err := store.saveForTest("global", original); err != nil {
				t.Fatal(err)
			}
			server, srv := newKimiRefreshServer(t, func(n int32, w http.ResponseWriter) {
				if n == 1 {
					w.WriteHeader(status)
					_, _ = io.WriteString(w, `{"error":"temporarily_unavailable"}`)
					return
				}
				okReply(n, w)
			})
			ref := newTestRefresher(t, store, srv.URL, srv.Client())
			_, err := ref.AccessToken(context.Background())
			if err == nil || errors.Is(err, ErrKimiCredentialUnauthorized) || errors.Is(err, ErrKimiCredentialUncertain) {
				t.Errorf("expected temporary rejection, got %v", err)
			}
			cred, state, err := store.Load("global")
			if err != nil || state != KimiCredentialValid || cred.RefreshToken != original.RefreshToken || server.calls.Load() != 1 {
				t.Errorf("login not retained: state=%v err=%v calls=%d", state, err, server.calls.Load())
			}
			if _, err := ref.AccessToken(context.Background()); err != nil || server.calls.Load() != 2 {
				t.Fatalf("could not retry refresh: %v, calls=%d", err, server.calls.Load())
			}
		})
	}
}

func TestKimiRefreshRejectionDoesNotTombstoneReplacement(t *testing.T) {
	if !KimiRefreshSupported() {
		t.Skip("managed store unavailable")
	}
	store := NewKimiCredentialStore(privateKimiTestDir(t))
	old := expiringKimiCredential()
	if err := store.saveForTest("global", old); err != nil {
		t.Fatal(err)
	}
	_, srv := newKimiRefreshServer(t, func(_ int32, w http.ResponseWriter) {
		replacement := old
		replacement.AccessToken = "fixture-new-access"
		replacement.ExpiresAt = time.Now().Add(time.Hour).Unix()
		replacement.DeviceID = stage3DeviceID
		// Simulate the same out-of-band peer write as the existing peer-rotation test,
		// but with a provider that returns the same refresh token on replacement.
		if err := store.saveUnlocked("global", replacement); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
	})
	ref := newTestRefresher(t, store, srv.URL, srv.Client())
	token, err := ref.AccessToken(context.Background())
	if err != nil || token != "fixture-new-access" {
		t.Errorf("peer replacement lost: %v", err)
	}
	_, state, err := store.Load("global")
	if err != nil || state != KimiCredentialValid {
		t.Errorf("peer credential state=%v err=%v", state, err)
	}
}
