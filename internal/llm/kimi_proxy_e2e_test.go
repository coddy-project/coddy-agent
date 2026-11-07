package llm

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestKimiProxyE2E(t *testing.T) {
	if os.Getenv("CODDY_KIMI_PROXY_CHILD") == "1" {
		kimiProxyE2EChild(t)
		return
	}
	for _, region := range []string{"global", "china"} {
		t.Run(region, func(t *testing.T) { kimiProxyE2EParent(t, region) })
	}
}

func kimiProxyE2EParent(t *testing.T, region string) {
	t.Helper()
	auth, api, platform, site := "auth.kimi.ai", "api.kimi.ai", "api.moonshot.ai", "www.kimi.ai"
	if region == "china" {
		auth, api, platform, site = "auth.kimi.com", "api.kimi.com", "api.moonshot.cn", "www.kimi.com"
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Kimi test CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{auth, api, platform}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, &leafKey.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert := tls.Certificate{Certificate: [][]byte{leafDER, caDER}, PrivateKey: leafKey}
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	connections := map[string]int{}
	requests := map[string]int{}
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "close")
		w.Header().Set("Content-Type", "application/json")
		key := r.Method + " " + r.Host + r.URL.Path
		mu.Lock()
		requests[key]++
		mu.Unlock()
		if r.TLS == nil || r.TLS.ServerName != r.Host {
			t.Errorf("TLS server name mismatch for %s", key)
		}
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "coddy/") ||
			r.Header.Get("X-Msh-Platform") != "kimi_code_cli" ||
			r.Header.Get("X-Msh-Version") == "" ||
			!kimiDeviceIDPattern.MatchString(r.Header.Get("X-Msh-Device-Id")) {
			t.Errorf("missing Kimi client identity headers for %s", key)
		}
		switch key {
		case "POST " + auth + "/api/oauth/device_authorization":
			if r.Header.Get("Authorization") != "" {
				t.Error("unexpected device authorization credential")
			}
			if _, err := fmt.Fprintf(w, `{"device_code":"device-test","user_code":"user-test","verification_uri_complete":"https://%s/code/authorize_device","expires_in":600,"interval":1}`, site); err != nil {
				t.Errorf("write response: %v", err)
			}
		case "POST " + auth + "/api/oauth/token":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.Header.Get("Authorization") != "" || r.Form.Get("client_id") != kimiClientID {
				t.Error("unexpected OAuth credential")
			}
			switch r.Form.Get("grant_type") {
			case "urn:ietf:params:oauth:grant-type:device_code":
				if r.Form.Get("device_code") != "device-test" {
					t.Error("wrong device code")
				}
				if _, err := fmt.Fprint(w, `{"access_token":"access-test","refresh_token":"refresh-test","expires_in":3600}`); err != nil {
					t.Errorf("write response: %v", err)
				}
			case "refresh_token":
				if r.Form.Get("refresh_token") != "refresh-test" {
					t.Error("wrong refresh credential")
				}
				if _, err := fmt.Fprint(w, `{"access_token":"rotated-test","refresh_token":"rotated-refresh-test","expires_in":3600}`); err != nil {
					t.Errorf("write response: %v", err)
				}
			default:
				t.Errorf("unexpected OAuth grant for %s", key)
				w.WriteHeader(http.StatusBadRequest)
			}
		case "GET " + api + "/coding/v1/models", "GET " + platform + "/v1/models":
			if r.Header.Get("Authorization") != "Bearer rotated-test" {
				t.Errorf("wrong credential for %s", key)
			}
			if _, err := fmt.Fprint(w, `{"data":[{"id":"model-test","limit":{"context":8192},"context_length":8192}]}`); err != nil {
				t.Errorf("write response: %v", err)
			}
		case "GET " + api + "/coding/v1/usages":
			if r.Header.Get("Authorization") != "Bearer rotated-test" {
				t.Errorf("wrong credential for %s", key)
			}
			if _, err := fmt.Fprint(w, `{"usages":{"limit_5h":{"used_ratio":0.25}}}`); err != nil {
				t.Errorf("write response: %v", err)
			}
		case "POST " + api + "/coding/v1/chat/completions", "POST " + platform + "/v1/chat/completions":
			if r.Header.Get("Authorization") != "Bearer rotated-test" {
				t.Errorf("wrong credential for %s", key)
			}
			if _, err := fmt.Fprint(w, `{"choices":[{"message":{"content":"stand-answer"},"finish_reason":"stop"}]}`); err != nil {
				t.Errorf("write response: %v", err)
			}
		default:
			t.Errorf("unexpected upstream request %s", key)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	upstream.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	upstream.StartTLS()
	defer upstream.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || (r.Host != auth+":443" && r.Host != api+":443" && r.Host != platform+":443") {
			t.Errorf("unexpected proxy request %s %s", r.Method, r.Host)
			http.Error(w, "refused", http.StatusForbidden)
			return
		}
		mu.Lock()
		connections[r.Host]++
		mu.Unlock()
		up, err := net.DialTimeout("tcp", upstream.Listener.Addr().String(), time.Second)
		if err != nil {
			t.Error(err)
			http.Error(w, "unavailable", http.StatusBadGateway)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			_ = up.Close()
			t.Error("proxy cannot hijack")
			return
		}
		down, buf, err := hijacker.Hijack()
		if err != nil {
			_ = up.Close()
			t.Error(err)
			return
		}
		if _, err := buf.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			_ = down.Close()
			_ = up.Close()
			t.Error(err)
			return
		}
		if err := buf.Flush(); err != nil {
			_ = down.Close()
			_ = up.Close()
			t.Error(err)
			return
		}
		go func() {
			defer func() { _ = down.Close() }()
			defer func() { _ = up.Close() }()
			go func() { _, _ = io.Copy(up, buf) }()
			_, _ = io.Copy(down, up)
		}()
	}))
	defer proxy.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestKimiProxyE2E$", "-test.v")
	cmd.Env = append(os.Environ(), "CODDY_KIMI_PROXY_CHILD=1", "CODDY_KIMI_PROXY_URL="+proxy.URL, "CODDY_KIMI_PROXY_REGION="+region, "SSL_CERT_FILE="+caPath, "SSL_CERT_DIR="+t.TempDir(), "HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "NO_PROXY=", "http_proxy=", "https_proxy=", "all_proxy=", "no_proxy=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	mu.Lock()
	defer mu.Unlock()
	want := map[string]int{"POST " + auth + "/api/oauth/device_authorization": 1, "POST " + auth + "/api/oauth/token": 2, "GET " + api + "/coding/v1/models": 1, "GET " + api + "/coding/v1/usages": 1, "POST " + api + "/coding/v1/chat/completions": 1, "GET " + platform + "/v1/models": 1, "POST " + platform + "/v1/chat/completions": 1}
	for k, n := range want {
		if requests[k] != n {
			t.Errorf("upstream %s = %d, want %d", k, requests[k], n)
		}
	}
	if len(requests) != len(want) {
		t.Errorf("unexpected upstream routes: %v", requests)
	}
	for host, n := range map[string]int{auth + ":443": 3, api + ":443": 3, platform + ":443": 2} {
		if connections[host] != n {
			t.Errorf("CONNECT %s = %d, want %d", host, connections[host], n)
		}
	}
}

