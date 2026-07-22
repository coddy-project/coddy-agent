//go:build http

package httpserver

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/EvilFreelancer/coddy-agent/internal/version"
	"gopkg.in/yaml.v3"
)

// openAPISpec builds the OpenAPI 3 document for the Coddy HTTP gateway.
// Keep this in sync with routes registered in New.
func openAPISpec() map[string]interface{} {
	ver := version.Get()
	doc := map[string]interface{}{
		"openapi": "3.0.3",
		"info": map[string]interface{}{
			"title": "Coddy HTTP API",
			"description": "OpenAI-compatible endpoints backed by Coddy sessions and agents. **`GET /v1/models`** returns one list: **agent** and **plan** first (**`owned_by`**: **`coddy`**), then every configured **`models[].model`** row (**`id`** is the YAML selector, **`owned_by`** is the provider prefix). " +
				"Classify POST **model** values: **agent** / **plan** run the ReAct agent; a selector with **provider/rest** form (see config) that appears in **`models`** triggers a single direct LLM completion (no tools). " +
				"**`metadata.model`** may appear only on agent/plan requests to set the session **`SelectedModelID`**; it is **not** allowed on direct completion. " +
				"**`metadata.reasoning`** (optional, agent/plan only) sets the reasoning level; it must be one of the effective model's **`reasoning_levels`** (or null/empty to clear). " +
				"JSON and SSE responses include **`metadata`** with the effective YAML model selector (**`metadata.model`**); streamed runs emit a final **`event: coddy_meta`** JSON payload with the same map before **`data: [DONE]`**. " +
				"Optional header **X-Coddy-Session-ID** continues an existing session; omit it to create one according to project docs.",
			"version": ver,
		},
		"servers": []interface{}{
			map[string]interface{}{
				"url":         "/",
				"description": "Server root (same host/port as coddy http). **`GET /`**, **`/index.html`**, **`/app.js`**, **`/styles.css`**, and favicon paths (**`/coddy-favicon.svg`**, **`/favicon-32.png`**, **`/favicon.ico`**, **`/apple-touch-icon.png`**) set **`Cache-Control: no-cache`**.",
			},
		},
		// Optional bearer auth: an empty requirement plus bearerAuth means requests may be
		// unauthenticated (default) or carry a token when httpserver.auth_token is configured.
		"security": []interface{}{
			map[string]interface{}{},
			map[string]interface{}{"bearerAuth": []interface{}{}},
		},
		"paths": map[string]interface{}{
			"/v1/models": map[string]interface{}{
				"get": map[string]interface{}{
					"summary": "List models (profiles and configured LLM backends)",
					"description": "Returns **agent**, then **plan** (**`owned_by`**: **`coddy`**), then each **`models[].model`** from configuration (**`owned_by`**: provider segment of **`id`**). " +
						"Optional **`default_agent_model`** echoes configured **`agent.model`** for clients that default **`metadata.model`** on profile requests. " +
						"Choose any returned **`id`** as the HTTP **`model`** on **`POST /v1/chat/completions`** or **`POST /v1/responses`**.",
					"operationId": "listModels",
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Model list",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"$ref": "#/components/schemas/ModelList",
									},
								},
							},
						},
					},
				},
			},
			"/v1/chat/completions": map[string]interface{}{
				"post": map[string]interface{}{
					"summary": "Create chat completion",
					"description": "Chat completion in OpenAI-compatible shape. **`model`** must match an **`id`** from **`GET /v1/models`**: **`agent`** / **`plan`** (ReAct) or a configured **`models[].model`** YAML selector (single direct completion). " +
						"Optional **`metadata`** on agent/plan only: **`metadata.model`** sets the backed LLM (**`models[].model`**); omit or omit the key to use session defaults. " +
						"**`metadata`** must not carry **`model`** for direct-completion **`model`** values. " +
						"When **stream** is true the response is **text/event-stream** (OpenAI-shaped chunks plus optional **`event: coddy_meta`** before **`[DONE]`**). Otherwise JSON. " +
						"The last entry in **messages** must have role **user**.",
					"operationId": "createChatCompletion",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "X-Coddy-Session-ID", "in": "header", "required": false,
							"schema":      map[string]string{"type": "string"},
							"description": "Existing session id. If absent, the server may create a new session.",
						},
					},
					"requestBody": map[string]interface{}{
						"required": true,
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{
									"$ref": "#/components/schemas/ChatCompletionRequest",
								},
							},
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Completion or streamed events. SSE may include **`event: coddy_meta`** (final metadata map) before **`data: [DONE]`**.",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"$ref": "#/components/schemas/ChatCompletionResponse",
									},
								},
								"text/event-stream": map[string]interface{}{
									"schema": map[string]interface{}{
										"type":        "string",
										"format":      "binary",
										"description": "Server-Sent Events stream (OpenAI-compatible chunk lines, optional coddy_meta).",
									},
								},
							},
						},
						"400": errorResponseRef(),
						"404": errorResponseRef(),
						"409": errorResponseRef(),
						"500": errorResponseRef(),
					},
				},
			},
			"/v1/responses": map[string]interface{}{
				"post": map[string]interface{}{
					"summary": "Create response",
					"description": "Responses-style call with **`model`**, **`input`** text, optional **`stream`** (SSE). **`model`** is any **`id`** from **`GET /v1/models`**. " +
						"**`metadata.model`** applies only when **`model`** is **`agent`** or **`plan`**. **`attachments`** (workspace-relative **`path`** rows) hydrate UTF-8 file bodies from session **cwd** on **`agent`** / **`plan`** only.",
					"operationId": "createResponse",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "X-Coddy-Session-ID", "in": "header", "required": false,
							"schema":      map[string]string{"type": "string"},
							"description": "Existing session id. If absent, the server creates a session for this turn.",
						},
					},
					"requestBody": map[string]interface{}{
						"required": true,
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{
									"$ref": "#/components/schemas/ResponsesCreateRequest",
								},
							},
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Completed JSON or streamed SSE (when **stream** is true). SSE default lines are OpenAI-style `data: { ... chat.completion.chunk ... }`. Named events: **tool_call**, **tool_call_update**, **plan**, **token_usage**, **`coddy_meta`** (effective **`metadata`** map last), then **`[DONE]`**.",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"$ref": "#/components/schemas/ResponsesCreateResponse",
									},
								},
								"text/event-stream": map[string]interface{}{
									"schema": map[string]interface{}{
										"type":        "string",
										"format":      "binary",
										"description": "SSE including optional `event:` lines",
									},
								},
							},
						},
						"400": errorResponseRef(),
						"404": errorResponseRef(),
						"409": errorResponseRef(),
						"500": errorResponseRef(),
					},
				},
			},
			"/v1/responses/{id}": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":     "Get response/session by id (MVP)",
					"description": "Returns metadata when **id** is an active session id in this process.",
					"operationId": "getResponse",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "id", "in": "path", "required": true,
							"schema":      map[string]string{"type": "string"},
							"description": "Session id (same as stored server-side for the conversation).",
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Response metadata",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"$ref": "#/components/schemas/ResponsesGetResponse",
									},
								},
							},
						},
						"404": errorResponseRef(),
					},
				},
			},
			"/coddy/sessions": map[string]interface{}{
				"get": map[string]interface{}{
					"summary": "List persisted chat sessions",
					"description": "Rows are ordered by **session.json** **updatedAt** (newest first), then **id** when timestamps tie. " +
						"**updatedAt** advances when session state is persisted (messages, titles, etc.); loading a snapshot into memory for HTTP does not rewrite it. " +
						"Bundles created for **scheduler runs** (cron or manual) carry **schedulerRun** metadata and are **hidden** from this list unless **include_scheduler=true**.",
					"parameters": append(coddyPagingParams(), map[string]interface{}{
						"name":        "include_scheduler",
						"in":          "query",
						"schema":      map[string]string{"type": "boolean"},
						"description": "When true, include scheduler-run session directories in the list.",
					}, map[string]interface{}{
						"name":        "include_activity",
						"in":          "query",
						"schema":      map[string]string{"type": "boolean"},
						"description": "When true, each session row includes **turnActive**, **activitySeq**, **readActivitySeq**, and **unreadComplete** for composer UI.",
					}),
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Paged session identifiers"},
						"503": errorResponseRef(),
					},
				},
			},
			"/coddy/describe": map[string]interface{}{
				"post": map[string]interface{}{
					"summary":     "Generate a short text description",
					"description": "Accepts arbitrary text and returns a short phrase describing what it is about. If the input is 3 words or fewer, the response echoes them.",
					"operationId": "coddyDescribe",
					"requestBody": map[string]interface{}{
						"required": true,
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{
									"type": "object",
									"properties": map[string]interface{}{
										"text": map[string]string{"type": "string"},
									},
									"required": []string{"text"},
								},
							},
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Description payload",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"type": "object",
										"properties": map[string]interface{}{
											"object": map[string]string{"type": "string", "example": "coddy.describe"},
											"short":  map[string]string{"type": "string"},
										},
										"required": []string{"object", "short"},
									},
								},
							},
						},
						"400": errorResponseRef(),
						"502": errorResponseRef(),
						"503": errorResponseRef(),
					},
				},
			},
			"/coddy/slash-commands": map[string]interface{}{
				"get": map[string]interface{}{
					"summary": "List slash commands from skills (paginated)",
					"description": "Returns skill-derived slash command **`name`** and **`description`** rows sorted by name. " +
						"**`page`** (1-based) and **`page_size`** (1 to 200) are required. Optional **`prefix`** filters by case-insensitive name prefix. " +
						"When **X-Coddy-Session-ID** is set (existing session), listing uses that session **cwd** when resolving **`${CWD}`** in configured skill directories; otherwise the server default session cwd applies.",
					"operationId": "listSlashCommands",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "X-Coddy-Session-ID", "in": "header", "required": false,
							"schema":      map[string]string{"type": "string"},
							"description": "Optional session whose cwd scopes skill path expansion.",
						},
						map[string]interface{}{
							"name": "page", "in": "query", "required": true,
							"schema":      map[string]interface{}{"type": "integer", "minimum": 1},
							"description": "Page index (1-based).",
						},
						map[string]interface{}{
							"name": "page_size", "in": "query", "required": true,
							"schema": map[string]interface{}{
								"type": "integer", "minimum": 1, "maximum": 200,
							},
							"description": "Rows per page.",
						},
						map[string]interface{}{
							"name": "prefix", "in": "query", "required": false,
							"schema":      map[string]string{"type": "string"},
							"description": "Case-insensitive filter on command name.",
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Paged slash command rows",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"$ref": "#/components/schemas/CoddySlashCommandsPage",
									},
								},
							},
						},
						"400": errorResponseRef(),
						"404": errorResponseRef(),
						"500": errorResponseRef(),
					},
				},
			},
			"/coddy/commands": map[string]interface{}{
				"get": map[string]interface{}{
					"summary": "List built-in slash commands",
					"description": "Returns the deterministic built-in commands (**`/compact`**, **`/plugin`**) that run without an LLM turn, so the composer can show a **Commands** group alongside skills. **`compact`** appears only while **`compaction.enabled`** is true. Optional **`prefix`** filters by case-insensitive name prefix. These are intentionally not part of **`/coddy/slash-commands`** (skills only).",
					"operationId": "listBuiltinCommands",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "prefix", "in": "query", "required": false,
							"schema":      map[string]string{"type": "string"},
							"description": "Case-insensitive filter on command name.",
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Built-in command rows",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"type": "object",
										"properties": map[string]interface{}{
											"object": map[string]string{"type": "string"},
											"items": map[string]interface{}{
												"type": "array",
												"items": map[string]interface{}{
													"type": "object",
													"properties": map[string]interface{}{
														"name":        map[string]string{"type": "string"},
														"description": map[string]string{"type": "string"},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			"/coddy/workspace/files": map[string]interface{}{
				"get": map[string]interface{}{
					"summary": "List workspace files under session cwd (paginated)",
					"description": "**`page`** (1-based) and **`page_size`** (1 to 200) are required. **Case-insensitive** **`prefix`** substring filter over **`path_rel`** (non-empty substring required; omit or blank **`prefix`** yields an empty **`items`** page without scanning). " +
						"Optional **`dirs=true`** adds **`kind`** **`dir`** rows with **`path_rel`** ending in **`/`** for navigation-only rows. Responses are sorted **`path_rel`** ascending. Paths never escape session **cwd**.",
					"operationId": "listWorkspaceFiles",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "X-Coddy-Session-ID", "in": "header", "required": false,
							"schema":      map[string]string{"type": "string"},
							"description": "Session whose **cwd** is the listing root.",
						},
						map[string]interface{}{
							"name": "page", "in": "query", "required": true,
							"schema":      map[string]interface{}{"type": "integer", "minimum": 1},
							"description": "Page index (1-based).",
						},
						map[string]interface{}{
							"name": "page_size", "in": "query", "required": true,
							"schema": map[string]interface{}{
								"type": "integer", "minimum": 1, "maximum": 200,
							},
							"description": "Rows per page.",
						},
						map[string]interface{}{
							"name": "prefix", "in": "query", "required": false,
							"schema":      map[string]string{"type": "string"},
							"description": "Case-insensitive substring filter applied to **`path_rel`**. When empty, **`items`** is empty.",
						},
						map[string]interface{}{
							"name": "dirs", "in": "query", "required": false,
							"schema": map[string]interface{}{
								"type": "string",
								"enum": []interface{}{"", "true", "false", "1", "0", "yes"},
							},
							"description": "Include directory rows (**`dirs=true`** / **`yes`**). File-only attachments still require non-folder paths.",
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Paged workspace file rows relative to cwd",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"$ref": "#/components/schemas/CoddyWorkspaceFilesPage",
									},
								},
							},
						},
						"400": errorResponseRef(),
						"404": errorResponseRef(),
						"500": errorResponseRef(),
					},
				},
			},
			"/coddy/workspace/context": map[string]interface{}{
				"get": map[string]interface{}{
					"summary": "Workspace context for the composer chips (folder, git branch, worktree)",
					"description": "Describes the workspace of the session in **`X-Coddy-Session-ID`** (or the server default cwd without the header). " +
						"With **`path`** the given folder is described instead (pre-session preview); a missing folder yields **400**. " +
						"Inside a git repository the payload adds **`repo_root`**, **`branch`**, **`branches`**, and **`worktrees`** (from `git worktree list`); **`is_worktree`** is true when the workspace is a linked (non-main) worktree.",
					"operationId": "coddyWorkspaceContextGet",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "X-Coddy-Session-ID", "in": "header", "required": false,
							"schema":      map[string]string{"type": "string"},
							"description": "Session whose **cwd** is described (ignored when **`path`** is set).",
						},
						map[string]interface{}{
							"name": "path", "in": "query", "required": false,
							"schema":      map[string]string{"type": "string"},
							"description": "Absolute folder to describe instead of the session cwd.",
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Workspace context",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"$ref": "#/components/schemas/CoddyWorkspaceContext",
									},
								},
							},
						},
						"400": errorResponseRef(),
						"404": errorResponseRef(),
						"500": errorResponseRef(),
					},
				},
			},
			"/coddy/workspace/folders": map[string]interface{}{
				"get": map[string]interface{}{
					"summary": "List subfolders for the workspace folder picker",
					"description": "Lists direct subfolders of **`path`** (default: session cwd via **`X-Coddy-Session-ID`**, else the server default cwd). " +
						"Hidden folders and **`node_modules`** are skipped; rows are sorted by name. A missing folder yields **400**.",
					"operationId": "coddyWorkspaceFoldersGet",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "X-Coddy-Session-ID", "in": "header", "required": false,
							"schema":      map[string]string{"type": "string"},
							"description": "Session whose **cwd** is the default listing root.",
						},
						map[string]interface{}{
							"name": "path", "in": "query", "required": false,
							"schema":      map[string]string{"type": "string"},
							"description": "Absolute folder to list.",
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Folder listing",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"type": "object",
										"properties": map[string]interface{}{
											"object": map[string]interface{}{"type": "string", "example": "coddy.workspace_folders"},
											"path":   map[string]interface{}{"type": "string"},
											"parent": map[string]interface{}{"type": "string"},
											"folders": map[string]interface{}{
												"type": "array",
												"items": map[string]interface{}{
													"type": "object",
													"properties": map[string]interface{}{
														"name": map[string]interface{}{"type": "string"},
														"path": map[string]interface{}{"type": "string"},
													},
												},
											},
										},
									},
								},
							},
							"400": errorResponseRef(),
							"404": errorResponseRef(),
							"500": errorResponseRef(),
						},
					},
				},
			},
			"/coddy/config/schema": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":     "JSON Schema for Coddy YAML configuration (UI)",
					"description": "Returns a JSON Schema document describing the JSON shape accepted by **PUT** `/coddy/config` and returned by **GET** `/coddy/config`. Includes **`providers[].name`** pattern, optional **`x-coddy-provider-api-key-env-placeholder`** on **`providers[].api_key`**, and other UI hints. Exposes **api_key**, optional per-provider **proxy**, and other secrets when combined with **GET** - use only on trusted networks.",
					"operationId": "coddyConfigSchemaGet",
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "JSON Schema (draft 2020-12)",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{"type": "object"},
								},
							},
						},
						"500": errorResponseRef(),
					},
				},
			},
			"/coddy/config": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":     "Get current configuration as JSON",
					"description": "Returns the active process configuration (including **api_key** and optional **proxy** fields on providers).",
					"operationId": "coddyConfigGet",
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Configuration JSON (ConfigJSON)",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{"$ref": "#/components/schemas/CoddyConfigJSON"},
								},
							},
						},
						"500": errorResponseRef(),
					},
				},
				"put": map[string]interface{}{
					"summary":     "Replace configuration from JSON",
					"description": "Validates the body, writes **config.yaml** atomically, reloads in-process config. On reload failure after write, restores **config.yaml.bak** to the primary path.",
					"operationId": "coddyConfigPut",
					"requestBody": map[string]interface{}{
						"required": true,
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{"$ref": "#/components/schemas/CoddyConfigJSON"},
							},
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "`{\"ok\":true}` on success",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{"$ref": "#/components/schemas/CoddyConfigValidateResponse"},
								},
							},
						},
						"400": errorResponseRef(),
						"500": errorResponseRef(),
					},
				},
			},
			"/coddy/config/validate": map[string]interface{}{
				"post": map[string]interface{}{
					"summary":     "Validate configuration JSON without writing",
					"description": "Runs the same validation as **PUT** `/coddy/config` without persisting.",
					"operationId": "coddyConfigValidatePost",
					"requestBody": map[string]interface{}{
						"required": true,
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{"$ref": "#/components/schemas/CoddyConfigJSON"},
							},
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "`{\"ok\":true}`",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{"$ref": "#/components/schemas/CoddyConfigValidateResponse"},
								},
							},
						},
						"400": map[string]interface{}{
							"description": "`{\"ok\":false,\"error\":\"...\"}`",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{"$ref": "#/components/schemas/CoddyConfigValidateResponse"},
								},
							},
						},
					},
				},
			},
			"/coddy/sessions/{id}/activity": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":     "Composer activity for a session",
					"description": "Returns **turnActive** (exclusive turn lock held), **activitySeq**, **readActivitySeq**, and **unreadComplete** for multi-surface UI.",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "id", "in": "path", "required": true,
							"schema":      map[string]string{"type": "string"},
							"description": "Session id.",
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Activity payload"},
						"404": errorResponseRef(),
						"500": errorResponseRef(),
					},
				},
			},
			"/coddy/sessions/{id}": map[string]interface{}{
				"patch": map[string]interface{}{
					"summary":     "Patch session composer metadata",
					"description": "Set **title** (pinned title), **selectedModelId** (YAML **`models[].model`** selector for this session), **selectedReasoning** (reasoning level; must be one of the effective model's **`reasoning_levels`**, empty to clear), and/or **markActivityRead** (boolean) to advance the read cursor for **activitySeq**. **markActivityRead** updates only activity counters in **session.json** and does not change **updatedAt** (history order stays stable until new chat content is saved).",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "id", "in": "path", "required": true,
							"schema":      map[string]string{"type": "string"},
							"description": "Session id.",
						},
					},
					"requestBody": map[string]interface{}{
						"required": true,
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{
									"type": "object",
									"properties": map[string]interface{}{
										"title":             map[string]string{"type": "string"},
										"selectedModelId":   map[string]string{"type": "string"},
										"selectedReasoning": map[string]string{"type": "string"},
										"markActivityRead":  map[string]string{"type": "boolean"},
									},
								},
							},
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Patched session"},
						"400": errorResponseRef(),
						"404": errorResponseRef(),
					},
				},
			},
			"/coddy/sessions/{id}/workspace": map[string]interface{}{
				"post": map[string]interface{}{
					"summary": "Switch the session workspace folder, git branch, or worktree",
					"description": "Body **`{\"path\": dir}`** switches the session cwd to an existing folder (skills, project rules, and slash commands are re-derived; the new cwd persists in **session.json**). " +
						"Body **`{\"branch\": b}`** checks the branch out in place; when the branch is already checked out in another worktree (including the main one) the session cwd jumps there instead. " +
						"Body **`{\"branch\": b, \"worktree\": true}`** ensures a dedicated worktree for the branch (created under **`<home>/worktrees/<repo>/`** on demand) and moves the session cwd into it. " +
						"The workspace is chosen **once per session**: as soon as the conversation has messages, switching yields **409** (`workspace is locked once the conversation starts`). " +
						"A missing folder or a branch switch outside a git repository yields **400**; git checkout/worktree failures yield **409**. The session is created on demand (draft flow). Responds with the fresh workspace context.",
					"operationId": "coddySessionWorkspacePost",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "id", "in": "path", "required": true,
							"schema":      map[string]string{"type": "string"},
							"description": "Session id.",
						},
					},
					"requestBody": map[string]interface{}{
						"required": true,
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{
									"type": "object",
									"properties": map[string]interface{}{
										"path":     map[string]string{"type": "string"},
										"branch":   map[string]string{"type": "string"},
										"worktree": map[string]string{"type": "boolean"},
									},
								},
							},
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Workspace context after the switch",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"$ref": "#/components/schemas/CoddyWorkspaceContext",
									},
								},
							},
						},
						"400": errorResponseRef(),
						"404": errorResponseRef(),
						"409": errorResponseRef(),
						"500": errorResponseRef(),
					},
				},
			},
			"/coddy/sessions/{id}/messages": map[string]interface{}{
				"get": map[string]interface{}{
					"summary": "Read conversation transcript",
					"description": "Top-level **model** is the effective YAML backend for this session (**`selectedModelId`** when set, else configured **`agent.model`**). **selectedModelId** echoes the stored session override (may be empty). Assistant rows in **messages** may include **`model`** (YAML selector used for that reply). " +
						"**user** and **assistant** rows may include **created_at** (RFC3339 UTC) when the server appended that message to history. " +
						"When long-term memory copilot has run for this session bundle, responses may include **memoryTurns** (persisted observability parallel to Chat Completions transcript; not forwarded to main LLM). " +
						"**uiLog** (optional) lists UI-only rows such as persisted LLM/request errors keyed by **userTurnIndex**; these are not part of **messages** and are not sent to the model. " +
						"Immediately after **POST /coddy/sessions/{id}/cancel**, the returned **messages** list can briefly omit or shorten the in-progress **assistant** row compared to what was already streamed; UIs that keep a local shadow should merge when the server snapshot is a strict prefix of on-screen rows.",
					"parameters": []interface{}{
						map[string]interface{}{"name": "id", "in": "path", "required": true, "schema": map[string]string{"type": "string"}},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "OpenAI-shaped messages payload"},
						"404": errorResponseRef(),
						"503": errorResponseRef(),
					},
				},
			},
			"/coddy/sessions/{id}/composer-stream": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":     "Subscribe to live composer SSE for an in-flight turn",
					"description": "Server-Sent Events with the same **data:** and **event:** frames as **POST /v1/responses** (**stream: true**) for the active **agent**/**plan** turn. Replays bytes generated so far, then forwards live chunks until the turn ends (relay closes). While no relay exists yet, emits **SSE comments** (`: composer stream pending`) until a composer POST attaches a relay or the wait window expires (**event: error**). Optional header **X-Coddy-Session-ID** must match **{id}** when set.",
					"parameters": []interface{}{
						map[string]interface{}{"name": "id", "in": "path", "required": true, "schema": map[string]string{"type": "string"}},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "text/event-stream composer relay"},
						"400": errorResponseRef(),
						"404": errorResponseRef(),
						"500": errorResponseRef(),
					},
				},
			},
			"/coddy/sessions/{id}/permission": map[string]interface{}{
				"post": map[string]interface{}{
					"summary":     "Resolve a pending tool permission prompt from a streaming ReAct turn",
					"description": "Completes **`event: permission`** on **`POST /v1/responses`** (**stream: true**). Body **`toolCallId`** must match **`toolCall.toolCallId`** from the SSE payload; **`optionId`** is **`allow`**, **`allow_always`**, or **`reject`** (or send **`outcome`** **`allow`** / **`cancelled`**). Optional header **X-Coddy-Session-ID** must match **{id}** when set.",
					"parameters": []interface{}{
						map[string]interface{}{
							"name":        "id",
							"in":          "path",
							"required":    true,
							"schema":      map[string]string{"type": "string"},
							"description": "Session id.",
						},
					},
					"requestBody": map[string]interface{}{
						"required": true,
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{
									"type": "object",
									"required": []interface{}{
										"toolCallId",
									},
									"properties": map[string]interface{}{
										"toolCallId": map[string]string{"type": "string"},
										"optionId":   map[string]string{"type": "string"},
										"outcome":    map[string]string{"type": "string"},
									},
								},
							},
						},
					},
					"responses": map[string]interface{}{
						"204": map[string]interface{}{"description": "Permission choice accepted"},
						"400": errorResponseRef(),
						"404": errorResponseRef(),
					},
				},
			},
			"/coddy/sessions/{id}/question": map[string]interface{}{
				"post": map[string]interface{}{
					"summary":     "Answer a pending interactive question from a streaming ReAct turn",
					"description": "Completes **`event: question`** on **`POST /v1/responses`** (**stream: true**). Body **`requestId`** must match the payload from SSE, and **`answers`** is an array of string arrays (one row per question, entries are selected labels or custom text). Optional header **X-Coddy-Session-ID** must match **{id}** when set.",
					"parameters": []interface{}{
						map[string]interface{}{
							"name":        "id",
							"in":          "path",
							"required":    true,
							"schema":      map[string]string{"type": "string"},
							"description": "Session id.",
						},
					},
					"requestBody": map[string]interface{}{
						"required": true,
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{
									"type": "object",
									"required": []interface{}{
										"requestId", "answers",
									},
									"properties": map[string]interface{}{
										"requestId": map[string]string{"type": "string"},
										"answers": map[string]interface{}{
											"type": "array",
											"items": map[string]interface{}{
												"type":  "array",
												"items": map[string]string{"type": "string"},
											},
										},
									},
								},
							},
						},
					},
					"responses": map[string]interface{}{
						"204": map[string]interface{}{"description": "Answer accepted"},
						"400": errorResponseRef(),
						"404": errorResponseRef(),
					},
				},
			},
			"/coddy/skills": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":     "List skills",
					"description": "Returns all skills discovered from **`skills.dirs`** with their enabled/disabled status. The disabled state is read from the managed skills directory (`~/.coddy/skills/.disabled`).",
					"operationId": "listSkills",
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Skill list",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"$ref": "#/components/schemas/SkillList",
									},
								},
							},
						},
						"500": errorResponseRef(),
					},
				},
			},
			"/coddy/skills/{name}/enable": map[string]interface{}{
				"post": map[string]interface{}{
					"summary":     "Enable a skill",
					"description": "Removes **{name}** from the disabled list so the skill is loaded on the next session turn.",
					"operationId": "enableSkill",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "name", "in": "path", "required": true,
							"schema":      map[string]string{"type": "string"},
							"description": "Canonical skill name (single segment, no slashes).",
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Skill enabled."},
						"400": errorResponseRef(),
					},
				},
			},
			"/coddy/providers/{name}/models": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":     "List a provider's available models",
					"description": "Fetches the model list advertised by the named provider's server (openai: **`GET {api_base}/models`**; anthropic: **`GET {api_base}/v1/models`**; neuraldeep: **`GET https://api.neuraldeep.ru/v1/models`**). The provider is resolved from the saved config, so its credentials (`api_key` / `api_key_command` / `NAME_API_KEY`) and `proxy` apply server-side without exposing secrets. Returns **`{ok:true, models:[{id,name}]}`** on success, or **`{ok:false, error, models:[]}`** with HTTP 200 when the upstream call fails so the UI can fall back to manual model entry. Unknown provider name returns 404.",
					"operationId": "listProviderModels",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "name", "in": "path", "required": true,
							"schema":      map[string]string{"type": "string"},
							"description": "Provider name from `providers[].name`.",
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Model list result (ok:true with models, or ok:false with error)."},
						"404": errorResponseRef(),
						"500": errorResponseRef(),
					},
				},
			},
			"/coddy/skills/{name}/disable": map[string]interface{}{
				"post": map[string]interface{}{
					"summary":     "Disable a skill",
					"description": "Adds **{name}** to the disabled list so the skill is skipped during loading. The skill files are not removed.",
					"operationId": "disableSkill",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "name", "in": "path", "required": true,
							"schema":      map[string]string{"type": "string"},
							"description": "Canonical skill name (single segment, no slashes).",
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Skill disabled."},
						"400": errorResponseRef(),
					},
				},
			},
			"/coddy/skills/sync": map[string]interface{}{
				"post": map[string]interface{}{
					"summary":     "Sync remote skill sources",
					"description": "Fetches every source in **`skills.sources`** (GitHub repos, git URLs, or an http(s) URL to an agents-standard **`marketplace.json`**) and materializes their skills into the managed skills directory. Manual only — never runs automatically. Returns lists of added/updated skill names and per-source failures.",
					"operationId": "syncSkills",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "source", "in": "query", "required": false,
							"schema":      map[string]string{"type": "string"},
							"description": "Sync only this marketplace source; omit to sync all configured sources.",
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Sync result.",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{"$ref": "#/components/schemas/SkillSyncResult"},
								},
							},
						},
						"500": errorResponseRef(),
					},
				},
			},
			"/coddy/skills/sources": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":     "List remote skill sources",
					"description": "Returns the configured **`skills.sources`** entries (GitHub repos, git URLs, or marketplace.json URLs).",
					"operationId": "listSkillSources",
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Configured sources.",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"type": "object",
										"properties": map[string]interface{}{
											"object": map[string]string{"type": "string", "example": "coddy.skills_sources"},
											"items":  map[string]interface{}{"type": "array", "items": map[string]string{"type": "string"}},
										},
									},
								},
							},
						},
					},
				},
				"post": map[string]interface{}{
					"summary":     "Add a remote skill source",
					"description": "Appends a source to **`skills.sources`** in **config.yaml** and reloads config. Set **`sync:true`** to also fetch it immediately. The source is a GitHub repo (`owner/repo[@ref]`), a git URL, or an http(s) URL to an agents-standard **`marketplace.json`**.",
					"operationId": "addSkillSource",
					"requestBody": map[string]interface{}{
						"required": true,
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{
									"type": "object",
									"properties": map[string]interface{}{
										"source": map[string]string{"type": "string", "description": "owner/repo[@ref], a git URL, or a marketplace.json URL."},
										"sync":   map[string]interface{}{"type": "boolean", "description": "Fetch the source immediately after adding."},
									},
									"required": []interface{}{"source"},
								},
							},
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Source added (with optional sync result)."},
						"400": errorResponseRef(),
						"500": errorResponseRef(),
					},
				},
				"delete": map[string]interface{}{
					"summary":     "Remove a remote skill source",
					"description": "Removes a source from **`skills.sources`** in **config.yaml** (matched case-insensitively) and reloads config. Already-installed skills remain until removed. The source is passed as the **`source`** query parameter. Missing **`source`** returns 400.",
					"operationId": "removeSkillSource",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "source", "in": "query", "required": true,
							"schema":      map[string]string{"type": "string"},
							"description": "The exact configured source string to remove.",
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Source removed (or absent, with removed:false)."},
						"400": errorResponseRef(),
					},
				},
			},
			"/coddy/skills/available": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":     "List installable marketplace plugins",
					"description": "Fetches every configured marketplace manifest (network / git) and returns the plugins they advertise, each flagged with `installed`. Backs the browse/filter install control.",
					"operationId": "listAvailablePlugins",
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Available plugins (name, description, version, source, installed)."},
						"500": errorResponseRef(),
					},
				},
			},
			"/coddy/skills/install": map[string]interface{}{
				"post": map[string]interface{}{
					"summary":     "Install one plugin from a marketplace",
					"description": "Installs a single named plugin from a marketplace source (rather than syncing every plugin the source advertises).",
					"operationId": "installPlugin",
					"requestBody": map[string]interface{}{
						"required": true,
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{
									"type": "object",
									"properties": map[string]interface{}{
										"source": map[string]string{"type": "string", "description": "Configured marketplace source the plugin comes from."},
										"plugin": map[string]string{"type": "string", "description": "Plugin name to install."},
									},
									"required": []interface{}{"source", "plugin"},
								},
							},
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Install result (added/updated/failed)."},
						"400": errorResponseRef(),
					},
				},
			},
			"/coddy/skills/updates": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":     "Check installed remote skills for updates",
					"description": "For every installed remote skill, fetches its marketplace source and compares the installed version against the latest declared upstream. Performs network / git access. Returns one entry per remote skill with **`update_available`** set when a newer version exists.",
					"operationId": "checkSkillUpdates",
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Per-skill update status.",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{"$ref": "#/components/schemas/SkillUpdateList"},
								},
							},
						},
						"500": errorResponseRef(),
					},
				},
			},
			"/coddy/skills/{name}/update": map[string]interface{}{
				"post": map[string]interface{}{
					"summary":     "Update a skill to its latest version",
					"description": "Re-syncs the marketplace source that provides **{name}**, installing whatever version that source currently declares. Fails with 400 when the skill was not installed from a remote source.",
					"operationId": "updateSkill",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "name", "in": "path", "required": true,
							"schema":      map[string]string{"type": "string"},
							"description": "Canonical skill name (single segment, no slashes).",
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Update result.",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{"$ref": "#/components/schemas/SkillSyncResult"},
								},
							},
						},
						"400": errorResponseRef(),
					},
				},
			},
			"/coddy/skills/{name}": map[string]interface{}{
				"delete": map[string]interface{}{
					"summary":     "Remove a remote skill",
					"description": "Deletes any on-disk skill by name (its directory, and its remote provenance entry when synced). Bundled (read-only) skills cannot be deleted and return 400; so do skills outside the configured skill directories.",
					"operationId": "removeRemoteSkill",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "name", "in": "path", "required": true,
							"schema":      map[string]string{"type": "string"},
							"description": "Canonical skill name (single segment, no slashes).",
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Remote skill removed."},
						"400": errorResponseRef(),
					},
				},
			},
			"/coddy/sessions/{id}/cancel": map[string]interface{}{
				"post": map[string]interface{}{
					"summary":     "Cancel active generation for a session",
					"description": "Best-effort cancellation of the current ReAct or direct completion turn. Writes a cross-process cancel signal for persisted bundles so another **coddy** process holding the turn can observe cooperative cancel. When assistant tokens were already streamed, the server persists that partial **assistant** message for the interrupted turn before the turn ends. Optional header **X-Coddy-Session-ID** must match **{id}** when set.",
					"parameters": []interface{}{
						map[string]interface{}{
							"name":        "id",
							"in":          "path",
							"required":    true,
							"schema":      map[string]string{"type": "string"},
							"description": "Session id.",
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Cancellation applied (idempotent when nothing is running)."},
						"400": errorResponseRef(),
						"404": errorResponseRef(),
					},
				},
			},
			"/coddy/sessions/{id}/compact": map[string]interface{}{
				"post": map[string]interface{}{
					"summary":     "Compact (summarize) older session history",
					"description": "Summarizes conversation history into a single summary row inserted into the transcript. As a manual trigger it forces compaction, folding whatever exists even below the keep-recent boundary (**compaction.keep_recent_turns**, default 2 user turns) by reducing the kept tail as needed; nothing_to_compact is returned only when there is no prior conversation. Later LLM prompts replay only the summary plus the kept tail; the persisted transcript keeps every original message. Equivalent to the built-in **/compact** prompt command. Requires the composer turn lock (409 when another agent turn is running).",
					"parameters": []interface{}{
						map[string]interface{}{
							"name":        "id",
							"in":          "path",
							"required":    true,
							"schema":      map[string]string{"type": "string"},
							"description": "Session id.",
						},
					},
					"requestBody": map[string]interface{}{
						"required": false,
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{
									"type": "object",
									"properties": map[string]interface{}{
										"instructions": map[string]string{
											"type":        "string",
											"description": "Optional extra guidance for the summarizer (what to emphasize).",
										},
									},
								},
							},
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Compaction outcome.",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{"$ref": "#/components/schemas/CompactResult"},
								},
							},
						},
						"400": errorResponseRef(),
						"404": errorResponseRef(),
						"409": errorResponseRef(),
					},
				},
			},
		},
		"components": map[string]interface{}{
			"securitySchemes": map[string]interface{}{
				"bearerAuth": map[string]interface{}{
					"type":        "http",
					"scheme":      "bearer",
					"description": "Optional. When httpserver.auth_token (or --auth-token / CODDY_HTTP_TOKEN) is set, every /v1/* and /coddy/* route requires `Authorization: Bearer <token>` and returns 401 otherwise. Disabled by default. /docs and /openapi.* are also protected unless httpserver.public_docs is true.",
				},
			},
			"schemas": map[string]interface{}{
				"ErrorEnvelope": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"error": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"message": map[string]string{"type": "string"},
							},
						},
					},
				},
				"CompactResult": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"compacted": map[string]interface{}{"type": "boolean", "description": "False when there was nothing to compact."},
						"reason":    map[string]string{"type": "string", "description": "Set to nothing_to_compact when compacted is false."},
						"summary":   map[string]string{"type": "string", "description": "Generated summary text (without the transcript preamble)."},
						"compacted_messages": map[string]interface{}{
							"type": "integer", "description": "How many history messages were folded into the summary.",
						},
						"kept_messages": map[string]interface{}{
							"type": "integer", "description": "How many messages after the summary stayed verbatim.",
						},
						"model": map[string]string{"type": "string", "description": "models[].model that produced the summary."},
					},
				},
				"SkillRow": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"name":        map[string]string{"type": "string", "description": "Canonical skill name."},
						"description": map[string]string{"type": "string"},
						"file_path":   map[string]string{"type": "string"},
						"enabled":     map[string]interface{}{"type": "boolean", "description": "False when the skill is in the disabled list."},
						"version":     map[string]string{"type": "string", "description": "Installed version: the marketplace-declared version for synced skills, else the SKILL.md frontmatter version. Absent when unknown."},
						"source":      map[string]string{"type": "string", "description": "Configured source string when the skill was installed via `skills.sources`; absent for local/bundled skills."},
						"readonly":    map[string]interface{}{"type": "boolean", "description": "True for bundled skills, which cannot be deleted."},
					},
				},
				"SkillSyncResult": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"ok":      map[string]interface{}{"type": "boolean"},
						"added":   map[string]interface{}{"type": "array", "items": map[string]string{"type": "string"}},
						"updated": map[string]interface{}{"type": "array", "items": map[string]string{"type": "string"}},
						"failed": map[string]interface{}{
							"type": "array",
							"items": map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"source": map[string]string{"type": "string"},
									"error":  map[string]string{"type": "string"},
								},
							},
						},
					},
				},
				"SkillList": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"object": map[string]string{"type": "string", "example": "coddy.skills_list"},
						"items": map[string]interface{}{
							"type":  "array",
							"items": map[string]interface{}{"$ref": "#/components/schemas/SkillRow"},
						},
					},
				},
				"SkillUpdateList": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"object": map[string]string{"type": "string", "example": "coddy.skills_updates"},
						"items": map[string]interface{}{
							"type": "array",
							"items": map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"name":             map[string]string{"type": "string", "description": "Installed remote skill name."},
									"source":           map[string]string{"type": "string", "description": "Configured source it was installed from."},
									"version":          map[string]string{"type": "string", "description": "Installed version."},
									"latest":           map[string]string{"type": "string", "description": "Latest version declared by the source."},
									"update_available": map[string]interface{}{"type": "boolean", "description": "True when latest is newer than the installed version."},
								},
							},
						},
					},
				},
				"CoddyConfigJSON": map[string]interface{}{
					"type":        "object",
					"description": "Coddy configuration as JSON (same logical fields as **config.yaml**). See **GET** `/coddy/config/schema` for the machine-readable JSON Schema.",
				},
				"CoddyConfigValidateResponse": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"ok":    map[string]string{"type": "boolean"},
						"error": map[string]string{"type": "string"},
					},
				},
				"ModelList": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"object": map[string]string{"type": "string", "example": "list"},
						"default_agent_model": map[string]interface{}{
							"type":        "string",
							"description": "Configured **`agent.model`** (**`models[].model`** selector). Omitted when empty. The embedded UI uses it as the default LLM choice for ReAct turns.",
						},
						"data": map[string]interface{}{
							"type": "array",
							"items": map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"id":                 map[string]string{"type": "string"},
									"object":             map[string]string{"type": "string", "example": "model"},
									"created":            map[string]string{"type": "integer", "format": "int64"},
									"owned_by":           map[string]string{"type": "string", "example": "coddy"},
									"max_context_tokens": map[string]string{"type": "integer"},
									"multimodal":         map[string]string{"type": "boolean"},
									"reasoning_levels": map[string]interface{}{
										"type":        "array",
										"items":       map[string]string{"type": "string"},
										"description": "Reasoning levels offered for this model (e.g. minimal, low, medium, high). Omitted for non-reasoning models.",
									},
									"reasoning_default": map[string]string{
										"type":        "string",
										"description": "Reasoning level pre-selected for new chats with this model. Omitted when none is configured.",
									},
								},
							},
						},
					},
				},
				"OpenAIMessage": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"role": map[string]interface{}{
							"type": "string",
							"enum": []interface{}{"system", "user", "assistant", "tool"},
						},
						"content": map[string]interface{}{
							"description": "JSON string or raw text/object per OpenAI client conventions.",
							"oneOf": []interface{}{
								map[string]string{"type": "string"},
								map[string]interface{}{"type": "array"},
								map[string]interface{}{"type": "object"},
							},
						},
						"reasoning": map[string]interface{}{
							"type":        "string",
							"description": "Coddy transcript extension persisted model reasoning alongside assistant replies.",
						},
						"reasoning_duration_ms": map[string]interface{}{
							"type":        "integer",
							"format":      "int64",
							"description": "Wall-clock thinking span (ms). Coddy persists this for UI restores.",
						},
						"model": map[string]interface{}{
							"type":        "string",
							"description": "YAML `models[].model` selector persisted on assistant replies (Coddy extension).",
						},
						"tool_call_id": map[string]string{"type": "string"},
						"name":         map[string]string{"type": "string"},
						"compaction_summary": map[string]interface{}{
							"type":        "boolean",
							"description": "Coddy transcript extension: this row is a generated summary of earlier history (context compaction). Rows before the last summary are excluded from LLM prompts but stay in the transcript.",
						},
					},
					"required": []string{"role"},
				},
				"ChatCompletionRequest": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"model": map[string]interface{}{
							"type":        "string",
							"description": "Any `id` from `GET /v1/models` (agent, plan, or `models[].model`).",
						},
						"messages": map[string]interface{}{
							"type":  "array",
							"items": map[string]interface{}{"$ref": "#/components/schemas/OpenAIMessage"},
						},
						"stream":      map[string]string{"type": "boolean"},
						"max_tokens":  map[string]string{"type": "integer"},
						"temperature": map[string]interface{}{"type": "number", "format": "float"},
						"metadata": map[string]interface{}{
							"type":                 "object",
							"description":          "Optional. For agent/plan only, `model` key selects `models[].model`. Not allowed for direct completion `model` values.",
							"additionalProperties": true,
						},
					},
					"required": []string{"model", "messages"},
				},
				"ChatCompletionResponse": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":      map[string]string{"type": "string"},
						"object":  map[string]string{"type": "string", "example": "chat.completion"},
						"created": map[string]string{"type": "integer", "format": "int64"},
						"model":   map[string]string{"type": "string"},
						"metadata": map[string]interface{}{
							"type":                 "object",
							"description":          "Effective YAML model selector under `model`, optional `api_model`.",
							"additionalProperties": map[string]string{"type": "string"},
						},
						"choices": map[string]interface{}{
							"type": "array",
							"items": map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"index": map[string]string{"type": "integer"},
									"message": map[string]interface{}{
										"type": "object",
										"properties": map[string]interface{}{
											"role":    map[string]string{"type": "string"},
											"content": map[string]string{"type": "string"},
										},
									},
									"finish_reason": map[string]string{"type": "string"},
								},
							},
						},
					},
				},
				"ResponsesCreateRequest": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"model": map[string]interface{}{
							"type":        "string",
							"description": "Any `id` from `GET /v1/models`.",
						},
						"input": map[string]string{"type": "string"},
						"stream": map[string]interface{}{
							"type":        "boolean",
							"description": "Emit **text/event-stream** when true.",
						},
						"metadata": map[string]interface{}{
							"type":                 "object",
							"description":          "Optional. For agent/plan only, `model` key selects `models[].model`.",
							"additionalProperties": true,
						},
						"attachments": map[string]interface{}{
							"type":        "array",
							"description": "Allowed only when **model** is **`agent`** or **`plan`**. Hydrated UTF-8 file bodies from session **cwd** **path** fields.",
							"items":       map[string]interface{}{"$ref": "#/components/schemas/ResponsesPromptAttachment"},
						},
						"inline_files": map[string]interface{}{
							"type":        "array",
							"description": "Supported for all modes. For **`agent`** / **`plan`**: each file is saved to `~/.coddy/sessions/<id>/assets/` with read-only permissions (0o444) and the model receives a `<coddy_session_assets>` annotation with the on-disk paths. For direct YAML model: each entry becomes an image content part sent inline to the provider.",
							"items":       map[string]interface{}{"$ref": "#/components/schemas/ResponsesInlineFile"},
						},
					},
					"required": []string{"model", "input"},
				},
				"ResponsesPromptAttachment": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]string{
							"type":        "string",
							"description": "Relative path within session **cwd** (no traversal). Folder paths (**trailing slash**) are rejected.",
						},
						"source": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"literal": map[string]string{"type": "string"},
								"start":   map[string]string{"type": "integer"},
								"end":     map[string]string{"type": "integer"},
							},
						},
					},
					"required": []string{"path"},
				},
				"ResponsesInlineFile": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"name": map[string]string{
							"type":        "string",
							"description": "Original file name (e.g. `photo.png`). Informational only.",
						},
						"data_url": map[string]string{
							"type":        "string",
							"description": "Data URI: `data:<mime>;base64,<bytes>` or an HTTPS image URL.",
						},
					},
					"required": []string{"data_url"},
				},
				"ResponsesCreateResponse": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":     map[string]string{"type": "string"},
						"object": map[string]string{"type": "string", "example": "response"},
						"status": map[string]string{"type": "string", "example": "completed"},
						"model":  map[string]string{"type": "string"},
						"metadata": map[string]interface{}{
							"type":                 "object",
							"additionalProperties": map[string]string{"type": "string"},
						},
						"output": map[string]interface{}{
							"type": "array",
							"items": map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"type": map[string]string{"type": "string", "example": "text"},
									"text": map[string]string{"type": "string"},
								},
							},
						},
					},
				},
				"ResponsesGetResponse": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":     map[string]string{"type": "string"},
						"object": map[string]string{"type": "string", "example": "response"},
						"status": map[string]string{"type": "string", "example": "completed"},
					},
				},
				"CoddySlashCommandRow": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"name":        map[string]string{"type": "string", "description": "Slash command id (text after `/`)."},
						"description": map[string]string{"type": "string", "description": "Short summary for pickers."},
					},
					"required": []string{"name", "description"},
				},
				"CoddySlashCommandsPage": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"object": map[string]string{"type": "string", "example": "coddy.slash_commands_page"},
						"items": map[string]interface{}{
							"type":  "array",
							"items": map[string]interface{}{"$ref": "#/components/schemas/CoddySlashCommandRow"},
						},
						"total":     map[string]string{"type": "integer", "description": "Row count after prefix filter."},
						"has_more":  map[string]string{"type": "boolean"},
						"page":      map[string]string{"type": "integer"},
						"page_size": map[string]string{"type": "integer"},
					},
					"required": []string{"object", "items", "total", "has_more", "page", "page_size"},
				},
				"CoddyWorkspaceFileRow": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"name": map[string]string{"type": "string"},
						"path_rel": map[string]string{
							"type":        "string",
							"description": "POSIX-style relative segment from cwd. Directory rows end with **/** when **dirs=true**.",
						},
						"kind": map[string]interface{}{
							"type": "string",
							"enum": []interface{}{"file", "dir"},
						},
					},
					"required": []string{"name", "path_rel", "kind"},
				},
				"CoddyWorkspaceFilesPage": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"object": map[string]string{"type": "string", "example": "coddy.workspace_files_page"},
						"items": map[string]interface{}{
							"type":  "array",
							"items": map[string]interface{}{"$ref": "#/components/schemas/CoddyWorkspaceFileRow"},
						},
						"total":     map[string]string{"type": "integer"},
						"has_more":  map[string]string{"type": "boolean"},
						"page":      map[string]string{"type": "integer"},
						"page_size": map[string]string{"type": "integer"},
					},
					"required": []string{"object", "items", "total", "has_more", "page", "page_size"},
				},
				"CoddyWorkspaceContext": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"object":      map[string]string{"type": "string", "example": "coddy.workspace_context"},
						"path":        map[string]string{"type": "string"},
						"name":        map[string]string{"type": "string"},
						"is_git_repo": map[string]string{"type": "boolean"},
						"is_worktree": map[string]string{"type": "boolean"},
						"repo_root":   map[string]string{"type": "string"},
						"branch":      map[string]string{"type": "string"},
						"branches": map[string]interface{}{
							"type":  "array",
							"items": map[string]string{"type": "string"},
						},
						"worktrees": map[string]interface{}{
							"type": "array",
							"items": map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"path":   map[string]string{"type": "string"},
									"branch": map[string]string{"type": "string"},
									"main":   map[string]string{"type": "boolean"},
								},
							},
						},
						"id": map[string]interface{}{
							"type":        "string",
							"description": "Session id (present on POST /coddy/sessions/{id}/workspace responses).",
						},
					},
					"required": []string{"object", "path", "name", "is_git_repo", "is_worktree"},
				},
			},
		},
	}
	mergeOpenAPISchedulerDoc(&doc)
	mergeOpenAPIMemoryDoc(&doc)
	return doc
}

