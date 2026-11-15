package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"

	_ "golang.org/x/image/webp"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/packages/ssestream"
	"github.com/openai/openai-go/responses"
)

// MaxGeneratedImageBytes bounds decoded provider output before allocation.
const MaxGeneratedImageBytes = 20 << 20
const maxGeneratedImages = 4

type imageGenerationKey struct{}

// WithImageGeneration explicitly enables the native Responses tool for one
// request context. It is never inferred from a model name or vision support.
func WithImageGeneration(ctx context.Context) context.Context {
	return context.WithValue(ctx, imageGenerationKey{}, true)
}

// WithoutImageGeneration removes the turn opt-in from auxiliary request contexts.
func WithoutImageGeneration(ctx context.Context) context.Context {
	return context.WithValue(ctx, imageGenerationKey{}, false)
}

// ImageGenerationEnabled reports the caller's explicit per-turn opt-in.
func ImageGenerationEnabled(ctx context.Context) bool {
	enabled, _ := ctx.Value(imageGenerationKey{}).(bool)
	return enabled
}

// GeneratedImage is validated native provider output, not a tool call or an
// image URL to fetch. Data is transient; the agent persists its own asset copy.
type GeneratedImage struct {
	ID       string
	MIMEType string
	Data     []byte
}

func decodeGeneratedImage(id, result string) (GeneratedImage, error) {
	if result == "" || len(result) > base64.StdEncoding.EncodedLen(MaxGeneratedImageBytes) {
		return GeneratedImage{}, fmt.Errorf("generated image is empty or exceeds %d bytes", MaxGeneratedImageBytes)
	}
	data, err := base64.StdEncoding.Strict().DecodeString(result)
	if err != nil || len(data) > MaxGeneratedImageBytes {
		return GeneratedImage{}, fmt.Errorf("generated image contains invalid or oversized base64")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 16_000_000/cfg.Height {
		return GeneratedImage{}, fmt.Errorf("generated image has invalid or excessive dimensions")
	}
	mime := ""
	switch format {
	case "png":
		mime = "image/png"
	case "jpeg":
		mime = "image/jpeg"
	case "webp":
		mime = "image/webp"
	default:
		return GeneratedImage{}, fmt.Errorf("unsupported generated image format %q", format)
	}
	// A valid header does not establish that the pixel payload is complete.
	// Decode only after the dimension gate bounds the decoder's allocation.
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return GeneratedImage{}, fmt.Errorf("generated image cannot be decoded: %w", err)
	}
	return GeneratedImage{ID: id, MIMEType: mime, Data: data}, nil
}

func (p *openAIProvider) streamImageGeneration(ctx context.Context, messages []Message, tools []ToolDefinition, onChunk func(StreamChunk)) (*Response, error) {
	params := buildResponsesParams(p.model, opencodeResponsesEffort(p.reasoningEffort), func(string) []json.RawMessage { return nil }, messages, tools)
	params.Tools = append(params.Tools, param.Override[responses.ToolUnionParam](json.RawMessage(`{"type":"image_generation"}`)))
	if p.maxTokens > 0 {
		params.MaxOutputTokens = openai.Int(int64(p.maxTokens))
	}
	var raw *http.Response
	err := p.client.Post(ctx, "responses", params, &raw, option.WithJSONSet("stream", true), option.WithHeader("Accept", "text/event-stream"))
	stream := ssestream.NewStream[responses.ResponseStreamEventUnion](newCodexEventDecoder(raw), err)
	defer func() { _ = stream.Close() }()
	return consumeResponsesStream(stream, onChunk, "openai", func([]json.RawMessage) string { return "" })
}
