//go:build http && swarm

package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"

	swarmserver "github.com/EvilFreelancer/coddy-agent/external/swarm"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

type swarmFileTransferState struct {
	root      string
	workspace string
	sessionID string

	node  *httptest.Server
	relay *httptest.Server
	mgr   *session.Manager

	relayToken string
	nodeToken  string

	mu                sync.Mutex
	uploadRequestAuth string
	fileRequestAuth   []string
	previewURL        string
	artifactURL       string
	mediaAddress      string
	rawRequests       []rawRequest
}

// rawRequest is what the node was asked on its workspace raw route.
type rawRequest struct {
	authorization string
	accessToken   string
}

const swarmFileTransferPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

func (s *swarmFileTransferState) reset() {
	if s.relay != nil {
		s.relay.Close()
		s.relay = nil
	}
	if s.node != nil {
		s.node.Close()
		s.node = nil
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
	s.workspace = ""
	s.sessionID = ""
	s.mgr = nil
	s.relayToken = ""
	s.nodeToken = ""
	s.mu.Lock()
	s.uploadRequestAuth = ""
	s.fileRequestAuth = nil
	s.rawRequests = nil
	s.mu.Unlock()
	s.mediaAddress = ""
	s.previewURL = ""
	s.artifactURL = ""
}

func (s *swarmFileTransferState) mountedFileCapableNode() error {
	s.reset()
	root, err := os.MkdirTemp("", "coddy-swarm-files-*")
	if err != nil {
		return err
	}
	s.root = root
	s.workspace = filepath.Join(root, "workspace")
	if err := os.MkdirAll(s.workspace, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(s.workspace, "report.txt"), []byte("report bytes"), 0o644); err != nil {
		return err
	}

	s.relayToken = "relay-client-token"
	s.nodeToken = "node-service-token"
	cfg := &config.Config{
		Paths:      config.Paths{Home: filepath.Join(root, "home"), CWD: s.workspace},
		Models:     []config.ModelEntry{{Model: "fake/model", MaxTokens: 128, Multimodal: config.BoolPtr(true)}},
		Agent:      config.Agent{Model: "fake/model"},
		HTTPServer: config.HTTPServerConfig{AuthToken: s.nodeToken},
	}
	store := &session.FileStore{Root: filepath.Join(root, "sessions")}
	s.mgr = session.NewManager(cfg, noopSender{}, func(_ context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		imageParts := st.TakePendingImageParts()
		for _, block := range prompt {
			if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
				st.AddMessage(llm.Message{Role: llm.RoleUser, Content: block.Text, ImageParts: imageParts})
				imageParts = nil
			}
		}
		return string(acp.StopReasonEndTurn), nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), s.workspace, store)
	nodeServer := New(cfg, s.mgr, slog.New(slog.NewTextHandler(io.Discard, nil)), s.workspace)
	nodeServer.makeLLMFromYAML = func(*config.Config, string, llm.RequestOptions) (llm.Provider, error) {
		return remoteStubProvider{}, nil
	}
	s.node = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/responses" {
			s.mu.Lock()
			s.uploadRequestAuth = r.Header.Get("Authorization")
			s.mu.Unlock()
		}
		if strings.HasSuffix(r.URL.Path, "/workspace/raw") {
			s.mu.Lock()
			s.rawRequests = append(s.rawRequests, rawRequest{
				authorization: r.Header.Get("Authorization"),
				accessToken:   r.URL.Query().Get("access_token"),
			})
			s.mu.Unlock()
		}
		if strings.Contains(r.URL.Path, "/assets/") || strings.Contains(r.URL.Path, "/artifacts/") {
			s.mu.Lock()
			s.fileRequestAuth = append(s.fileRequestAuth, r.Header.Get("Authorization"))
			s.mu.Unlock()
		}
		nodeServer.Handler().ServeHTTP(w, r)
	}))

	relayCfg := &config.Config{}
	relayCfg.Swarm.Host = "127.0.0.1"
	relayCfg.Swarm.AuthToken = s.relayToken
	relayCfg.Swarm.PairingTokens = []string{"relay-pair-token"}
	relayServer, err := swarmserver.New(relayCfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return err
	}
	s.relay = httptest.NewServer(relayServer.Handler())

	registration, err := json.Marshal(swarmdto.RegisterRequest{
		Name:         "nas02",
		Kind:         swarmdto.KindAgent,
		Transport:    swarmdto.TransportDirect,
		AdvertiseURL: s.node.URL,
		InstanceUUID: "swarm-file-transfer-node",
		Token:        s.nodeToken,
		Version:      "test",
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, s.relay.URL+"/swarm/register", bytes.NewReader(registration))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer relay-pair-token")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("register node: status %d: %s", res.StatusCode, body)
	}

	created, err := s.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.workspace})
	if err != nil {
		return err
	}
	s.sessionID = created.SessionID
	return nil
}

