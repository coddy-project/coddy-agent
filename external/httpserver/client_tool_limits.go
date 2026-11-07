//go:build http

package httpserver

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// Defaults of the bounds on the tools a client hands a direct model: OpenAI
// itself takes up to 128 tools, and a schema past 256 KiB is not one coddy
// should carry to a provider.
const (
	defaultMaxClientTools           = 128
	defaultMaxClientToolSchemaBytes = 256 << 10
)

// clientToolLimits is how many tools a client may offer a direct model and how
// large one tool's parameters may be (httpserver.max_client_tools and
// httpserver.max_client_tool_schema_bytes, issue #577).
type clientToolLimits struct {
	tools       int
	schemaBytes int
}

// clientToolLimitsOf reads the limits off a configuration; zero or no
// configuration keeps a default.
func clientToolLimitsOf(cfg *config.Config) clientToolLimits {
	l := clientToolLimits{tools: defaultMaxClientTools, schemaBytes: defaultMaxClientToolSchemaBytes}
	if cfg == nil {
		return l
	}
	if n := cfg.HTTPServer.MaxClientTools; n > 0 {
		l.tools = n
	}
	if n := cfg.HTTPServer.MaxClientToolSchemaBytes; n > 0 {
		l.schemaBytes = n
	}
	return l
}

// modelLimits is what a request on one row of GET /v1/models may carry: the
// bounds a client has to size a request against, which were constants read off
// nothing until a refusal arrived (issue #577). ClientTools says whether the
// row takes the client's tools at all, so a reader tells "coddy's own tools
// run here" from "the bounds are not advertised"; the two tool bounds are left
// out where it is false.
type modelLimits struct {
	ClientTools      bool `json:"client_tools"`
	Tools            int  `json:"tools,omitempty"`
	ToolSchemaBytes  int  `json:"tool_schema_bytes,omitempty"`
	ImagesPerMessage int  `json:"images_per_message"`
	ImageBytes       int  `json:"image_bytes"`
}

// modelLimitsOf is what a direct model row advertises: the configured tool
// bounds and the built-in picture ones.
func modelLimitsOf(l clientToolLimits) modelLimits {
	return modelLimits{
		ClientTools:      true,
		Tools:            l.tools,
		ToolSchemaBytes:  l.schemaBytes,
		ImagesPerMessage: maxImagePartsPerMessage,
		ImageBytes:       maxImagePartBytes,
	}
}

// withoutClientTools is the same bounds for a row that refuses client tools.
func (l modelLimits) withoutClientTools() modelLimits {
	l.ClientTools, l.Tools, l.ToolSchemaBytes = false, 0, 0
	return l
}

// clientToolError is a refused tool list that knows what to answer about
// itself: param is the error.param of the field it concerns, "tools" when
// empty, and code the error.code, which only a broken bound has.
type clientToolError struct {
	param string
	code  string
	msg   string
}

func (e *clientToolError) Error() string { return e.msg }

// badToolChoice is a refusal about tool_choice rather than about the tools.
func badToolChoice(msg string) error {
	return &clientToolError{param: "tool_choice", msg: msg}
}

// tooManyClientTools names the count, the limit and what to change: a client
// whose MCP servers offer more tools than the limit otherwise loses every turn
// with nothing to go on.
func tooManyClientTools(offered, limit int) error {
	return &clientToolError{
		code: "too_many_tools",
		msg: fmt.Sprintf("at most %d tools may be offered, %d were offered: offer fewer (a Coddy client narrows its tools with a toolsets preset) or raise httpserver.max_client_tools on this server",
			limit, offered),
	}
}

// clientToolParametersTooLarge names the tool, the limit and the setting.
func clientToolParametersTooLarge(name string, size, limit int) error {
	return &clientToolError{
		code: "tool_parameters_too_large",
		msg: fmt.Sprintf("tool %q: parameters exceed %d bytes (%d given): raise httpserver.max_client_tool_schema_bytes on this server",
			name, limit, size),
	}
}

// writeClientToolError answers a refused tool list with the field it concerns
// and, for a broken bound, its code. The field is carried by the refusal and
// never read back out of its message: a message names the tool it is about,
// and a client may call a tool "tool_choice", which would then be answered
// against a field the request never sent.
func writeClientToolError(w http.ResponseWriter, err error) {
	param, code := "tools", ""
	var toolErr *clientToolError
	if errors.As(err, &toolErr) {
		if toolErr.param != "" {
			param = toolErr.param
		}
		code = toolErr.code
	}
	writeOpenAIValidationErrorCode(w, err.Error(), param, code)
}
