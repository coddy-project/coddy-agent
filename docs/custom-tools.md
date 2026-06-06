# Custom Tools Guide

## Overview

The agent uses a **tool registry** (`internal/tools`) to expose capabilities to the LLM. Every
action the agent can take — reading files, running commands, searching code — is implemented as
a tool. This guide explains how to add new built-in tools directly to the agent binary.

If you want to add tools without modifying the agent source, use
[MCP servers](./mcp-integration.md) instead.

---

## How the Tool System Works

```
User prompt
    │
    ▼
ReAct loop (internal/agent/react.go)
    │
    ├── tools.NewRegistryFor(cfg) registers builtins (internal/tools/export.go)
    │
    ├── Registry.AllToolDefinitions()
    │
    ├── FilterToolDefinitions(..., ToolSetForMode(mode))  ← internal/agent/toolsets.go
    │
    ├── (agent or plan mode) append MCP tool definitions
    │
    ├── passes tool definitions to LLM via provider.Stream()
    │
    └── on tool_call in LLM response:
            │
            ├── permission check (ACP)
            │
            └── registry.Execute(name, argsJSON, env)  →  tool.Execute()
```

The LLM receives a list of tool **definitions** (name, description, JSON Schema). When the LLM
decides to call a tool, the agent executes it and feeds the result back into the conversation.

---

## The `Tool` Struct

Every tool is a value of type `tools.Tool`, a type alias for `tooling.Tool` defined in
[`internal/tooling/tool.go`](../internal/tooling/tool.go):

```go
type Tool struct {
    Definition llm.ToolDefinition

    // RequiresPermission indicates the tool needs user approval before running.
    RequiresPermission bool

    Execute func(ctx context.Context, argsJSON string, env *Env) (string, error)
}
```

### `llm.ToolDefinition`

This is what the LLM sees:

```go
type ToolDefinition struct {
    Name        string      `json:"name"`
    Description string      `json:"description"`
    InputSchema interface{} `json:"input_schema"` // JSON Schema object
}
```

