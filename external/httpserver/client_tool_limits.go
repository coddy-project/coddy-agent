//go:build http

package httpserver

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

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

// clientToolError is a tool list refused for a bound it broke; code is the
// error.code the client receives.
type clientToolError struct {
	code string
	msg  string
}

func (e *clientToolError) Error() string { return e.msg }

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
// and, for a broken bound, its code. A broken bound always concerns "tools",
// so it never meets the reading below, which is all the other refusals have:
// a tool a client happens to name "tool_choice" would otherwise be reported
// against the field of that name.
func writeClientToolError(w http.ResponseWriter, err error) {
	var limitErr *clientToolError
	if errors.As(err, &limitErr) {
		writeOpenAIValidationErrorCode(w, err.Error(), "tools", limitErr.code)
		return
	}
	param := "tools"
	if strings.Contains(err.Error(), "tool_choice") {
		param = "tool_choice"
	}
	writeOpenAIValidationError(w, err.Error(), param)
}
