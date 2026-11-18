package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

func generatedPNG(t *testing.T) string {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

func imageEvent(result string) string {
	b, _ := json.Marshal(map[string]any{"type": "response.output_item.done", "item": map[string]any{"id": "ig_1", "type": "image_generation_call", "status": "completed", "result": result, "output_format": "png"}})
	return "data: " + string(b) + "\n\n"
}

func TestChatImageGenerationFeature(t *testing.T) {
	var srv *httptest.Server
	var request map[string]any
	var result *Response
	suite := godog.TestSuite{Name: "chat_image_generation", Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/chat_image_generation.feature"}, TestingT: t}, ScenarioInitializer: func(sc *godog.ScenarioContext) {
		sc.Step(`^an OpenAI Responses endpoint returning a generated PNG$`, func() {
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/responses" {
					t.Errorf("path = %s", r.URL.Path)
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, imageEvent(generatedPNG(t))+"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
			}))
			t.Cleanup(srv.Close)
		})
		sc.Step(`^the caller explicitly enables native image generation$`, func() error {
			p := newOpenAIProvider("gpt-test", "key", srv.URL, srv.Client(), 100, 0, "", "coddy")
			var err error
			result, err = p.Complete(WithImageGeneration(context.Background()), []Message{{Role: RoleUser, Content: "draw a square"}}, nil)
			return err
		})
		sc.Step(`^the request includes the native image generation tool$`, func() error {
			raw, _ := json.Marshal(request["tools"])
			if !strings.Contains(string(raw), `"type":"image_generation"`) {
				return fmt.Errorf("tools = %s", raw)
			}
			return nil
		})
		sc.Step(`^the response contains a typed generated image$`, func() error {
			if result == nil || len(result.GeneratedImages) != 1 || result.GeneratedImages[0].MIMEType != "image/png" {
				return fmt.Errorf("missing typed image: %+v", result)
			}
			return nil
		})
	}}
	if suite.Run() != 0 {
		t.Fatal("feature failed")
	}
}

func TestGeneratedImageRejectsUnsafeData(t *testing.T) {
	for _, value := range []string{"", "https://example.org/image.png", "!!!!", base64.StdEncoding.EncodeToString([]byte("not an image")), strings.Repeat("A", MaxGeneratedImageBytes*2)} {
		if _, err := decodeGeneratedImage("ig_1", value); err == nil {
			t.Errorf("accepted unsafe data of length %d", len(value))
		}
	}
}

func TestGeneratedImageFailureCannotRetryWithoutCallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, imageEvent(generatedPNG(t))+"data: {\"type\":\"error\",\"code\":\"server_error\",\"message\":\"overloaded\"}\n\n")
	}))
	defer srv.Close()
	p := newOpenAIProvider("gpt-test", "key", srv.URL, srv.Client(), 100, 0, "", "coddy")
	_, err := p.Complete(WithImageGeneration(context.Background()), []Message{{Role: RoleUser, Content: "draw"}}, nil)
	if err == nil {
		t.Fatal("expected failure")
	}
	if !strings.Contains(err.Error(), "overloaded") {
		t.Fatal(err)
	}
	if isRetryableLLMError(err) {
		t.Fatal("image output must not be retried")
	}
}

func TestGeneratedImagePreventsFallbackAndSurvivesBlockingErrors(t *testing.T) {
	image := GeneratedImage{ID: "ig_1", MIMEType: "image/png", Data: []byte("validated by transport")}
	partial := &Response{GeneratedImages: []GeneratedImage{image}}
	failure := &streamServerError{code: 503, emitted: true, msg: "overloaded"}
	first := &stubProvider{
		completeFn: func(context.Context, []Message, []ToolDefinition) (*Response, error) { return partial, failure },
		streamFn: func(_ context.Context, _ []Message, _ []ToolDefinition, emit func(StreamChunk)) (*Response, error) {
			emit(StreamChunk{GeneratedImage: &image})
			return partial, failure
		},
	}
	next := &stubProvider{
		completeFn: func(context.Context, []Message, []ToolDefinition) (*Response, error) {
			t.Fatal("fell back after image output")
			return nil, nil
		},
		streamFn: func(context.Context, []Message, []ToolDefinition, func(StreamChunk)) (*Response, error) {
			t.Fatal("fell back after image output")
			return nil, nil
		},
	}
	chain := NewFallbackChain([]FallbackCandidate{{Provider: first, Model: "first"}, {Provider: next, Model: "next"}}, nil)
	ctx := WithImageGeneration(context.Background())
	if got, err := chain.Complete(ctx, nil, nil); got != partial || err == nil {
		t.Fatalf("Complete = %+v, %v", got, err)
	}
	if got, err := chain.Stream(ctx, nil, nil, nil); got != partial || err == nil {
		t.Fatalf("Stream = %+v, %v", got, err)
	}
	if got, err := newBlockingProvider(first).Stream(ctx, nil, nil, nil); got != partial || err == nil {
		t.Fatalf("blocking partial = %+v, %v", got, err)
	}
	if !chunkHasOutput(StreamChunk{ImageGenerationStarted: true}) || !chunkHasOutput(StreamChunk{GeneratedImage: &image}) {
		t.Fatal("native generation is output")
	}
}