func errorResponseRef() map[string]interface{} {
	return map[string]interface{}{
		"description": "Error",
		"content": map[string]interface{}{
			"application/json": map[string]interface{}{
				"schema": map[string]interface{}{
					"$ref": "#/components/schemas/ErrorEnvelope",
				},
			},
		},
	}
}

func coddyPagingParams() []interface{} {
	return []interface{}{
		map[string]interface{}{
			"name": "limit", "in": "query", "schema": map[string]string{"type": "string"},
			"description": "Maximum rows (default 50, capped at 100).",
		},
		map[string]interface{}{
			"name": "cursor", "in": "query", "schema": map[string]string{"type": "string"},
			"description": "Numeric offset for the next results page.",
		},
		map[string]interface{}{
			"name":        "q",
			"in":          "query",
			"schema":      map[string]string{"type": "string"},
			"description": `Optional substring filter over session title OR the first persisted user message content only (case-insensitive). Other messages are not searched.`,
		},
	}
}

func encodeOpenAPIYAML() ([]byte, error) {
	doc := openAPISpec()
	data, err := yaml.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func (s *Server) handleOpenAPIYAML(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	data, err := encodeOpenAPIYAML()
	if err != nil {
		s.log.Error("openapi yaml", "error", err)
		http.Error(w, "failed to build OpenAPI document", http.StatusInternalServerError)
		return
	}
	// Inline + text-ish type so browsers show the document instead of forcing download (application/yaml often saves a file).
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", `inline; filename="openapi.yaml"`)
	_, _ = w.Write(data)
}

func (s *Server) handleOpenAPIJSON(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(openAPISpec()); err != nil {
		s.log.Error("openapi json", "error", err)
		http.Error(w, "failed to build OpenAPI document", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `inline; filename="openapi.json"`)
	_, _ = w.Write(buf.Bytes())
}