func (s *swarmFileTransferState) mountedURL(path string) string {
	return s.relay.URL + swarmdto.MountPath + "nas02" + path
}

func (s *swarmFileTransferState) request(method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, s.mountedURL(path), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.relayToken)
	if s.sessionID != "" {
		req.Header.Set("X-Coddy-Session-ID", s.sessionID)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return http.DefaultClient.Do(req)
}

func (s *swarmFileTransferState) uploadPNG() error {
	payload := map[string]interface{}{
		"model":  "agent",
		"input":  "inspect this image",
		"stream": false,
		"inline_files": []map[string]string{{
			"name":     "upload.png",
			"data_url": "data:image/png;base64," + swarmFileTransferPNG,
		}},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	res, err := s.request(http.MethodPost, "/v1/responses", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("upload: status %d: %s", res.StatusCode, body)
	}
	return nil
}

func (s *swarmFileTransferState) shareReport() error {
	st := s.mgr.SessionByID(s.sessionID)
	if st == nil {
		return fmt.Errorf("session %q is missing", s.sessionID)
	}
	artifact, err := session.CaptureArtifact(st.GetPersistedSessionDir(), s.workspace, "report.txt")
	if err != nil {
		return err
	}
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "share", Name: "share_file", InputJSON: `{"path":"report.txt"}`}}})
	st.AddMessage(llm.Message{Role: llm.RoleTool, ToolCallID: "share", Content: `{"artifact":{"id":"` + artifact.ID + `"}}`, Artifacts: []llm.Artifact{{ID: artifact.ID, Name: artifact.Name, SHA256: artifact.SHA256, Size: artifact.Size, SourcePath: artifact.SourcePath, SourceRelativePath: artifact.SourceRelativePath}}})
	return nil
}

func (s *swarmFileTransferState) nodeReceivedOwnCredentialForUpload() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.uploadRequestAuth != "Bearer "+s.nodeToken {
		return fmt.Errorf("node received upload authorization %q, want its own credential", s.uploadRequestAuth)
	}
	return nil
}

func (s *swarmFileTransferState) uploadedThumbnailAvailable() error {
	res, err := s.request(http.MethodGet, "/coddy/sessions/"+s.sessionID+"/messages", nil)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("messages: status %d: %s", res.StatusCode, body)
	}
	var body struct {
		Messages []struct {
			Files []struct {
				PreviewURL string `json:"preview_url"`
			} `json:"files"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return err
	}
	for _, message := range body.Messages {
		for _, file := range message.Files {
			if file.PreviewURL != "" {
				s.previewURL = file.PreviewURL
				break
			}
		}
	}
	if s.previewURL == "" {
		return fmt.Errorf("messages did not contain an uploaded image preview")
	}
	res, err = s.request(http.MethodGet, s.previewURL, nil)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	image, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "image/png" || !bytes.HasPrefix(image, []byte("\x89PNG\r\n\x1a\n")) {
		return fmt.Errorf("thumbnail: status %d, type %q, bytes %d", res.StatusCode, res.Header.Get("Content-Type"), len(image))
	}
	return nil
}

func (s *swarmFileTransferState) sharedReportDownloadAvailable() error {
	res, err := s.request(http.MethodGet, "/coddy/sessions/"+s.sessionID+"/tool-calls/share", nil)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("tool call: status %d: %s", res.StatusCode, body)
	}
	var body struct {
		Artifacts []struct {
			URL string `json:"url"`
		} `json:"artifacts"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return err
	}
	if len(body.Artifacts) != 1 || body.Artifacts[0].URL == "" {
		return fmt.Errorf("tool call did not contain an artifact download URL")
	}
	s.artifactURL = body.Artifacts[0].URL
	res, err = s.request(http.MethodGet, s.artifactURL, nil)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	download, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK || string(download) != "report bytes" {
		return fmt.Errorf("artifact: status %d, body %q", res.StatusCode, download)
	}
	return nil
}

