package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

const kimiBDDToken = "synthetic-kimi-bdd-access"

type kimiBDDState struct {
	t      *testing.T
	server *httptest.Server
	store  *KimiCredentialStore
	source *KimiSubscriptionSource
	answer *Response
	proxy  *httptest.Server
	hits   chan string
	seen   []string
	region string
	body   map[string]any
}

func (s *kimiBDDState) stand() error {
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/oauth/device_authorization":
			_, _ = w.Write([]byte(`{"device_code":"synthetic-device","user_code":"TEST-CODE","verification_uri_complete":"https://www.kimi.ai/code/authorize_device?user_code=TEST-CODE","expires_in":600,"interval":1}`))
		case "/api/oauth/token":
			_, _ = w.Write([]byte(`{"access_token":"` + kimiBDDToken + `","refresh_token":"synthetic-refresh","expires_in":7200}`))
		case "/models":
			if r.Header.Get("Authorization") != "Bearer "+kimiBDDToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"kimi-stand","limit":{"context":131072}}]}`))
		case "/usages":
			if r.Header.Get("Authorization") != "Bearer "+kimiBDDToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"usages":{"limit_5h":{"used_ratio":0.25},"limit_7d":{"used_ratio":0.5}}}`))
		case "/chat/completions":
			if r.Header.Get("Authorization") != "Bearer "+kimiBDDToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			s.body = nil
			_ = json.NewDecoder(r.Body).Decode(&s.body)
			// The subscription endpoint takes only temperature 1 and refuses
			// any other value, whatever the model.
			if temp, ok := s.body["temperature"]; ok && temp != float64(1) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"message":"invalid temperature: only 1 is allowed for this model","type":"invalid_request_error"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Kimi stand answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	s.t.Cleanup(s.server.Close)
	dir := s.t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	s.store = NewKimiCredentialStore(dir)
	return nil
}

func (s *kimiBDDState) signIn() error {
	ctx := context.Background()
	attempt, err := s.store.BeginLogin(ctx, "global")
	if err != nil {
		return err
	}
	login, err := startKimiDeviceLogin(ctx, "global", s.server.Client(), s.server.URL)
	if err != nil {
		return err
	}
	token, err := login.Wait(ctx)
	if err != nil {
		return err
	}
	return s.store.CompleteLogin(ctx, "global", attempt, KimiCredential{
		AccessToken: token.AccessToken, RefreshToken: token.RefreshToken,
		ExpiresAt: token.ExpiresAt, ExpiresIn: token.ExpiresIn, DeviceID: token.DeviceID,
	})
}

func (s *kimiBDDState) stored() error {
	credential, state, err := s.store.Load("global")
	if err != nil {
		return err
	}
	if state != KimiCredentialValid || credential.ExpiresIn != 7200 || !kimiDeviceIDPattern.MatchString(credential.DeviceID) {
		return fmt.Errorf("credential lifetime or device identity was not preserved")
	}
	s.source, err = NewKimiSubscriptionSource(s.store, "subscription", "global", "none", true)
	if err != nil {
		return err
	}
	token, kind, err := s.source.AccessToken(context.Background())
	if err != nil {
		return err
	}
	if token != kimiBDDToken || kind != KimiSourceManaged {
		return fmt.Errorf("the managed credential was not selected")
	}
	return nil
}

func (s *kimiBDDState) catalog() error {
	token, _, err := s.source.AccessToken(context.Background())
	if err != nil {
		return err
	}
	models, err := fetchKimiModels(context.Background(), "subscription", token, s.server.URL+"/models", s.server.Client())
	if err != nil {
		return err
	}
	if len(models) != 1 || models[0].ID != "kimi-stand" || models[0].Protocol != "kimi" {
		return fmt.Errorf("unexpected model catalog")
	}
	return nil
}

func (s *kimiBDDState) usage() error {
	token, _, err := s.source.AccessToken(context.Background())
	if err != nil {
		return err
	}
	usage, err := fetchKimiUsage(context.Background(), token, s.server.URL+"/usages", s.server.Client())
	if err != nil {
		return err
	}
	if usage.Limit5H == nil || usage.Limit7D == nil || usage.Limit5H.UsedRatio != 0.25 || usage.Limit7D.UsedRatio != 0.5 {
		return fmt.Errorf("subscription windows were not preserved")
	}
	return nil
}

func (s *kimiBDDState) ask() error {
	transport := &KimiInferenceTransport{
		endpoint: s.server.URL + "/chat/completions", client: s.server.Client(),
		source: func(ctx context.Context) (string, error) {
			token, _, err := s.source.AccessToken(ctx)
			return token, err
		},
	}
	var err error
	s.answer, err = newKimiChatProvider(transport, ProviderInput{Model: "kimi-stand"}).Complete(context.Background(), []Message{{Role: RoleUser, Content: "Hello"}}, nil)
	return err
}