func kimiProxyE2EChild(t *testing.T) {
	t.Helper()
	region, proxy := os.Getenv("CODDY_KIMI_PROXY_REGION"), os.Getenv("CODDY_KIMI_PROXY_URL")
	proxyURL, err := url.Parse(proxy)
	if err != nil || proxyURL.Host == "" {
		t.Fatal("invalid test proxy URL")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	dialer := &net.Dialer{}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != proxyURL.Host {
			return nil, errors.New("test: direct network disabled")
		}
		return dialer.DialContext(ctx, network, address)
	}
	http.DefaultTransport = transport
	if _, err := http.Get("https://auth.kimi.ai/"); err == nil || !strings.Contains(err.Error(), "test: direct network disabled") {
		t.Fatalf("direct network was not blocked: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	login, err := StartKimiDeviceLogin(ctx, region, proxy)
	if err != nil {
		t.Fatal(err)
	}
	if login.UserCode != "user-test" {
		t.Fatalf("user code %q", login.UserCode)
	}
	token, err := login.Wait(ctx)
	if err != nil || token.AccessToken != "access-test" || token.RefreshToken != "refresh-test" {
		t.Fatalf("login token %#v: %v", token, err)
	}
	store := NewKimiCredentialStore(privateKimiTestDir(t))
	attempt, err := store.BeginLogin(ctx, region)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteLogin(ctx, region, attempt, KimiCredential{AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, ExpiresAt: time.Now().Unix() + 30, ExpiresIn: token.ExpiresIn, DeviceID: token.DeviceID}); err != nil {
		t.Fatal(err)
	}
	refresher, err := NewKimiCredentialRefresherForRegion(store, region, proxy)
	if err != nil {
		t.Fatal(err)
	}
	access, err := refresher.AccessToken(ctx)
	if err != nil || access != "rotated-test" {
		t.Fatalf("refresh %q: %v", access, err)
	}
	for _, mode := range []string{"subscription", "platform"} {
		models, err := ListKimiModels(ctx, mode, region, access, proxy)
		if err != nil || len(models) != 1 || models[0].ID != "model-test" || models[0].ContextWindow != 8192 {
			t.Fatalf("%s models %#v: %v", mode, models, err)
		}
		provider, err := NewKimiChatProvider(ProviderInput{Mode: mode, Region: region, ProxyURL: proxy, Model: "model-test"}, func(context.Context) (string, error) { return access, nil })
		if err != nil {
			t.Fatal(err)
		}
		response, err := provider.Complete(ctx, []Message{{Role: RoleUser, Content: "hello"}}, nil)
		if err != nil || response.Content != "stand-answer" {
			t.Fatalf("%s inference %#v: %v", mode, response, err)
		}
	}
	usage, err := FetchKimiUsage(ctx, region, access, proxy)
	if err != nil || usage.Limit5H == nil || usage.Limit5H.UsedRatio != .25 {
		t.Fatalf("usage %#v: %v", usage, err)
	}
}