- `Name` - unique snake_case identifier, e.g. `fetch_url`, `git_log`
- `Description` - plain English description; the LLM uses this to decide when to call the tool
- `InputSchema` - standard [JSON Schema](https://json-schema.org/) describing the tool's arguments

### `tools.Env`

The `Env` struct is passed to every `Execute` call and provides session context:

```go
type Env struct {
    CWD              string   // session working directory
    PermissionMode   string   // "ask", "accept_edits", or "bypass"
    CommandAllowlist []string // commands that skip permission checks

    // Plan/todo support (always populated by the ReAct agent):
    SessionID string                      // current session ID
    Sender    acp.UpdateSender            // sends session/update to connected ACP client
    GetPlan   func() []acp.PlanEntry      // read current todo list
    SetPlan   func([]acp.PlanEntry)       // replace todo list
}
```

Use `env.CWD` as the base for relative paths. Use `resolvePath(path, env.CWD)` (package-private
helper) to convert user-supplied paths to absolute paths.

If your tool wants to push plan state to the client, call `sendPlanUpdate(env, entries)` — a
package-private helper in `internal/tools/todo.go` that nil-checks `Sender` before sending.

---

## Step-by-Step: Creating a Built-in Tool

### 1. Create the tool constructor

Add a new `.go` file in `internal/tools/` (or add to an existing file if thematically related).

```go
package tools

import (
    "context"
    "fmt"
    "net/http"

    "github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// fetchURLTool returns a tool that performs an HTTP GET request.
func fetchURLTool() *Tool {
    return &Tool{
        Definition: llm.ToolDefinition{
            Name:        "fetch_url",
            Description: "Perform an HTTP GET request and return the response body as text.",
            InputSchema: map[string]interface{}{
                "type": "object",
                "properties": map[string]interface{}{
                    "url": map[string]interface{}{
                        "type":        "string",
                        "description": "Full URL to fetch, e.g. https://example.com/api/data",
                    },
                },
                "required": []string{"url"},
            },
        },
        RequiresPermission: false,
        Execute:            executeFetchURL,
    }
}
```

### 2. Define the arguments struct

```go
type fetchURLArgs struct {
    URL string `json:"url"`
}
```

### 3. Implement the `Execute` function

```go
func executeFetchURL(ctx context.Context, argsJSON string, _ *Env) (string, error) {
    args, err := parseArgs[fetchURLArgs](argsJSON)
    if err != nil {
        return "", err
    }

    req, err := http.NewRequestWithContext(ctx, http.MethodGet, args.URL, nil)
    if err != nil {
        return "", fmt.Errorf("fetch_url: invalid request: %w", err)
    }

    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        return "", fmt.Errorf("fetch_url: %w", err)
    }
    defer resp.Body.Close()

    body := make([]byte, 0, 4096)
    buf := make([]byte, 512)
    for {
        n, readErr := resp.Body.Read(buf)
        body = append(body, buf[:n]...)
        if readErr != nil {
            break
        }
    }

    return fmt.Sprintf("HTTP %d\n%s", resp.StatusCode, string(body)), nil
}
```

Key rules for `Execute`:
- Return `("", error)` for real errors (wrong args, IO failure, etc.)
- Return `(errorMessage, nil)` when the command runs but produces a non-success result — this
  lets the LLM see the failure and decide what to do next
- Use `context.Context` for cancellation and timeouts
- Use `parseArgs[T]` to unmarshal JSON arguments into a typed struct

### 4. Register the tool in `NewRegistryFor()`

Open `internal/tools/export.go` and add `r.Register(...)` inside `NewRegistryFor()` (or register from a helper such as `internal/tools/fs/register.go` if you are extending the filesystem bundle).

```go
func NewRegistryFor(cfg *config.Config) *Registry {
    r := tooling.NewRegistry()
    // ...
    r.Register(fetchURLTool()) // <-- add here
    registerSchedulerTools(r, cfg)
    return r
}
```

That's it. After rebuilding (`go build ./...`), the LLM will see the new tool.

---

## Tool Fields Reference

### Plan vs agent exposure

Mode-specific visibility is **not** configured on the `Tool` struct. Instead, `internal/agent/toolsets.go`
defines a `ToolSet` allowlist:

- **`agent`** mode uses an **empty** `ToolSet`, which means **no filtering** - every tool in the registry is advertised, and MCP tools are appended.
- **`plan`** mode filters **registry** builtins to a **fixed allowlist** (`read_file`, `list_dir`, `search_files`, `search_web`, `extract_page_content`, `run_command`). Other builtins (writes, todo tools, scheduler, memory, etc.) stay registered for execution consistency but are **hidden** from the LLM. **MCP** tool definitions from connected servers are **still appended** after that filter, same wiring as agent mode.

When you add a new built-in that should be plan-safe, append its name to `planToolNames` in `internal/agent/toolsets.go` and extend tests in `internal/agent/toolsets_test.go`.

### `RequiresPermission`

When `true`, the ReAct loop pauses before calling the tool and sends a
`session/request_permission` notification to the ACP client. The user must approve the call
before execution proceeds.

Use `RequiresPermission: true` for:
- Tools that execute external processes or shell commands
- Tools that delete or irreversibly modify data outside clearly read-only flows

Network tools may still use `RequiresPermission: false` when the implementation enforces its own guardrails (for example SSRF checks and response size limits on `extract_page_content`).

Use `RequiresPermission: false` for:
- Read-only operations
- Writes within the working directory (can be controlled by `env.RequirePermissionForWrites`)

---

## JSON Schema for `InputSchema`

`InputSchema` is a standard JSON Schema object. The most common patterns:

### Required string field

```go
"url": map[string]interface{}{
    "type":        "string",
    "description": "URL to fetch",
},
```

### Optional integer field with default

```go
"timeout_seconds": map[string]interface{}{
    "type":        "integer",
    "description": "Timeout in seconds (default: 30)",
},
```

### Enum field

```go
"method": map[string]interface{}{
    "type":        "string",
    "enum":        []string{"GET", "POST", "PUT", "DELETE"},
    "description": "HTTP method",
},
```

### Boolean field

```go
"follow_redirects": map[string]interface{}{
    "type":        "boolean",
    "description": "Follow HTTP redirects (default: true)",
},
```

### Marking required fields

```go
"required": []string{"url", "method"},
```

---

## Complete Example: `git_log` Tool

Below is a complete, production-ready example of a tool that runs `git log` in the working
directory and returns a formatted summary.

**`internal/tools/git.go`:**

```go
package tools

import (
    "bytes"
    "context"
    "fmt"
    "os/exec"

    "github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func gitLogTool() *Tool {
    return &Tool{
        Definition: llm.ToolDefinition{
            Name:        "git_log",
            Description: "Show recent git commit history for the working directory repository.",
            InputSchema: map[string]interface{}{
                "type": "object",
                "properties": map[string]interface{}{
                    "limit": map[string]interface{}{
                        "type":        "integer",
                        "description": "Number of commits to show (default: 10)",
                    },
                    "branch": map[string]interface{}{
                        "type":        "string",
                        "description": "Branch name (default: current branch)",
                    },
                },
            },
        },
        RequiresPermission: false,
        Execute:            executeGitLog,
    }
}

type gitLogArgs struct {
    Limit  int    `json:"limit"`
    Branch string `json:"branch"`
}

func executeGitLog(ctx context.Context, argsJSON string, env *Env) (string, error) {
    args, err := parseArgs[gitLogArgs](argsJSON)
    if err != nil {
        return "", err
    }

    limit := 10
    if args.Limit > 0 {
        limit = args.Limit
    }

    cmdArgs := []string{
        "log",
        fmt.Sprintf("--max-count=%d", limit),
        "--oneline",
        "--decorate",
    }
    if args.Branch != "" {
        cmdArgs = append(cmdArgs, args.Branch)
    }

    cmd := exec.CommandContext(ctx, "git", cmdArgs...)
    cmd.Dir = env.CWD

    var out bytes.Buffer
    cmd.Stdout = &out
    cmd.Stderr = &out

    if err := cmd.Run(); err != nil {
        return fmt.Sprintf("git log failed: %v\n%s", err, out.String()), nil
    }

    result := out.String()
    if result == "" {
        return "(no commits)", nil
    }
    return result, nil
}
```

Then in `export.go`:

```go
r.Register(gitLogTool())
```

---

## Checklist

Before submitting a new tool, verify:

- [ ] Tool name is unique and follows `snake_case` convention
- [ ] Description clearly explains what the tool does and when to use it
- [ ] All required fields are listed in `InputSchema.required`
- [ ] Optional fields have sensible defaults documented in their description
- [ ] If the tool must be visible in **plan** mode, add its name to `planToolNames` in `internal/agent/toolsets.go` and extend `internal/agent/toolsets_test.go`
- [ ] `RequiresPermission` is set to `true` for destructive shell commands or sensitive writes
- [ ] `Execute` returns `("", error)` for argument parsing failures
- [ ] `Execute` returns `(errorMessage, nil)` for runtime failures so the LLM can see them
- [ ] The tool constructor is registered in `NewRegistry()`
- [ ] `go build ./...` and `go test ./...` pass

---

## Built-in Plan / Todo Tools

Seven **`coddy_`** tools (`internal/tools/todo`) drive the checklist. Plan mutations emit
`session/update` with **`plan`** payloads. They are registered for every session but **only advertised to the LLM in `agent` mode** (plan mode hides them via `ToolSet`).

| Tool name | Purpose |
|-----------|---------|
| **`coddy_todo_plan_read`** | Return the markdown checklist rendering of the active plan (`{}`). |
| **`coddy_todo_plan_replace`** | Swap the entire plan (`markdown`). Rejected while any row is unfinished unless you **`coddy_todo_plan_archive`** first. Completed lists archive **`todos/active.md`** before swapping. |
| **`coddy_todo_plan_archive`** | Mark every unfinished row **`completed`**, write **`todos/archive/plan_<unix_seconds>.md`** when **`SessionDir`**, clear in-memory plan, emit empty **`plan`**. |
| **`coddy_todo_item_add`** | Append or insert (`content`, optional `status`, optional `after_index`; `-1` prepends). |
| **`coddy_todo_item_remove`** | Drop a row (`index`). |
| **`coddy_todo_item_update`** | Mutate (`index` plus **`content`** and/or **`status`**). Status enum is `pending`, `in_progress`, `completed`, `failed`, `cancelled`. |
| **`coddy_todo_item_move`** | Re-order (`from_index`, `to_index`; indices apply after deletion semantics). |

Example wholesale replace:

```json
{
  "markdown": "- [ ] Read existing code\n- [ ] Write tests\n- [ ] Implement feature\n- [ ] Update docs"
}
```

Example status bump:

```json
{ "index": 0, "status": "in_progress" }
```

ACP-capable editors map plan states to their UI. Markdown markers map roughly to:

| Symbol | Status |
|--------|--------|
| `[ ]` | pending |
| `[>]` | in_progress |
| `[x]` | completed |
| `[!]` | failed |
| `[-]` | cancelled |

---

## Alternative: MCP Servers

If you want to add tools without modifying the agent source (e.g. for project-specific tools,
third-party integrations, or tools in other languages), use the
[MCP server integration](./mcp-integration.md).

MCP tools are registered at runtime with the prefix `serverName__toolName` and follow the same
`RequiresPermission`/plan allowlist model as built-in tools.