// askConfiguredTemperature runs the request of a models[] row that configures
// a temperature, the way Coddy's own catalog import writes one.
func (s *kimiBDDState) askConfiguredTemperature() error {
	transport := &KimiInferenceTransport{
		endpoint: s.server.URL + "/chat/completions", client: s.server.Client(),
		source: func(context.Context) (string, error) { return kimiBDDToken, nil },
	}
	in := ProviderInput{Model: "kimi-stand", Temperature: 0.2, ReasoningEffort: "low"}
	var err error
	s.answer, err = newKimiChatProvider(transport, in).Complete(context.Background(), []Message{{Role: RoleUser, Content: "Hello"}}, nil)
	return err
}

func (s *kimiBDDState) answeredWithoutTemperature() error {
	if s.body == nil {
		return fmt.Errorf("the stand recorded no request")
	}
	if _, ok := s.body["temperature"]; ok {
		return fmt.Errorf("the request carried temperature %v", s.body["temperature"])
	}
	return s.answered()
}

func (s *kimiBDDState) answered() error {
	if s.answer == nil || s.answer.Content != "Kimi stand answer" {
		return fmt.Errorf("unexpected model answer")
	}
	return nil
}

func (s *kimiBDDState) proxyStand() error {
	s.region = "global"
	s.hits = make(chan string, 8)
	s.seen = nil
	s.proxy = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits <- r.Method + " " + r.Host
		w.WriteHeader(http.StatusBadGateway)
	}))
	s.t.Cleanup(s.proxy.Close)
	dir := s.t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	s.store = NewKimiCredentialStore(dir)
	return nil
}

func (s *kimiBDDState) chinaProxyStand() error {
	if err := s.proxyStand(); err != nil {
		return err
	}
	s.region = "china"
	return nil
}

func (s *kimiBDDState) proxyRequests() error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	attempt, err := s.store.BeginLogin(ctx, s.region)
	if err != nil {
		return err
	}
	err = s.store.CompleteLogin(ctx, s.region, attempt, KimiCredential{
		AccessToken: "expired-synthetic", RefreshToken: "synthetic-refresh",
		ExpiresAt: time.Now().Add(-time.Hour).Unix(), ExpiresIn: 7200,
	})
	if err != nil {
		return err
	}
	refresher, err := NewKimiCredentialRefresherForRegion(s.store, s.region, s.proxy.URL)
	if err != nil {
		return err
	}
	client, err := HTTPClientForProviderProxy(s.proxy.URL)
	if err != nil {
		return err
	}
	endpoint, err := kimiOAuthEndpoint(s.region)
	if err != nil {
		return err
	}
	login := &KimiDeviceLogin{client: client, endpoint: endpoint, code: "synthetic-device", interval: time.Second, expires: time.Now().Add(time.Minute)}
	chat, err := NewKimiChatProvider(ProviderInput{Model: "kimi-stand", Region: s.region, ProxyURL: s.proxy.URL}, func(context.Context) (string, error) {
		return kimiBDDToken, nil
	})
	if err != nil {
		return err
	}
	requests := []struct {
		name string
		run  func() error
	}{
		{"authorization", func() error { _, err := StartKimiDeviceLogin(ctx, s.region, s.proxy.URL); return err }},
		{"device polling", func() error { _, err := login.Wait(ctx); return err }},
		{"refresh", func() error { _, err := refresher.AccessToken(ctx); return err }},
		{"discovery", func() error {
			_, err := ListKimiModels(ctx, "subscription", s.region, kimiBDDToken, s.proxy.URL)
			return err
		}},
		{"usage", func() error { _, err := FetchKimiUsage(ctx, s.region, kimiBDDToken, s.proxy.URL); return err }},
		{"inference", func() error { _, err := chat.Complete(ctx, nil, nil); return err }},
	}
	for _, request := range requests {
		if err := request.run(); err == nil {
			return fmt.Errorf("%s: expected test proxy rejection", request.name)
		}
		select {
		case got := <-s.hits:
			s.seen = append(s.seen, got)
		case <-ctx.Done():
			return fmt.Errorf("%s: proxy was not reached: %w", request.name, ctx.Err())
		}
	}
	return nil
}

func (s *kimiBDDState) proxyVerified() error {
	return s.verifyProxy("auth.kimi.ai:443", "api.kimi.ai:443")
}