func TestGeneratedImageRequiresTerminalAndDeduplicatesItems(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		t.Run(fmt.Sprint(terminal), func(t *testing.T) {
			payload := imageEvent(generatedPNG(t))
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, payload+payload)
				if terminal {
					_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{}}\n\n")
				}
			}))
			defer srv.Close()
			p := newOpenAIProvider("gpt-test", "key", srv.URL, srv.Client(), 100, 0, "", "coddy")
			result, err := p.Complete(WithImageGeneration(context.Background()), nil, nil)
			if (err == nil) != terminal {
				t.Fatalf("terminal %v: %v", terminal, err)
			}
			if result == nil || len(result.GeneratedImages) != 1 {
				t.Fatalf("partial image not retained/deduplicated: %+v", result)
			}
			if err != nil && isRetryableLLMError(err) {
				t.Fatal("image output replayed after EOF")
			}
		})
	}
}

func TestGeneratedImageRejectsTruncatedPayload(t *testing.T) {
	pngData, err := base64.StdEncoding.DecodeString(generatedPNG(t))
	if err != nil {
		t.Fatal(err)
	}
	var jpegData bytes.Buffer
	if err := jpeg.Encode(&jpegData, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		data []byte
	}{{"png", pngData}, {"jpeg", jpegData.Bytes()}} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeGeneratedImage("ig_valid", base64.StdEncoding.EncodeToString(tc.data)); err != nil {
				t.Fatalf("valid image rejected: %v", err)
			}
			var headerOnly []byte
			for end := 1; end < len(tc.data); end++ {
				if _, _, err := image.DecodeConfig(bytes.NewReader(tc.data[:end])); err == nil {
					headerOnly = tc.data[:end]
					break
				}
			}
			if len(headerOnly) == 0 {
				t.Fatal("fixture has no header-only prefix")
			}
			if _, _, err := image.Decode(bytes.NewReader(headerOnly)); err == nil {
				t.Fatal("fixture is not truncated")
			}
			if got, err := decodeGeneratedImage("ig_truncated", base64.StdEncoding.EncodeToString(headerOnly)); err == nil {
				t.Fatalf("accepted truncated %s (%d of %d bytes): %+v", tc.name, len(headerOnly), len(tc.data), got)
			}
		})
	}
}

func TestGeneratedImageDimensionGatePrecedesPixelDecode(t *testing.T) {
	data, err := base64.StdEncoding.DecodeString(generatedPNG(t))
	if err != nil {
		t.Fatal(err)
	}
	header := append([]byte(nil), data[:33]...)
	binary.BigEndian.PutUint32(header[16:20], 16_000_001)
	binary.BigEndian.PutUint32(header[20:24], 1)
	binary.BigEndian.PutUint32(header[29:33], crc32.ChecksumIEEE(header[12:29]))
	cfg, _, err := image.DecodeConfig(bytes.NewReader(header))
	if err != nil || cfg.Width != 16_000_001 || cfg.Height != 1 {
		t.Fatalf("bad oversized-header fixture: %+v %v", cfg, err)
	}
	if _, err := decodeGeneratedImage("ig_oversized", base64.StdEncoding.EncodeToString(header)); err == nil || !strings.Contains(err.Error(), "dimensions") {
		t.Fatalf("oversized dimensions must be rejected before decoding the truncated pixel body: %v", err)
	}
}