func (s *swarmFileTransferState) nodeReceivedOwnCredentialForFileRequests() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.fileRequestAuth) < 2 {
		return fmt.Errorf("node received %d file requests, want at least 2", len(s.fileRequestAuth))
	}
	for _, auth := range s.fileRequestAuth {
		if auth != "Bearer "+s.nodeToken {
			return fmt.Errorf("node received %q, want its own credential", auth)
		}
	}
	return nil
}

// The web UI asks the node, through the relay, for a short-lived address of one
// workspace file: a native <video> or <audio> element cannot send a header.
func (s *swarmFileTransferState) mintMediaAddress(path string) error {
	raw, err := json.Marshal(map[string]interface{}{"path_rel": path, "download": false})
	if err != nil {
		return err
	}
	res, err := s.request(http.MethodPost, "/coddy/sessions/"+s.sessionID+"/workspace/media-token", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("media token: status %d: %s", res.StatusCode, body)
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return err
	}
	if body.Token == "" {
		return fmt.Errorf("media token: empty token")
	}
	s.mediaAddress = s.mountedURL("/coddy/sessions/" + s.sessionID + "/workspace/raw?path_rel=" + url.QueryEscape(path) + "&download=0&access_token=" + url.QueryEscape(body.Token))
	return nil
}

// The address alone is what the browser has: no Authorization header.
func (s *swarmFileTransferState) mediaStreamsFromAddressAlone(path string) error {
	if s.mediaAddress == "" {
		return fmt.Errorf("no media address was minted")
	}
	req, err := http.NewRequest(http.MethodGet, s.mediaAddress, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", "bytes=0-5")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	want, err := os.ReadFile(filepath.Join(s.workspace, path))
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusPartialContent || string(body) != string(want[:6]) {
		return fmt.Errorf("media through the relay: status %d, body %q, want 206 and %q", res.StatusCode, body, want[:6])
	}
	return nil
}

// The relay cannot check the node's signature, so it must not vouch for the
// request with its own credential: the node checks the capability itself.
func (s *swarmFileTransferState) nodeChecksMediaAddress() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.rawRequests) == 0 {
		return fmt.Errorf("the media request never reached the node")
	}
	last := s.rawRequests[len(s.rawRequests)-1]
	if last.accessToken == "" {
		return fmt.Errorf("the node received no capability to check")
	}
	if last.authorization != "" {
		return fmt.Errorf("the node received authorization %q with the capability", last.authorization)
	}
	return nil
}

func TestSwarmFileTransferFeature(t *testing.T) {
	state := &swarmFileTransferState{}
	suite := godog.TestSuite{
		Name: "swarm-file-transfer",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^a swarm relay mounts an authenticated file-capable node$`, state.mountedFileCapableNode)
			sc.Step(`^I upload a PNG image to the mounted node$`, state.uploadPNG)
			sc.Step(`^the node receives its own credential for the upload$`, state.nodeReceivedOwnCredentialForUpload)
			sc.Step(`^the node shares a report file$`, state.shareReport)
			sc.Step(`^the uploaded image thumbnail is available through the relay$`, state.uploadedThumbnailAvailable)
			sc.Step(`^the shared report is downloadable through the relay$`, state.sharedReportDownloadAvailable)
			sc.Step(`^the node receives its own credential for every file request$`, state.nodeReceivedOwnCredentialForFileRequests)
			sc.Step(`^I ask the mounted node for a media address of "([^"]*)"$`, state.mintMediaAddress)
			sc.Step(`^the first six bytes of "([^"]*)" stream through the relay from that address alone$`, state.mediaStreamsFromAddressAlone)
			sc.Step(`^the node checks the media address itself$`, state.nodeChecksMediaAddress)
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				state.reset()
				return ctx, nil
			})
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/swarm_file_transfer.feature"},
			Strict:   true,
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("swarm file transfer feature failed")
	}
}