func (s *kimiBDDState) chinaProxyVerified() error {
	return s.verifyProxy("auth.kimi.com:443", "api.kimi.com:443")
}

func (s *kimiBDDState) verifyProxy(auth, api string) error {
	want := []string{"CONNECT " + auth, "CONNECT " + auth, "CONNECT " + auth,
		"CONNECT " + api, "CONNECT " + api, "CONNECT " + api}
	if len(s.seen) != len(want) {
		return fmt.Errorf("proxy saw %d requests, want %d", len(s.seen), len(want))
	}
	for i := range want {
		if s.seen[i] != want[i] {
			return fmt.Errorf("proxy request %d = %q, want %q", i, s.seen[i], want[i])
		}
	}
	return nil
}

func (s *kimiBDDState) platformRequests() error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, region := range []string{"global", "china"} {
		provider, err := NewKimiChatProvider(ProviderInput{Model: "kimi-stand", Mode: "platform", Region: region, ProxyURL: s.proxy.URL}, func(context.Context) (string, error) {
			return "synthetic-platform-key", nil
		})
		if err != nil {
			return err
		}
		requests := []func() error{
			func() error {
				_, err := ListKimiModels(ctx, "platform", region, "synthetic-platform-key", s.proxy.URL)
				return err
			},
			func() error { _, err := provider.Complete(ctx, nil, nil); return err },
		}
		for _, request := range requests {
			if err := request(); err == nil {
				return fmt.Errorf("%s Platform: expected test proxy rejection", region)
			}
			select {
			case got := <-s.hits:
				s.seen = append(s.seen, got)
			case <-ctx.Done():
				return fmt.Errorf("%s Platform: proxy was not reached: %w", region, ctx.Err())
			}
		}
	}
	return nil
}

func (s *kimiBDDState) platformProxyVerified() error {
	want := []string{"CONNECT api.moonshot.ai:443", "CONNECT api.moonshot.ai:443", "CONNECT api.moonshot.cn:443", "CONNECT api.moonshot.cn:443"}
	if len(s.seen) != len(want) {
		return fmt.Errorf("proxy saw %d Platform requests, want %d", len(s.seen), len(want))
	}
	for i := range want {
		if s.seen[i] != want[i] {
			return fmt.Errorf("Platform proxy request %d = %q, want %q", i, s.seen[i], want[i])
		}
	}
	return nil
}

func TestKimiProviderFeature(t *testing.T) {
	state := &kimiBDDState{t: t}
	paths := []string{"../../features/kimi_provider.feature"}
	if !KimiRefreshSupported() {
		// The scenarios that need no managed credential store.
		paths = []string{"../../features/kimi_provider.feature:15", "../../features/kimi_provider.feature:30"}
	}
	suite := godog.TestSuite{
		Name:    "kimi-provider",
		Options: &godog.Options{Format: "pretty", Paths: paths, TestingT: t, Strict: true},
		ScenarioInitializer: func(ctx *godog.ScenarioContext) {
			ctx.Step(`^a Kimi subscription stand in the global region$`, state.stand)
			ctx.Step(`^the operator completes the Kimi device authorization$`, state.signIn)
			ctx.Step(`^Coddy stores its own credential for the global region$`, state.stored)
			ctx.Step(`^the subscription model catalog uses that credential$`, state.catalog)
			ctx.Step(`^the subscription usage contains its independent limits$`, state.usage)
			ctx.Step(`^Coddy sends a model request with its stored credential$`, state.ask)
			ctx.Step(`^the model answers with the stand's response$`, state.answered)
			ctx.Step(`^Coddy sends a model request for a model row that configures a temperature$`, state.askConfiguredTemperature)
			ctx.Step(`^the subscription request carries no temperature and the model answers$`, state.answeredWithoutTemperature)
			ctx.Step(`^a Kimi provider configured with a local proxy$`, state.proxyStand)
			ctx.Step(`^Coddy starts authorization, polls the device and requests refresh, discovery, usage and inference$`, state.proxyRequests)
			ctx.Step(`^each request reaches the configured proxy$`, state.proxyVerified)
			ctx.Step(`^a China Kimi provider configured with a local proxy$`, state.chinaProxyStand)
			ctx.Step(`^each China request reaches the configured proxy$`, state.chinaProxyVerified)
			ctx.Step(`^a Kimi Platform provider configured with a local proxy$`, state.proxyStand)
			ctx.Step(`^Coddy requests Platform discovery and inference in both regions$`, state.platformRequests)
			ctx.Step(`^each Platform request reaches the configured proxy$`, state.platformProxyVerified)
		},
	}
	if suite.Run() != 0 {
		t.Fatal("Kimi provider feature suite failed")
	}
}
