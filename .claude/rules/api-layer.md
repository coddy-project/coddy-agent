---
description: Optional HTTP gateway, OpenAPI spec source of truth
paths:
  - "external/httpserver/**/*.go"
---

# HTTP API layer (`external/httpserver`)

- Built with **`-tags http`**. SPA root **`/`** (`go:embed` from **`external/ui`**) links only when you add **`tags=ui`** with **`go build -tags=http,ui`** (see **`mountEmbeddedSPARoot`** splits in **`spa_embed_ui.go`** / **`spa_no_ui.go`**). Handlers are registered in **`Server.New`** in **`server.go`**.
- **`openapi.go`** builds the served OpenAPI 3 document (`openAPISpec`). It is the **programmatic** spec; it must describe the same paths, methods, parameters, schemas, and response shapes as the live handlers.
- After any change to routes or externally visible request or response behavior, update **`openAPISpec`** and extend **`server_test.go`** (or add tests) under the **`http` build tag.
- User-facing narrative lives in **`docs/http-api.md`**; keep it aligned when endpoints or semantics change.

## References

@workflow.mdc
@docs/http-api.md
@external/httpserver/server.go
@external/httpserver/openapi.go
