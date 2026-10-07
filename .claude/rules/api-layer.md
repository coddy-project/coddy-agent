---
description: Optional HTTP gateway, OpenAPI spec source of truth
paths:
  - "external/httpserver/**/*.go"
---

# HTTP API layer (`external/httpserver`)

- Built with **`-tags http`**. SPA root **`/`** (`go:embed` from **`external/ui`**) links only when you add **`tags=ui`** with **`go build -tags=http,ui`** (see **`mountEmbeddedSPARoot`** splits in **`spa_embed_ui.go`** / **`spa_no_ui.go`**). Handlers are registered in **`Server.New`** in **`server.go`**.
- **`openapi.go`** builds the served OpenAPI 3 document (`openAPISpec`). It is the **programmatic** spec; it must describe the same paths, methods, parameters, schemas, and response shapes as the live handlers.
- After any change to routes or externally visible request or response behavior, update **`openAPISpec`** and extend **`server_test.go`** (or add tests) under the **`http` build tag.
- User-facing narrative lives in **`docs/reference/http-api.md`**; keep it aligned when endpoints or semantics change.
- **Which workspace a request describes.** **`resolveSessionCWD`** (**`slash_commands.go`**) answers with the session in **`X-Coddy-Session-ID`** (a persisted one loaded on demand; **`400`** malformed, **`404`** unknown) or the server default cwd. The read-only listings the composer asks for while the user types - **`/coddy/slash-commands`**, **`GET /coddy/skills`**, **`/coddy/mentions`**, **`POST /coddy/mentions/check`**, **`/coddy/workspace/file`** - go through **`resolveListingCWD`** instead, which adds the **`cwd`** query: the folder a new chat picked before its session exists, an absolute existing directory or **`400`**, used when the header is absent or names a session the server does not have yet, ignored next to one it has. A new listing of that kind uses **`resolveListingCWD`** and adds **`listingCWDParam()`** in **`openapi.go`**. **`DELETE /coddy/skills/{name}`** resolves the same way, because a skill is deleted from the list **`GET /coddy/skills`** showed for that workspace: **`DeleteSkill`** removes only a path inside a configured skills directory of it (a link into the directory is removed as the link, never what it points at), which is the reach a client already has by anchoring a session on the folder, and it approves or runs nothing. Routes that change a workspace (the MCP routes through **`mcpWorkspace`**, trust and untrust, **`POST /coddy/workspace/folders`**) stay on **`resolveSessionCWD`** and never take **`cwd`**: an arbitrary folder must not get a declaration written or approved without a session in it. Held by **`TestListingCWDQueryEdges`** and **`features/skills_session_workspace.feature`**.

## References

@workflow.mdc
@docs/reference/http-api.md
@external/httpserver/server.go
@external/httpserver/openapi.go
