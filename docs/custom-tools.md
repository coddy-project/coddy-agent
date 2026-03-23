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
ReAct loop (internal/react/agent.go)
    │
    ├── builds tools.Registry  ←  NewRegistry() + MCP tools
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

Every tool is a value of type `tools.Tool` defined in `internal/tools/registry.go`:

```go
type Tool struct {
    // Definition is the schema exposed to the LLM.
    Definition llm.ToolDefinition

    // RequiresPermission indicates the tool needs user approval before running.
    RequiresPermission bool

    // AllowedInPlanMode indicates the tool is available in plan mode.
    AllowedInPlanMode bool

    // Execute runs the tool with the given JSON arguments.
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
    CWD                          string   // session working directory
    RestrictToCWD                bool     // deny paths outside CWD
    RequirePermissionForCommands bool     // force permission for run_command
    RequirePermissionForWrites   bool     // force permission for write_file
    CommandAllowlist             []string // commands that skip permission checks

    // Plan/todo support (always populated by the ReAct agent):
    SessionID string                      // current session ID
    Sender    acp.UpdateSender            // sends updates to TUI/ACP client
    GetPlan   func() []acp.PlanEntry      // read current todo list
    SetPlan   func([]acp.PlanEntry)       // replace todo list
}
```

Use `env.CWD` as the base for relative paths. Use `resolvePath(path, env.CWD)` (package-private
helper) to convert user-supplied paths to absolute paths.

If your tool wants to update the plan sidebar, call `sendPlanUpdate(env, entries)` — a
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
        AllowedInPlanMode:  false,
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

### 4. Register the tool in `NewRegistry()`

Open `internal/tools/registry.go` and add a call inside `NewRegistry()`:

```go
func NewRegistry() *Registry {
    r := &Registry{tools: make(map[string]*Tool)}
    r.register(readFileTool())
    r.register(writeFileTool())
    r.register(listDirTool())
    r.register(searchFilesTool())
    r.register(runCommandTool())
    r.register(applyDiffTool())
    r.register(switchToAgentModeTool())
    r.register(fetchURLTool())  // <-- add here
    return r
}
```

That's it. After rebuilding (`go build ./...`), the LLM will see the new tool.

---

## Tool Fields Reference

### `AllowedInPlanMode`

The agent has two operating modes: `agent` (full access) and `plan` (read-only planning).

| Value   | Behavior |
|---------|----------|
| `true`  | Tool is available in both `agent` and `plan` modes |
| `false` | Tool is only available in `agent` mode |

Safe read-only tools (e.g. `read_file`, `list_dir`, `search_files`) should set this to `true`.
Destructive or side-effectful tools should use `false`.

### `RequiresPermission`

When `true`, the ReAct loop pauses before calling the tool and sends a
`session/request_permission` notification to the ACP client. The user must approve the call
before execution proceeds.

Use `RequiresPermission: true` for:
- Tools that execute external processes or shell commands
- Tools that make network requests to external services
- Tools that delete or irreversibly modify data

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
        AllowedInPlanMode:  true,
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

Then in `registry.go`:

```go
r.register(gitLogTool())
```

---

## Checklist

Before submitting a new tool, verify:

- [ ] Tool name is unique and follows `snake_case` convention
- [ ] Description clearly explains what the tool does and when to use it
- [ ] All required fields are listed in `InputSchema.required`
- [ ] Optional fields have sensible defaults documented in their description
- [ ] `AllowedInPlanMode` is set correctly (read-only → `true`, side-effectful → `false`)
- [ ] `RequiresPermission` is set to `true` for destructive or external-network operations
- [ ] `Execute` returns `("", error)` for argument parsing failures
- [ ] `Execute` returns `(errorMessage, nil)` for runtime failures so the LLM can see them
- [ ] The tool constructor is registered in `NewRegistry()`
- [ ] `go build ./...` and `go test ./...` pass

---

## Built-in Plan / Todo Tools

Two tools are built into the agent to support task tracking. The TUI renders a dedicated sidebar
panel (right 1/5 of the screen) that shows the current plan and token usage. Both tools are
available in **both** `agent` and `plan` modes.

### `create_todo_list`

Creates or replaces the current todo list from a markdown checklist.

```
Arguments:
  items  (string, required)  Markdown checklist, one item per line.
                             Supported: "- [ ] task", "- [x] done", "* [ ] task"
```

Example agent call:

```json
{
  "items": "- [ ] Read existing code\n- [ ] Write tests\n- [ ] Implement feature\n- [ ] Update docs"
}
```

The tool:
1. Parses the checklist into plan entries (checked items get status `completed`)
2. Stores the plan in session state (persists across turns)
3. Sends a `PlanUpdate` via `acp.UpdateSender` so the TUI sidebar refreshes immediately
4. Returns a confirmation string to the LLM

### `update_todo_item`

Updates the status of a single plan entry by zero-based index.

```
Arguments:
  index   (integer, required)  Zero-based position in the list
  status  (string, required)   One of: pending, in_progress, completed, failed, cancelled
```

Example agent call:

```json
{ "index": 0, "status": "in_progress" }
```

### TUI Sidebar

When the terminal is at least 60 columns wide, the TUI splits into two vertical panels:

- Left (4/5): chat viewport, input field, header, status bar - as usual
- Right (1/5): token statistics + plan checklist

Token stats show per-turn and cumulative input/output tokens. The plan checklist uses visual
indicators:

| Symbol | Status |
|--------|--------|
| `[ ]` | pending |
| `[>]` | in_progress |
| `[x]` | completed |
| `[!]` | failed |
| `[-]` | cancelled |

Scroll the sidebar with `Ctrl+Up` / `Ctrl+Down`.

---

## Alternative: MCP Servers

If you want to add tools without modifying the agent source (e.g. for project-specific tools,
third-party integrations, or tools in other languages), use the
[MCP server integration](./mcp-integration.md).

MCP tools are registered at runtime with the prefix `serverName__toolName` and follow the same
`AllowedInPlanMode`/permission model as built-in tools.
