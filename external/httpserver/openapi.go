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
			"description": "OpenAI-compatible endpoints backed by Coddy sessions and agents. **`GET /v1/models`** returns one list: **agent**, **plan**, and **ask** first (**`owned_by`**: **`coddy`**), then every configured **`models[].model`** row (**`id`** is the YAML selector, **`owned_by`** is the provider prefix). " +
				"Classify POST **model** values: **agent** / **plan** / **ask** run the ReAct agent; a selector with **provider/rest** form (see config) that appears in **`models`** triggers a single direct LLM completion (no tools). " +
				"**`metadata.model`** may appear only on agent/plan/ask requests to set the session **`SelectedModelID`**; it is **not** allowed on direct completion. " +
				"**`metadata.reasoning`** (optional, agent/plan/ask only) sets the reasoning level; it must be one of the effective model's **`reasoning_levels`** (or null/empty to clear). Levels map to provider controls (**`reasoning_effort`**; **`qwen3*`** models on OpenAI-compatible providers also pin **`chat_template_kwargs.enable_thinking`** on). " +
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
					"description": "Returns **agent**, **plan**, then **ask** (**`owned_by`**: **`coddy`**), then each **`models[].model`** from configuration (**`owned_by`**: provider segment of **`id`**). " +
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
					"description": "Chat completion in OpenAI-compatible shape. **`model`** must match an **`id`** from **`GET /v1/models`**: **`agent`** / **`plan`** / **`ask`** (ReAct) or a configured **`models[].model`** YAML selector (single direct completion). " +
						"Optional **`metadata`** on agent/plan/ask only: **`metadata.model`** sets the backed LLM (**`models[].model`**); omit or omit the key to use session defaults. " +
						"**`metadata`** must not carry **`model`** for direct-completion **`model`** values. " +
						"When **stream** is true the response is **text/event-stream** (OpenAI-shaped chunks plus optional **`event: coddy_meta`** before **`[DONE]`**). Otherwise JSON. " +
						"**409** when **X-Coddy-Session-ID** names a child session spawned by **spawn_agent** (**sub_** ids): those transcripts are read-only for every model kind, and the error names the parent session to prompt instead. " +
						"A streamed response that has produced no frame for 15s sends an SSE comment keepalive, so an idle-timeout proxy does not drop a turn whose model is answering slowly. " +
						"This **`stream`** field selects the response shape for the client; **`models[].stream`** in **config.yaml** separately selects the transport coddy uses to reach the LLM. " +
						"Every **agent**/**plan**/**ask** turn is published to the session's composer relay whatever **`stream`** is set to, so other clients can watch it live over **GET /coddy/sessions/{id}/composer-stream**; with **`stream: false`** this response body is unchanged. A session already running a turn answers **409** for both shapes. " +
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
						"**409** when **X-Coddy-Session-ID** names a child session spawned by **spawn_agent** (**sub_** ids): those transcripts are read-only for every model kind, and the error names the parent session to prompt instead. " +
						"**`metadata.model`** applies only when **`model`** is **`agent`**, **`plan`**, or **`ask`**. **`attachments`** (workspace-relative **`path`** rows) hydrate text file bodies from session **cwd** on **`agent`** / **`plan`** / **`ask`** only; a file stored in another detected encoding (Windows-1251 and other legacy charsets) is converted to UTF-8. Every **agent**/**plan**/**ask** turn is published to the session's composer relay whatever **`stream`** is set to, so other clients can watch it live over **GET /coddy/sessions/{id}/composer-stream**; with **`stream: false`** this response body is unchanged. A session already running a turn answers **409** for both shapes. A turn started with **`stream: false`** is cancelled when its HTTP request is dropped; a streamed one keeps running. A streamed response that has produced no frame for 15s sends an SSE comment keepalive, so an idle-timeout proxy does not drop a turn whose model is answering slowly. This **`stream`** field selects the response shape for the client; **`models[].stream`** in **config.yaml** separately selects the transport coddy uses to reach the LLM.",
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
							"description": "Completed JSON or streamed SSE (when **stream** is true). SSE default lines are OpenAI-style `data: { ... chat.completion.chunk ... }`. Named events: **tool_call**, **tool_call_update**, **plan**, **token_usage** (completed model-call counters), **usage_update** (`used` / `size` for the current context window), **`coddy_meta`** (effective **`metadata`** map last; for agent/plan/ask turns it also carries **`stop_reason`** - `end_turn`, `cancelled`, `max_turns`, ... - so remote clients recover the ACP stop reason), then **`[DONE]`**.",
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
						"Bundles created for **scheduler runs** (cron or manual) carry **schedulerRun** metadata and are **hidden** from this list unless **include_scheduler=true**. " +
						"Child sessions of subagent runs (**subagentRun** metadata, **sub_** ids) are hidden unless **include_subagents=true**; an included child row carries **subagent** **`{parentSessionId, name, taskId}`** so a client can route back to the parent chat and to the task in its drawer.",
					"parameters": append(coddyPagingParams(), map[string]interface{}{
						"name":        "include_scheduler",
						"in":          "query",
						"schema":      map[string]string{"type": "boolean"},
						"description": "When true, include scheduler-run session directories in the list.",
					}, map[string]interface{}{
						"name":        "include_subagents",
						"in":          "query",
						"schema":      map[string]string{"type": "boolean"},
						"description": "When true, include child sessions spawned by **spawn_agent**; each such row carries **subagent** **`{parentSessionId, name, taskId}`** read from its bundle. The default listing hides them and opens no child bundle.",
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
			"/coddy/enhance-prompt": map[string]interface{}{
				"post": map[string]interface{}{
					"summary": "Improve a draft prompt",
					"description": "Rewrites a user's draft prompt into a clearer, more specific, and more effective prompt. The draft is treated only as source text to improve, never as a request to answer. " +
						"The rewrite uses the model selected by the session in **X-Coddy-Session-ID**. Without a usable session, it falls back to **`agent.model`**, then the first configured **`models[]`** row. " +
						"Unknown or invalid session ids fall back without creating a session. Returns **503** when no model is configured.",
					"operationId": "coddyEnhancePrompt",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "X-Coddy-Session-ID", "in": "header", "required": false,
							"schema":      map[string]string{"type": "string"},
							"description": "Existing session id, used to select the rewrite model.",
						},
					},
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
							"description": "Enhanced prompt payload",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"type": "object",
										"properties": map[string]interface{}{
											"object": map[string]string{"type": "string", "example": "coddy.enhance_prompt"},
											"text":   map[string]string{"type": "string"},
										},
										"required": []string{"object", "text"},
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
					"summary":     "List built-in slash commands",
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
						"Hidden folders and **`node_modules`** are skipped; rows are sorted by name. A missing folder yields **400**. " +
						"**`path=:drives:`** lists the machine's drive roots instead (Windows only; **400** elsewhere), and the **`parent`** " +
						"of a drive root is **`:drives:`** so the picker can walk up out of a volume.",
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
							"description": "Absolute folder to list, or **`:drives:`** for the drive level.",
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
											"drives": map[string]interface{}{
												"type":        "boolean",
												"description": "Present and **`true`** only on the **`:drives:`** level, whose rows are drive roots rather than folders.",
											},
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
						},
						"400": errorResponseRef(),
						"404": errorResponseRef(),
						"500": errorResponseRef(),
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
			"/coddy/config/reasoning-levels": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":     "Reasoning levels a model id offers (UI)",
					"description": "Resolves the reasoning levels a logical model id would offer with **no** **`models[].reasoning_levels`** override configured, so the settings form can fill that field instead of relying on the operator knowing each family's tiers. Detection is model-id based (**`gpt-5*`** -> **`minimal,low,medium,high`**; OpenAI **`o`**-series, **`gpt-oss*`**, **`qwen3*`**, and Claude extended-thinking models -> **`low,medium,high`**). The provider type decides the Codex remap (**`minimal`** becomes **`none`**): **`provider_type`**, when sent, is the type currently chosen in the settings form and wins over the saved config, so an unsaved or just-retyped provider row is honoured; without it the **`model`** provider prefix is looked up in the active config. A model id with no reasoning support is **not** an error: it answers **`{\"ok\":true,\"levels\":[],\"detected\":false}`**. A malformed id (the form is mid-edit) answers **200** with **`ok:false`** and an **`error`** for inline display; only a missing **`model`** parameter is **400**, reported as the flat **`{\"ok\":false,\"error\":...}`** the other **`/coddy/config`** routes use.",
					"operationId": "coddyConfigReasoningLevelsGet",
					"parameters": []interface{}{
						map[string]interface{}{
							"name":        "model",
							"in":          "query",
							"required":    true,
							"description": "Logical model id in the form **`provider_name/api_model_id`**. It need not be saved in **`models[]`** yet.",
							"schema":      map[string]interface{}{"type": "string"},
							"example":     "valera/qwen3.8-27b",
						},
						map[string]interface{}{
							"name":        "provider_type",
							"in":          "query",
							"required":    false,
							"description": "Wire type of the provider currently chosen for this model in the settings form (**`openai`**, **`anthropic`**, **`neuraldeep`**, **`codex`**). Overrides the saved provider's type for the Codex remap so an unsaved or just-retyped provider row resolves correctly; omit to use the saved config.",
							"schema":      map[string]interface{}{"type": "string"},
							"example":     "codex",
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Resolved levels, or `ok:false` with `error` for a malformed model id",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{"$ref": "#/components/schemas/CoddyReasoningLevelsResponse"},
								},
							},
						},
						"400": coddyConfigErrorResponse("Missing `model` query parameter"),
						"500": coddyConfigErrorResponse("Configuration unavailable"),
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
					"description": "Validates the body, writes **config.yaml** atomically, and reloads in-process config. Changed **mcp_servers** are reconnected for active sessions, re-running the workspace trust gate so unapproved project declarations stay cold; a session with a turn in flight is reconnected when that turn ends, not mid-turn, while ACP client-provided session servers stay connected. On reload failure after write, restores **config.yaml.bak** to the primary path.",
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
					"description": "Returns **turnActive** (a turn running in this server process, or the exclusive turn lock held by another one), **activitySeq**, **readActivitySeq**, and **unreadComplete** for multi-surface UI.",
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
			"/coddy/sessions/{id}/background-tasks": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":     "Background tasks of a session",
					"description": "Lists the tasks of the session: commands the agent started with **run_command** **`background: true`** (**kind** **command**) and subagent runs started with **spawn_agent** (**kind** **agent**, with **agent** **`{name, session_id}`** naming the definition and the child session whose transcript **GET /coddy/sessions/{session_id}/messages** serves). Each row carries **id**, **kind**, **label**, **command**, **status** (**queued**, **running**, **succeeded**, **failed**, **timed_out**, **stopped**, **orphaned**), **started_at**, **finished_at**, **exit_code**, **expected_seconds** (the model's own estimate), **timeout_seconds** (the hard limit), **notify_on_finish** (the task wakes the agent when it ends), plus the server-computed **elapsed_seconds**, **overdue**, and **running**. The task pool lives in the running **coddy** process; tasks recorded by an earlier process are merged in from the session bundle with status **orphaned**. Poll this endpoint for the status ticker: background tasks outlive the SSE stream of the turn that started them.",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "id", "in": "path", "required": true,
							"schema":      map[string]string{"type": "string"},
							"description": "Session id.",
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Task list with a **running** count"},
						"404": errorResponseRef(),
					},
				},
				"delete": map[string]interface{}{
					"summary":     "Clear the finished background tasks of a session",
					"description": "Drops every terminal task of the session, in memory and from the session bundle, and answers with **cleared**. Running tasks are left alone. History accumulates on its own and is deleted with the session, so this is the operator's explicit way to throw it away early.",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "id", "in": "path", "required": true,
							"schema":      map[string]string{"type": "string"},
							"description": "Session id.",
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Number of cleared tasks"},
						"404": errorResponseRef(),
					},
				},
			},
			"/coddy/sessions/{id}/background-tasks/{task_id}": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":     "One background task with its captured output",
					"description": "Returns the task row plus **output**, the combined stdout and stderr captured so far. Works while the task is still running. A task the pool no longer holds is answered from the session bundle log.",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "id", "in": "path", "required": true,
							"schema":      map[string]string{"type": "string"},
							"description": "Session id.",
						},
						map[string]interface{}{
							"name": "task_id", "in": "path", "required": true,
							"schema":      map[string]string{"type": "string"},
							"description": "Background task id (for example **bg_1**).",
						},
						map[string]interface{}{
							"name": "tail", "in": "query", "required": false,
							"schema":      map[string]string{"type": "integer"},
							"description": "Return only the last N lines of output. Omit for everything retained. A non-integer or negative value is a **400**. A log read back from the session bundle is capped at its last 256 KiB and flags the truncation.",
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Task with output"},
						"400": errorResponseRef(),
						"404": errorResponseRef(),
					},
				},
			},
			"/coddy/sessions/{id}/background-tasks/{task_id}/stop": map[string]interface{}{
				"post": map[string]interface{}{
					"summary":     "Stop a running background task",
					"description": "Terminates the task and the whole process group it started, then returns the final row and its output. Stopping a task that already finished changes nothing and still returns **200**. An unknown id is a **404**; a task that exists but could not be terminated is a **500**, never a 404.",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "id", "in": "path", "required": true,
							"schema":      map[string]string{"type": "string"},
							"description": "Session id.",
						},
						map[string]interface{}{
							"name": "task_id", "in": "path", "required": true,
							"schema":      map[string]string{"type": "string"},
							"description": "Background task id (for example **bg_1**).",
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Stopped task with output"},
						"404": errorResponseRef(),
						"500": errorResponseRef(),
					},
				},
			},
			"/coddy/subagents": map[string]interface{}{
				"get": map[string]interface{}{
					"summary": "Subagent definitions visible from a workspace",
					"description": "Lists the subagent definitions a session with this **cwd** would see: the embedded built-ins (**general**, **explore**), user-scope files under **`${CODDY_HOME}/agents`**, and project-scope files under the workspace's **`.claude/agents`** and **`.coddy/agents`** (**`subagents.dirs`**), later directories overriding earlier ones by name. " +
						"Each item carries **name**, **description**, **scope** (**builtin**, **user**, **project**), **path**, **digest** (SHA-256 of the file), **model**, **mode**, **builtin**, **hidden**, and the trust decision for this workspace: **trust** (**trusted** or **needs_approval**), mirrored as the booleans **trusted** and **needs_approval**. " +
						"Under **`subagents.project_trust: ask`** a project-scope file needs a receipt for its current content; under **allow** it is trusted; under **deny** project directories are not read at all. **workspace** is the canonical path the receipts are keyed by and **policy** the effective project trust policy.",
					"operationId": "listSubagents",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "cwd", "in": "query", "required": false,
							"schema":      map[string]string{"type": "string"},
							"description": "Absolute workspace path. Defaults to the server's default cwd. A relative path is a **400**.",
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Catalog for the workspace",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"type": "object",
										"properties": map[string]interface{}{
											"object":    map[string]string{"type": "string", "example": "coddy.subagent_list"},
											"workspace": map[string]string{"type": "string", "description": "Canonical workspace path the receipts are keyed by."},
											"policy":    map[string]interface{}{"type": "string", "enum": []string{"ask", "allow", "deny"}},
											"items": map[string]interface{}{
												"type":  "array",
												"items": map[string]interface{}{"$ref": "#/components/schemas/SubagentCatalogEntry"},
											},
										},
										"required": []string{"object", "workspace", "policy", "items"},
									},
								},
							},
						},
						"400": errorResponseRef(),
					},
				},
			},
			"/coddy/subagents/{name}/trust": map[string]interface{}{
				"post": map[string]interface{}{
					"summary": "Approve a project subagent definition for a workspace",
					"description": "Records a receipt in **`<home>/subagents-trust.json`** binding the workspace, the definition name and the digest of its current file content, so **spawn_agent** may run it under **`subagents.project_trust: ask`**. Rewriting the file changes the digest and withdraws the approval. " +
						"Optional body **`{\"cwd\": ...}`** selects the workspace (default: the server's default cwd). **404** when no definition of that name is visible from the workspace; **400** for a built-in or user-scope definition (nothing to approve), a malformed body, or a relative **cwd**. Answers with the refreshed catalog entry.",
					"operationId": "trustSubagent",
					"parameters":  []interface{}{subagentNameParam()},
					"requestBody": subagentTrustRequestBody(),
					"responses": map[string]interface{}{
						"200": subagentEntryResponse("Approval recorded; the entry now reports **trusted**."),
						"400": errorResponseRef(),
						"404": errorResponseRef(),
						"500": errorResponseRef(),
					},
				},
			},
			"/coddy/subagents/{name}/untrust": map[string]interface{}{
				"post": map[string]interface{}{
					"summary":     "Withdraw a project subagent approval",
					"description": "Removes the receipt of the named definition for the workspace (optional body **`{\"cwd\": ...}`**, default: the server's default cwd). Withdrawing an approval that was never on file, or naming a built-in or user-scope definition, changes nothing and still answers with the current entry. **404** when no definition of that name is visible from the workspace; **400** for a malformed body or a relative **cwd**.",
					"operationId": "untrustSubagent",
					"parameters":  []interface{}{subagentNameParam()},
					"requestBody": subagentTrustRequestBody(),
					"responses": map[string]interface{}{
						"200": subagentEntryResponse("Approval withdrawn (or none was on file); the entry reports its current trust state."),
						"400": errorResponseRef(),
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
				"delete": map[string]interface{}{
					"summary": "Delete a persisted session",
					"description": "Removes the whole session directory (messages, **`tool_calls/`**, **`stats.json`**, assets, background task logs) and the in-memory MCP clients, after stopping anything the session left running. " +
						"The delete covers the session tree: every child session spawned by **spawn_agent** (found by **parentSessionId**, nested descendants included) goes with it. The task representing each child run is stopped and awaited first, root to leaf, then every remaining task of every node, and only then are the bundles removed deepest first, so nothing writes into a removed directory. Deleting a child session directly removes that child and its own descendants and stops its task in the parent's tasks drawer. " +
						"A session that forked from another is also retracted from the **branches.json** of its source, and a branch point left with a single thread is dropped, so the branch navigator never points at a bundle that is gone. " +
						"Deleting an id with no bundle on disk still answers **200**.",
					"operationId": "coddySessionDelete",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "id", "in": "path", "required": true,
							"schema":      map[string]string{"type": "string"},
							"description": "Session id.",
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Session deleted",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"type": "object",
										"properties": map[string]interface{}{
											"object": map[string]string{"type": "string"},
											"id":     map[string]string{"type": "string"},
										},
									},
								},
							},
						},
						"400": errorResponseRef(),
						"409": map[string]interface{}{
							"description": "Nothing was removed: either a cancelled turn of the session tree was still running after the settle timeout, or descendants kept appearing while the tree was being marked. Retry once the tree is quiet.",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{"$ref": "#/components/schemas/ErrorEnvelope"},
								},
							},
						},
						"500": errorResponseRef(),
						"503": errorResponseRef(),
					},
				},
			},
			"/coddy/sessions/{id}/branches": map[string]interface{}{
				"post": map[string]interface{}{
					"summary": "Fork the session at one user message",
					"description": "Creates a sibling conversation that receives every message **before** the user message at **userMessageIndex** (0-based over **`user`** rows), so an edited version of that message can be resent without overwriting the original branch. " +
						"Workspace turn diffs recorded **after** the branch point are reversed in the session cwd first, so the files match the state the branch starts from; **fileRollbackNote** reports which turns were reversed or why none were. " +
						"Both branches are recorded in **branches.json** inside the source session bundle and are listed by **GET** on this path.",
					"operationId": "coddyBranchCreate",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "id", "in": "path", "required": true,
							"schema":      map[string]string{"type": "string"},
							"description": "Source session id.",
						},
					},
					"requestBody": map[string]interface{}{
						"required": true,
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{
									"type":     "object",
									"required": []interface{}{"userMessageIndex"},
									"properties": map[string]interface{}{
										"userMessageIndex": map[string]interface{}{
											"type": "integer", "minimum": 0,
											"description": "0-based index of the user message to branch at.",
										},
									},
								},
							},
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Branch created",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"type": "object",
										"properties": map[string]interface{}{
											"object":           map[string]string{"type": "string"},
											"newSessionId":     map[string]string{"type": "string"},
											"branchIndex":      map[string]string{"type": "integer"},
											"totalBranches":    map[string]string{"type": "integer"},
											"fileRollbackNote": map[string]string{"type": "string"},
										},
									},
								},
							},
						},
						"400": errorResponseRef(),
						"404": errorResponseRef(),
						"409": map[string]interface{}{
							"description": "The source is a subagent child session (sub_…): its transcript is read-only and cannot be forked; branch the parent instead.",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{"$ref": "#/components/schemas/ErrorEnvelope"},
								},
							},
						},
						"500": errorResponseRef(),
					},
				},
				"get": map[string]interface{}{
					"summary": "List branch points visible from a session",
					"description": "Reads **branches.json** from the session bundle. Each entry carries **userMessageIndex**, **currentIndex**, **total**, the sibling **sessions** (**sessionId**, **branchIndex**, **preview**, **lastUpdatedAt**), and **own** - **`true`** for a branch point this session introduced, **`false`** for the sibling view inherited from its parent. " +
						"Sessions whose bundle no longer exists are skipped and **currentIndex** is derived from the surviving list, so a stale branch file heals itself on read; a branch point with fewer than two surviving threads is not reported. " +
						"The bundled UI renders each entry as a **`‹ n/m ›`** navigator under that user message.",
					"operationId": "coddyBranchList",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "id", "in": "path", "required": true,
							"schema":      map[string]string{"type": "string"},
							"description": "Session id.",
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Branch points",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"type": "object",
										"properties": map[string]interface{}{
											"object":    map[string]string{"type": "string"},
											"sessionId": map[string]string{"type": "string"},
											"branchPoints": map[string]interface{}{
												"type": "array",
												"items": map[string]interface{}{
													"type": "object",
													"properties": map[string]interface{}{
														"userMessageIndex": map[string]string{"type": "integer"},
														"currentIndex":     map[string]string{"type": "integer"},
														"total":            map[string]string{"type": "integer"},
														"own":              map[string]string{"type": "boolean"},
														"sessions": map[string]interface{}{
															"type": "array",
															"items": map[string]interface{}{
																"type": "object",
																"properties": map[string]interface{}{
																	"sessionId":     map[string]string{"type": "string"},
																	"branchIndex":   map[string]string{"type": "integer"},
																	"preview":       map[string]string{"type": "string"},
																	"lastUpdatedAt": map[string]string{"type": "integer"},
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
						"400": errorResponseRef(),
						"404": errorResponseRef(),
						"500": errorResponseRef(),
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
						"409": map[string]interface{}{
							"description": "The workspace is locked (the conversation already has messages), or the session is a subagent child (sub_…) whose transcript is read-only.",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{"$ref": "#/components/schemas/ErrorEnvelope"},
								},
							},
						},
						"500": errorResponseRef(),
					},
				},
			},
			"/coddy/sessions/{id}/messages": map[string]interface{}{
				"get": map[string]interface{}{
					"summary": "Read conversation transcript",
					"description": "Top-level **model** is the effective YAML backend for this session (**`selectedModelId`** when set, else configured **`agent.model`**). **selectedModelId** echoes the stored session override (may be empty). **mode** reports the session profile (`agent`, `plan`, or `ask`) so remote clients restore it on load. Assistant rows in **messages** may include **`model`** (YAML selector used for that reply). User rows with uploaded files include **`files`** metadata; persisted images carry a session-scoped **`preview_url`**. " +
						"**user** and **assistant** rows may include **created_at** (RFC3339 UTC) when the server appended that message to history. " +
						"When long-term memory copilot has run for this session bundle, responses may include **memoryTurns** (persisted observability parallel to Chat Completions transcript; not forwarded to main LLM). " +
						"**uiLog** (optional) lists UI-only rows such as persisted LLM/request errors keyed by **userTurnIndex**; these are not part of **messages** and are not sent to the model. " +
						"Immediately after **POST /coddy/sessions/{id}/cancel**, the returned **messages** list can briefly omit or shorten the in-progress **assistant** row compared to what was already streamed; UIs that keep a local shadow should merge when the server snapshot is a strict prefix of on-screen rows. " +
						"For a child session spawned by **spawn_agent** the payload also carries **readOnly** **true** and **subagent** **`{parentSessionId, name, taskId}`**: the transcript is served from the live child while it runs and from its bundle afterwards, and no route accepts a prompt for it (**409**), so a UI replaces the composer with a notice linking to the parent chat.",
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
			"/coddy/sessions/{id}/assets/{name}/thumbnail": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":     "Read a persisted session image thumbnail",
					"description": "Returns the bounded PNG preview created for an uploaded image. The asset name comes from a user message **`files[].preview_url`**; arbitrary original asset bytes are not exposed by this route.",
					"parameters": []interface{}{
						map[string]interface{}{"name": "id", "in": "path", "required": true, "schema": map[string]string{"type": "string"}},
						map[string]interface{}{"name": "name", "in": "path", "required": true, "schema": map[string]string{"type": "string"}},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "PNG thumbnail",
							"content": map[string]interface{}{
								"image/png": map[string]interface{}{"schema": map[string]string{"type": "string", "format": "binary"}},
							},
						},
						"400": errorResponseRef(),
						"404": errorResponseRef(),
						"503": errorResponseRef(),
					},
				},
			},
			"/coddy/events": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":     "Subscribe to server-wide session events",
					"description": "Server-Sent Events for activity that is not tied to one session, so a client can be told a turn started in a session it is not driving instead of polling **GET /coddy/sessions**. Emits **event: turn_started** and **event: turn_ended** (**`{object, sessionId, phase, at}`**) for every turn in this server process, whichever surface started it. On connect it replays one **turn_started** per turn already running, then **event: ready** to mark the snapshot complete; an idle stream sends **SSE comments** as keepalives. Like the composer stream, this route also accepts the bearer token as **`?access_token=`**.",
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "text/event-stream of session turn events"},
						"500": errorResponseRef(),
					},
				},
			},
			"/coddy/sessions/{id}/composer-stream": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":     "Subscribe to live composer SSE for an in-flight turn",
					"description": "Server-Sent Events with the same **data:** and **event:** frames as **POST /v1/responses** (**stream: true**) for the active **agent**/**plan**/**ask** turn. Replays bytes generated so far, then forwards live chunks until the turn ends (relay closes). With **no turn running** for the session, answers immediately with **event: error** carrying **error.code** **no_active_stream**, so a client can fall back to the persisted transcript without waiting. While a turn *is* running but has not attached its relay yet, emits **SSE comments** (`: composer stream pending`) until it does or the wait window expires. Optional header **X-Coddy-Session-ID** must match **{id}** when set. Frames replayed to a subscriber carry an **`id:`** sequence; send it back as **Last-Event-ID** (or **`?last_event_id=`**) to resume after it instead of replaying the whole turn. When the frames a client asks to resume from have already been trimmed, the stream leads with **event: desync** so it can reload the transcript instead of rendering a gap. The primary **POST** stream is unchanged and carries no ids.",
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
					"description": "Completes **`event: permission`** on **`POST /v1/responses`** (**stream: true**). A child session spawned by **spawn_agent** never holds a prompt of its own (its requests are relayed to the parent chat) and answers **409**. Body **`toolCallId`** must match **`toolCall.toolCallId`** from the SSE payload; **`optionId`** is **`allow`**, **`allow_always`** (remembers this exact command), **`allow_always_program`** (offered for **run_command** only, and only when the command is a single plain invocation; remembers the program, or the program plus its subcommand for multiplexers like **git**), or **`reject`** (or send **`outcome`** **`allow`** / **`cancelled`**). Optional header **X-Coddy-Session-ID** must match **{id}** when set. Frames replayed to a subscriber carry an **`id:`** sequence; send it back as **Last-Event-ID** (or **`?last_event_id=`**) to resume after it instead of replaying the whole turn. When the frames a client asks to resume from have already been trimmed, the stream leads with **event: desync** so it can reload the transcript instead of rendering a gap. The primary **POST** stream is unchanged and carries no ids.",
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
						"409": errorResponseRef(),
					},
				},
			},
			"/coddy/sessions/{id}/question": map[string]interface{}{
				"post": map[string]interface{}{
					"summary":     "Answer a pending interactive question from a streaming ReAct turn",
					"description": "Completes **`event: question`** on **`POST /v1/responses`** (**stream: true**). Body **`requestId`** must match the payload from SSE, and **`answers`** is an array of string arrays (one row per question, entries are selected labels or custom text). Optional header **X-Coddy-Session-ID** must match **{id}** when set. Frames replayed to a subscriber carry an **`id:`** sequence; send it back as **Last-Event-ID** (or **`?last_event_id=`**) to resume after it instead of replaying the whole turn. When the frames a client asks to resume from have already been trimmed, the stream leads with **event: desync** so it can reload the transcript instead of rendering a gap. The primary **POST** stream is unchanged and carries no ids.",
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
					"description": "Fetches the model list advertised by the named provider's server (openai: **`GET {api_base}/models`**; anthropic: **`GET {api_base}/v1/models`**; neuraldeep: **`GET {selected api_base}/models`**, where api_base is one of the official deployments (**`https://api.neuraldeep.ru/v1`** by default, **`https://api.neuraldeep.tech/v1`** for the international mirror); codex: the fixed official Codex backend with the saved ChatGPT OAuth token). The provider is resolved from the saved config, so its credentials and `proxy` apply server-side without exposing secrets. Returns **`{ok:true, models:[{id,name}]}`** on success, or **`{ok:false, error, models:[]}`** with HTTP 200 when the upstream call fails so the UI can fall back to manual model entry. Unknown provider name returns 404.",
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
			"/coddy/providers/{name}/codex-auth": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":     "Get Codex OAuth status",
					"description": "Reports whether the named Codex provider has a server-side ChatGPT OAuth credential. It never returns token values. A valid unsaved provider name is accepted so Settings can show status before config is saved.",
					"operationId": "getProviderCodexAuth",
					"parameters":  []interface{}{codexProviderNameParameter()},
					"responses": map[string]interface{}{
						"200": jsonSchemaResponse("Non-secret Codex OAuth connection status.", "#/components/schemas/CodexAuthStatus"),
						"400": errorResponseRef(),
						"409": errorResponseRef(),
						"500": errorResponseRef(),
					},
				},
				"delete": map[string]interface{}{
					"summary":     "Remove Coddy-managed Codex OAuth credentials",
					"description": "Deletes only the credential stored under `CODDY_HOME/providers/{name}/codex-auth.json`. A separate Codex CLI login may remain available as a compatibility fallback.",
					"operationId": "deleteProviderCodexAuth",
					"parameters":  []interface{}{codexProviderNameParameter()},
					"responses": map[string]interface{}{
						"200": jsonSchemaResponse("Connection status after removal.", "#/components/schemas/CodexAuthStatus"),
						"400": errorResponseRef(),
						"409": errorResponseRef(),
						"500": errorResponseRef(),
					},
				},
			},
			"/coddy/providers/{name}/codex-auth/device": map[string]interface{}{
				"post": map[string]interface{}{
					"summary":     "Start Codex ChatGPT device authorization",
					"description": "Starts the official ChatGPT device flow. Open `verification_url`, enter `user_code`, then poll the returned `login_id`. The server performs the token exchange and stores credentials with restrictive file permissions.",
					"operationId": "startProviderCodexDeviceAuth",
					"parameters":  []interface{}{codexProviderNameParameter()},
					"responses": map[string]interface{}{
						"200": jsonSchemaResponse("Device authorization instructions.", "#/components/schemas/CodexAuthDeviceStart"),
						"400": errorResponseRef(),
						"409": errorResponseRef(),
						"502": errorResponseRef(),
					},
				},
			},
			"/coddy/providers/{name}/codex-auth/device/{loginID}": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":     "Poll Codex device authorization",
					"description": "Returns `pending`, `completed`, or `failed`. Token values are never returned.",
					"operationId": "getProviderCodexDeviceAuth",
					"parameters": []interface{}{
						codexProviderNameParameter(),
						map[string]interface{}{
							"name": "loginID", "in": "path", "required": true,
							"schema": map[string]string{"type": "string"},
						},
					},
					"responses": map[string]interface{}{
						"200": jsonSchemaResponse("Current device authorization state.", "#/components/schemas/CodexAuthDeviceStatus"),
						"400": errorResponseRef(),
						"404": errorResponseRef(),
						"409": errorResponseRef(),
					},
				},
			},
			"/coddy/providers/{name}/neuraldeep-auth": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":     "Get NeuralDeep sign-in status",
					"description": "Reports whether the named neuraldeep provider has a server-side hub login, masked, plus the credential source requests actually use (`oauth`, `api_key`, `api_key_command`, `env`, or `none`). `hub` names the hub that issued the stored login and `endpoint_hub` the hub a sign-in for the endpoint in **`api_base`** (default: the saved row's) would use; Settings warns when they differ, because a key minted by one deployment is not honored by the other. Key values are never returned. A valid unsaved provider name is accepted so Settings can show status before config is saved.",
					"operationId": "getProviderNeuralDeepAuth",
					"parameters": []interface{}{
						codexProviderNameParameter(),
						map[string]interface{}{
							"name": "api_base", "in": "query", "required": false,
							"schema":      map[string]string{"type": "string"},
							"description": "Endpoint currently picked in the settings form (`https://api.neuraldeep.ru/v1` or `https://api.neuraldeep.tech/v1`), possibly unsaved. Omitted or unrecognized: the saved row's endpoint, falling back to the default deployment like requests do.",
						},
					},
					"responses": map[string]interface{}{
						"200": jsonSchemaResponse("Non-secret NeuralDeep sign-in status.", "#/components/schemas/NeuralDeepAuthStatus"),
						"400": errorResponseRef(),
						"409": errorResponseRef(),
						"500": errorResponseRef(),
					},
				},
				"delete": map[string]interface{}{
					"summary":     "Sign out of NeuralDeep",
					"description": "Best-effort revokes the key on the hub, then deletes the credential stored under `CODDY_HOME/providers/{name}/neuraldeep-auth.json`.",
					"operationId": "deleteProviderNeuralDeepAuth",
					"parameters":  []interface{}{codexProviderNameParameter()},
					"responses": map[string]interface{}{
						"200": jsonSchemaResponse("Connection status after sign-out.", "#/components/schemas/NeuralDeepAuthStatus"),
						"400": errorResponseRef(),
						"409": errorResponseRef(),
						"500": errorResponseRef(),
					},
				},
			},
			"/coddy/providers/{name}/neuraldeep-auth/device": map[string]interface{}{
				"post": map[string]interface{}{
					"summary":     "Start NeuralDeep device authorization",
					"description": "Starts the hub's RFC 8628 device flow for client `coddy`. The hub is the one paired with the deployment: **`api_base`** in the optional JSON body (the endpoint picked in Settings, possibly unsaved) or, when the body is absent, the saved row's `api_base`; a body value that is not one of the official endpoints is refused with 400 before the hub is contacted. A new start supersedes the provider's previous pending attempt, including one still waiting for the hub (that one answers 409); a sign-out cancels a pending start the same way. Open `verification_url` (it carries the pre-filled code), confirm on the hub portal, then poll the returned `login_id`. The server polls the hub and stores the key with restrictive file permissions.",
					"operationId": "startProviderNeuralDeepDeviceAuth",
					"parameters":  []interface{}{codexProviderNameParameter()},
					"requestBody": map[string]interface{}{
						"required": false,
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{"$ref": "#/components/schemas/NeuralDeepAuthDeviceStartRequest"},
							},
						},
					},
					"responses": map[string]interface{}{
						"200": jsonSchemaResponse("Device authorization instructions.", "#/components/schemas/NeuralDeepAuthDeviceStart"),
						"400": errorResponseRef(),
						"409": errorResponseRef(),
						"502": errorResponseRef(),
					},
				},
			},
			"/coddy/providers/{name}/neuraldeep-auth/device/{loginID}": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":     "Poll NeuralDeep device authorization",
					"description": "Returns `pending`, `completed`, or `failed`. Key values are never returned.",
					"operationId": "getProviderNeuralDeepDeviceAuth",
					"parameters": []interface{}{
						codexProviderNameParameter(),
						map[string]interface{}{
							"name": "loginID", "in": "path", "required": true,
							"schema": map[string]string{"type": "string"},
						},
					},
					"responses": map[string]interface{}{
						"200": jsonSchemaResponse("Current device authorization state.", "#/components/schemas/CodexAuthDeviceStatus"),
						"400": errorResponseRef(),
						"404": errorResponseRef(),
						"409": errorResponseRef(),
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
			"/coddy/mcp": map[string]interface{}{
				"get": map[string]interface{}{
					"summary":     "List MCP servers",
					"description": "Returns the merged MCP server list from three levels: **`mcp_servers`** in config.yaml and the global **`<home>/mcp.json`** (scope `global`), plus the project-local **`.coddy/mcp.json`** (scope `local`); all mcp.json files are Cursor-compatible and later levels override earlier ones by name. Enabled servers are probed for their tool inventory over their transport (stdio spawn, streamable HTTP with legacy-SSE fallback, or SSE; connect, `tools/list`, close); results are cached until the server definition changes. **`?refresh=1`** forces a re-probe.\n\nA project-local entry arrives with the checkout, so it is **not** probed until it is approved for this workspace (see **POST** `/coddy/mcp/{name}/trust`): such a row comes back with `status: \"needs_approval\"`, `trusted: false`, no tools, and the `command`/`args`/`env`/`url`/`fingerprint` an approval would cover. Under `mcp.project_trust: deny` the status is `denied`.",
					"operationId": "listMCPServers",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "refresh", "in": "query", "required": false,
							"schema":      map[string]string{"type": "string"},
							"description": "Set to `1` to bypass the probe cache.",
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "MCP server list",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"$ref": "#/components/schemas/MCPServerList",
									},
								},
							},
						},
						"500": errorResponseRef(),
					},
				},
			},
			"/coddy/mcp/{name}/enable": map[string]interface{}{
				"post": map[string]interface{}{
					"summary":     "Enable an MCP server",
					"description": "Clears the disabled flag, persisting into the file that defines the server (config.yaml or `.coddy/mcp.json`). New sessions connect it; live sessions see its tools on their next turn.",
					"operationId": "enableMCPServer",
					"parameters":  []interface{}{mcpServerNameParam()},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Server enabled."},
						"400": errorResponseRef(),
					},
				},
			},
			"/coddy/mcp/{name}/disable": map[string]interface{}{
				"post": map[string]interface{}{
					"summary":     "Disable an MCP server",
					"description": "Sets the disabled flag in the owning file. The server's tools disappear from live sessions on their next turn; new sessions skip connecting it.",
					"operationId": "disableMCPServer",
					"parameters":  []interface{}{mcpServerNameParam()},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Server disabled."},
						"400": errorResponseRef(),
					},
				},
			},
			"/coddy/mcp/{name}/trust": map[string]interface{}{
				"post": map[string]interface{}{
					"summary":     "Approve a project MCP server for this workspace",
					"description": "Records the operator's approval of the **current** declaration of a project-local (`.coddy/mcp.json`) server for the server's workspace, so sessions may start it. The approval is bound to the workspace and to a digest of the command-bearing declaration (transport, command, args, env, url, headers), and is stored in `<home>/mcp-trust.json` with a receipt naming what was approved (env and header **names** only). Rewriting the entry withdraws it. Refused with 400 for servers defined in config.yaml or `<home>/mcp.json` (they need no approval) and under `mcp.project_trust: deny`.",
					"operationId": "trustMCPServer",
					"parameters":  []interface{}{mcpServerNameParam()},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Server approved; the response carries the approved `fingerprint`.",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"type": "object",
										"properties": map[string]interface{}{
											"ok":          map[string]interface{}{"type": "boolean"},
											"fingerprint": map[string]interface{}{"type": "string", "description": "Digest the approval is bound to."},
										},
									},
								},
							},
						},
						"400": errorResponseRef(),
						"500": errorResponseRef(),
					},
				},
			},
			"/coddy/mcp/{name}/untrust": map[string]interface{}{
				"post": map[string]interface{}{
					"summary":     "Withdraw a project MCP server approval",
					"description": "Removes the workspace approval of a project-local server. Sessions already holding a connected client keep it; new sessions no longer start the server. `removed` reports whether an approval was actually on file.",
					"operationId": "untrustMCPServer",
					"parameters":  []interface{}{mcpServerNameParam()},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Approval withdrawn (or none was on file).",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"type": "object",
										"properties": map[string]interface{}{
											"ok":      map[string]interface{}{"type": "boolean"},
											"removed": map[string]interface{}{"type": "boolean"},
										},
									},
								},
							},
						},
						"500": errorResponseRef(),
					},
				},
			},
			"/coddy/mcp/project-trust": map[string]interface{}{
				"post": map[string]interface{}{
					"summary":     "Set the project MCP trust policy",
					"description": "Persists **`mcp.project_trust`** into config.yaml and reloads it. Body: **`{\"policy\":\"ask\"|\"allow\"|\"deny\"}`**. `ask` (default) keeps project-local `.coddy/mcp.json` servers cold until each declaration is approved for its workspace; `allow` starts them automatically; `deny` never loads them. The MCP tab of the bundled UI edits this next to the servers it governs, so it never joins the settings-document save flow.",
					"operationId": "setMCPProjectTrust",
					"requestBody": map[string]interface{}{
						"required": true,
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{
									"type":     "object",
									"required": []string{"policy"},
									"properties": map[string]interface{}{
										"policy": map[string]interface{}{"type": "string", "enum": []string{"ask", "allow", "deny"}},
									},
								},
							},
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Policy stored; the response echoes the effective `project_trust`.",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"type": "object",
										"properties": map[string]interface{}{
											"ok":            map[string]interface{}{"type": "boolean"},
											"project_trust": map[string]interface{}{"type": "string", "enum": []string{"ask", "allow", "deny"}},
										},
									},
								},
							},
						},
						"400": errorResponseRef(),
					},
				},
			},
			"/coddy/mcp/{name}/tools/{tool}/enable": map[string]interface{}{
				"post": map[string]interface{}{
					"summary":     "Enable a single MCP tool",
					"description": "Removes **{tool}** from the server's disabled-tools list in the owning file.",
					"operationId": "enableMCPTool",
					"parameters":  []interface{}{mcpServerNameParam(), mcpToolNameParam()},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Tool enabled."},
						"400": errorResponseRef(),
					},
				},
			},
			"/coddy/mcp/{name}/tools/{tool}/disable": map[string]interface{}{
				"post": map[string]interface{}{
					"summary":     "Disable a single MCP tool",
					"description": "Adds **{tool}** to the server's disabled-tools list (`disabled_tools` in config.yaml, `disabledTools` in `.coddy/mcp.json`). The tool is hidden from the agent and rejected at dispatch.",
					"operationId": "disableMCPTool",
					"parameters":  []interface{}{mcpServerNameParam(), mcpToolNameParam()},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Tool disabled."},
						"400": errorResponseRef(),
					},
				},
			},
			"/coddy/mcp/{name}": map[string]interface{}{
				"put": map[string]interface{}{
					"summary":     "Create or update an mcp.json MCP server",
					"description": "Upserts one named entry in an mcp.json file (Cursor format: `env` and `headers` are objects, per-tool switches use `disabledTools`). **`?scope=local`** (default) writes the project **`.coddy/mcp.json`**; **`?scope=global`** writes the user-global **`<home>/mcp.json`**. Either `command` (stdio) or `url` is required; names must not contain `__`. Config.yaml-defined servers are edited via **PUT** `/coddy/config` instead.",
					"operationId": "putMCPServer",
					"parameters": []interface{}{
						mcpServerNameParam(),
						map[string]interface{}{
							"name": "scope", "in": "query", "required": false,
							"schema":      map[string]interface{}{"type": "string", "enum": []string{"global", "local"}},
							"description": "Target file: local (default) = ./.coddy/mcp.json, global = <home>/mcp.json.",
						},
					},
					"requestBody": map[string]interface{}{
						"required": true,
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{"$ref": "#/components/schemas/MCPJSONServer"},
							},
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Server saved."},
						"400": errorResponseRef(),
						"500": errorResponseRef(),
					},
				},
				"delete": map[string]interface{}{
					"summary":     "Delete an mcp.json MCP server",
					"description": "Removes the named entry from the mcp.json file that defines it (project **`.coddy/mcp.json`** or global **`<home>/mcp.json`**). Servers defined in config.yaml are refused with 400.",
					"operationId": "deleteMCPServer",
					"parameters":  []interface{}{mcpServerNameParam()},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Server deleted."},
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
					"description": "Best-effort cancellation of the current ReAct or direct completion turn. Writes a cross-process cancel signal for persisted bundles so another **coddy** process holding the turn can observe cooperative cancel. When assistant tokens were already streamed, the server persists that partial **assistant** message for the interrupted turn before the turn ends. Optional header **X-Coddy-Session-ID** must match **{id}** when set. Frames replayed to a subscriber carry an **`id:`** sequence; send it back as **Last-Event-ID** (or **`?last_event_id=`**) to resume after it instead of replaying the whole turn. When the frames a client asks to resume from have already been trimmed, the stream leads with **event: desync** so it can reload the transcript instead of rendering a gap. The primary **POST** stream is unchanged and carries no ids.",
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
			"/coddy/sessions/{id}/plans/{slug}": map[string]interface{}{
				"patch": map[string]interface{}{
					"summary":     "Run a design plan",
					"description": "Body **`{\"runPlan\": true}`** runs **RunPlan** synchronously: the session switches to **agent** mode, the plan document is injected and one agent turn runs. Prefer **POST /v1/responses** with **`metadata.runPlanSlug`** for streaming. The run is admitted like any other turn (turn lock, registration, cancellation).",
					"operationId": "coddyDesignPlanRun",
					"parameters": []interface{}{
						map[string]interface{}{
							"name": "id", "in": "path", "required": true,
							"schema":      map[string]string{"type": "string"},
							"description": "Session id.",
						},
						map[string]interface{}{
							"name": "slug", "in": "path", "required": true,
							"schema":      map[string]string{"type": "string"},
							"description": "Plan slug (the file name under the session's plans/ directory without the .plan.md suffix).",
						},
					},
					"requestBody": map[string]interface{}{
						"required": true,
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{
									"type":     "object",
									"required": []interface{}{"runPlan"},
									"properties": map[string]interface{}{
										"runPlan": map[string]interface{}{"type": "boolean", "description": "Must be true; any other patch is rejected."},
									},
								},
							},
						},
					},
					"responses": map[string]interface{}{
						"200": map[string]interface{}{
							"description": "Plan run finished",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{
										"type": "object",
										"properties": map[string]interface{}{
											"object":     map[string]string{"type": "string"},
											"stopReason": map[string]string{"type": "string"},
										},
									},
								},
							},
						},
						"400": errorResponseRef(),
						"404": errorResponseRef(),
						"409": map[string]interface{}{
							"description": "The session is in **ask** mode (read-only: switch the mode first, then run), the session is a subagent child (sub_…) whose transcript is read-only, or another turn already holds the session turn lock.",
							"content": map[string]interface{}{
								"application/json": map[string]interface{}{
									"schema": map[string]interface{}{"$ref": "#/components/schemas/ErrorEnvelope"},
								},
							},
						},
						"500": errorResponseRef(),
					},
				},
			},
			"/coddy/sessions/{id}/compact": map[string]interface{}{
				"post": map[string]interface{}{
					"summary":     "Compact (summarize) older session history",
					"description": "Summarizes conversation history into a single summary row inserted into the transcript. As a manual trigger it forces compaction, folding whatever exists even below the keep-recent boundary (**compaction.keep_recent_turns**, default 2 user turns) by reducing the kept tail as needed; nothing_to_compact is returned only when there is no prior conversation. Later LLM prompts replay only the summary plus the kept tail; the persisted transcript keeps every original message. Equivalent to the built-in **/compact** prompt command. Requires the composer turn lock (409 when another agent turn is running). A child session spawned by **spawn_agent** is a read-only transcript and answers **409** as well.",
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
				"SubagentCatalogEntry": map[string]interface{}{
					"type":        "object",
					"description": "One subagent definition as the catalog shows it, with the trust decision for the requested workspace.",
					"properties": map[string]interface{}{
						"name":           map[string]string{"type": "string"},
						"description":    map[string]string{"type": "string"},
						"scope":          map[string]interface{}{"type": "string", "enum": []string{"builtin", "user", "project"}},
						"path":           map[string]string{"type": "string", "description": "Definition file; absent for built-ins."},
						"digest":         map[string]string{"type": "string", "description": "SHA-256 of the definition bytes (the embedded bytes for built-ins)."},
						"model":          map[string]string{"type": "string", "description": "models[].model id from the frontmatter, when set."},
						"mode":           map[string]string{"type": "string", "description": "agent or plan from the frontmatter, when set."},
						"builtin":        map[string]string{"type": "boolean"},
						"hidden":         map[string]string{"type": "boolean", "description": "Kept out of the model-facing catalog; still spawnable by name."},
						"trust":          map[string]interface{}{"type": "string", "enum": []string{"trusted", "needs_approval"}},
						"trusted":        map[string]string{"type": "boolean"},
						"needs_approval": map[string]string{"type": "boolean"},
					},
					"required": []string{"name", "description", "scope", "builtin", "hidden", "trust", "trusted", "needs_approval"},
				},
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
				"MCPToolRow": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"name":        map[string]string{"type": "string", "description": "Tool name as advertised by the server."},
						"description": map[string]string{"type": "string"},
						"enabled":     map[string]interface{}{"type": "boolean", "description": "False when the tool is in the server's disabled-tools list."},
					},
				},
				"MCPServerRow": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"name":        map[string]string{"type": "string", "description": "Server name (unique across the merged list)."},
						"source":      map[string]interface{}{"type": "string", "enum": []string{"global", "local"}, "description": "Scope: global (config.yaml or <home>/mcp.json) or local (./.coddy/mcp.json)."},
						"origin":      map[string]interface{}{"type": "string", "enum": []string{"config", "home", "project"}, "description": "File that owns the definition: config.yaml, <home>/mcp.json, or ./.coddy/mcp.json."},
						"readonly":    map[string]interface{}{"type": "boolean", "description": "True for config.yaml-defined servers: not editable or deletable via this API."},
						"transport":   map[string]string{"type": "string", "description": "Effective transport: stdio, http (streamable, with legacy-SSE fallback), or sse."},
						"command":     map[string]string{"type": "string"},
						"args":        map[string]interface{}{"type": "array", "items": map[string]string{"type": "string"}},
						"url":         map[string]string{"type": "string"},
						"env":         map[string]interface{}{"type": "object", "additionalProperties": map[string]string{"type": "string"}},
						"headers":     map[string]interface{}{"type": "object", "additionalProperties": map[string]string{"type": "string"}, "description": "HTTP headers sent to http/sse servers."},
						"enabled":     map[string]interface{}{"type": "boolean", "description": "False when the server-level disabled switch is set."},
						"status":      map[string]interface{}{"type": "string", "enum": []string{"connected", "error", "disabled", "unsupported", "needs_approval", "denied"}, "description": "Probe result: connected (tools listed), error (probe failed), disabled (switched off), unsupported (unknown transport type), needs_approval (project entry awaiting workspace approval; not probed), denied (project entries switched off by mcp.project_trust)."},
						"error":       map[string]string{"type": "string", "description": "Probe error message when status is error or unsupported, or why the trust gate refused the entry."},
						"source_path": map[string]string{"type": "string", "description": "File the declaration was read from."},
						"trusted":     map[string]interface{}{"type": "boolean", "description": "False only for a project entry the workspace trust gate holds back."},
						"gated":       map[string]interface{}{"type": "boolean", "description": "True for project-local entries, the ones the trust gate applies to."},
						"fingerprint": map[string]string{"type": "string", "description": "Digest of the command-bearing declaration; an approval binds to this value."},
						"tools": map[string]interface{}{
							"type":  "array",
							"items": map[string]interface{}{"$ref": "#/components/schemas/MCPToolRow"},
						},
						"disabled_tools": map[string]interface{}{"type": "array", "items": map[string]string{"type": "string"}},
					},
				},
				"MCPServerList": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"object":        map[string]string{"type": "string", "example": "coddy.mcp_list"},
						"workspace":     map[string]string{"type": "string", "description": "Workspace the rows were merged for; approvals are recorded against it."},
						"project_trust": map[string]interface{}{"type": "string", "enum": []string{"ask", "allow", "deny"}, "description": "Effective mcp.project_trust policy."},
						"items": map[string]interface{}{
							"type":  "array",
							"items": map[string]interface{}{"$ref": "#/components/schemas/MCPServerRow"},
						},
					},
				},
				"MCPJSONServer": map[string]interface{}{
					"type":        "object",
					"description": "One mcp.json entry (global <home>/mcp.json or project .coddy/mcp.json; Cursor-compatible).",
					"properties": map[string]interface{}{
						"type":          map[string]interface{}{"type": "string", "enum": []string{"stdio", "http", "sse"}, "description": "Transport; empty means stdio. Inferred as http for url-only entries."},
						"command":       map[string]string{"type": "string", "description": "Executable for stdio transport."},
						"args":          map[string]interface{}{"type": "array", "items": map[string]string{"type": "string"}},
						"env":           map[string]interface{}{"type": "object", "additionalProperties": map[string]string{"type": "string"}},
						"url":           map[string]string{"type": "string", "description": "Remote endpoint for http/sse transports."},
						"headers":       map[string]interface{}{"type": "object", "additionalProperties": map[string]string{"type": "string"}},
						"disabled":      map[string]interface{}{"type": "boolean"},
						"disabledTools": map[string]interface{}{"type": "array", "items": map[string]string{"type": "string"}},
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
				"CoddyReasoningLevelsResponse": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"ok":    map[string]string{"type": "boolean"},
						"error": map[string]string{"type": "string"},
						"model": map[string]string{"type": "string"},
						"levels": map[string]interface{}{
							"type":        "array",
							"items":       map[string]string{"type": "string"},
							"description": "Levels detected for this model id, in the order the composer offers them. Empty for a model without reasoning support.",
						},
						"detected": map[string]interface{}{
							"type":        "boolean",
							"description": "False when the model id matches no reasoning family, so the UI can say so instead of writing an override.",
						},
					},
					"required": []string{"ok", "levels", "detected"},
				},
				"CodexAuthStatus": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"connected": map[string]string{"type": "boolean"},
						"source": map[string]interface{}{
							"type": "string", "enum": []string{"coddy", "codex_cli"},
						},
						"account_id": map[string]string{"type": "string"},
					},
					"required": []string{"connected"},
				},
				"CodexAuthDeviceStart": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"login_id":         map[string]string{"type": "string"},
						"verification_url": map[string]interface{}{"type": "string", "format": "uri"},
						"user_code":        map[string]string{"type": "string"},
						"status":           map[string]string{"type": "string", "example": "pending"},
						"connected":        map[string]string{"type": "boolean"},
					},
					"required": []string{"login_id", "verification_url", "user_code", "status", "connected"},
				},
				"CodexAuthDeviceStatus": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"status": map[string]interface{}{
							"type": "string", "enum": []string{"pending", "completed", "failed"},
						},
						"connected": map[string]string{"type": "boolean"},
						"error":     map[string]string{"type": "string"},
					},
					"required": []string{"status", "connected"},
				},
				"NeuralDeepAuthDeviceStartRequest": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"api_base": map[string]interface{}{
							"type":        "string",
							"enum":        []string{"https://api.neuraldeep.ru/v1", "https://api.neuraldeep.tech/v1"},
							"description": "Deployment to sign in against; decides which hub mints the key. Empty or absent: the saved provider row's `api_base`, else the default deployment.",
						},
					},
				},
				"NeuralDeepAuthDeviceStart": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"login_id": map[string]string{"type": "string"},
						"verification_url": map[string]interface{}{
							"type": "string", "format": "uri",
							"description": "Hub portal page to open: the complete URI with the pre-filled code when the hub provides one, otherwise the plain verification URI (the complete form is optional in RFC 8628).",
						},
						"user_code": map[string]string{"type": "string"},
						"status":    map[string]string{"type": "string", "example": "pending"},
						"connected": map[string]string{"type": "boolean"},
					},
					"required": []string{"login_id", "verification_url", "user_code", "status", "connected"},
				},
				"NeuralDeepAuthStatus": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"connected": map[string]string{"type": "boolean"},
						"masked": map[string]interface{}{
							"type":        "string",
							"description": "Display mask of the stored key (`sk-ab…1234`); never the key itself.",
						},
						"key_name": map[string]string{"type": "string"},
						"source": map[string]interface{}{
							"type":        "string",
							"enum":        []string{"oauth", "api_key", "api_key_command", "env", "none"},
							"description": "Credential requests actually use. An explicit api_key / command / env var wins over a stored hub login.",
						},
						"hub": map[string]interface{}{
							"type":        "string",
							"format":      "uri",
							"description": "Hub that issued the stored login, as recorded in the credential file. Omitted when there is no stored login.",
						},
						"endpoint_hub": map[string]interface{}{
							"type":        "string",
							"format":      "uri",
							"description": "Hub a sign-in for the queried endpoint (`api_base`, default: the saved row's) would use. Differs from `hub` when the stored login belongs to the other deployment.",
						},
					},
					"required": []string{"connected", "source"},
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
										"description": "Reasoning levels offered for this model (e.g. minimal, low, medium, high). Models served by a `type: codex` provider report `none` instead of `minimal`, which the Codex backend rejects. Omitted for non-reasoning models.",
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
						"files": map[string]interface{}{
							"type":        "array",
							"readOnly":    true,
							"description": "Coddy transcript extension for uploaded files on a user row.",
							"items":       map[string]interface{}{"$ref": "#/components/schemas/CoddyMessageFile"},
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
				"CoddyMessageFile": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"name":      map[string]string{"type": "string"},
						"mime_type": map[string]string{"type": "string"},
						"preview_url": map[string]interface{}{
							"type":        "string",
							"description": "Session-scoped URL for a bounded PNG preview; present only when the backend persisted a decodable image thumbnail.",
						},
					},
					"required": []string{"name", "mime_type"},
				},
				"ChatCompletionRequest": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"model": map[string]interface{}{
							"type":        "string",
							"description": "Any `id` from `GET /v1/models` (agent, plan, ask, or `models[].model`).",
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
							"description":          "Optional. For agent/plan/ask only, `model` key selects `models[].model`; `runPlanSlug` runs the named design plan (switches the session to agent) and is answered with **409** when `model` is `ask`. Not allowed for direct completion `model` values.",
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
							"description":          "Optional. For agent/plan/ask only, `model` key selects `models[].model`; `runPlanSlug` runs the named design plan (switches the session to agent) and is answered with **409** when `model` is `ask`.",
							"additionalProperties": true,
						},
						"attachments": map[string]interface{}{
							"type":        "array",
							"description": "Allowed only when **model** is **`agent`**, **`plan`**, or **`ask`**. Hydrated text file bodies from session **cwd** **path** fields, converted to UTF-8 when the file uses another detected encoding.",
							"items":       map[string]interface{}{"$ref": "#/components/schemas/ResponsesPromptAttachment"},
						},
						"inline_files": map[string]interface{}{
							"type":        "array",
							"description": "Supported for all modes when the effective YAML model has **`multimodal: true`**. Entries sent for a non-multimodal model are ignored and never forwarded to its provider. Each accepted file is saved to `~/.coddy/sessions/<id>/assets/`; decodable images also get a bounded PNG thumbnail for transcript history. For **`agent`** / **`plan`** / **`ask`**, the model receives a `<coddy_session_assets>` annotation with the on-disk paths. For direct YAML model, each entry also becomes an image content part sent inline to the provider.",
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

// coddyConfigErrorResponse documents the flat {"ok":false,"error":...} body that
// writeCoddyConfigErr emits for the /coddy/config routes, as opposed to the
// OpenAI-style nested ErrorEnvelope used by the /v1 surface.
func coddyConfigErrorResponse(description string) map[string]interface{} {
	return map[string]interface{}{
		"description": description,
		"content": map[string]interface{}{
			"application/json": map[string]interface{}{
				"schema": map[string]interface{}{
					"$ref": "#/components/schemas/CoddyConfigValidateResponse",
				},
			},
		},
	}
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

func codexProviderNameParameter() map[string]interface{} {
	return map[string]interface{}{
		"name":        "name",
		"in":          "path",
		"required":    true,
		"schema":      map[string]string{"type": "string"},
		"description": "Codex provider name. Valid unsaved provider names are accepted by the OAuth routes.",
	}
}

func jsonSchemaResponse(description, ref string) map[string]interface{} {
	return map[string]interface{}{
		"description": description,
		"content": map[string]interface{}{
			"application/json": map[string]interface{}{
				"schema": map[string]interface{}{"$ref": ref},
			},
		},
	}
}

func mcpServerNameParam() map[string]interface{} {
	return map[string]interface{}{
		"name": "name", "in": "path", "required": true,
		"schema":      map[string]string{"type": "string"},
		"description": "MCP server name (no `__`, spaces, or path separators).",
	}
}

func mcpToolNameParam() map[string]interface{} {
	return map[string]interface{}{
		"name": "tool", "in": "path", "required": true,
		"schema":      map[string]string{"type": "string"},
		"description": "Tool name as advertised by the server.",
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

// subagentNameParam is the {name} path parameter of the subagent trust routes.
func subagentNameParam() map[string]interface{} {
	return map[string]interface{}{
		"name": "name", "in": "path", "required": true,
		"schema":      map[string]string{"type": "string"},
		"description": "Subagent definition name as listed by **GET /coddy/subagents**.",
	}
}

// subagentTrustRequestBody is the optional body of the trust routes: the
// workspace the receipt is written for.
func subagentTrustRequestBody() map[string]interface{} {
	return map[string]interface{}{
		"required": false,
		"content": map[string]interface{}{
			"application/json": map[string]interface{}{
				"schema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"cwd": map[string]string{"type": "string", "description": "Absolute workspace path. Defaults to the server's default cwd; a relative path is a **400**."},
					},
				},
			},
		},
	}
}

// subagentEntryResponse describes a 200 carrying one refreshed catalog entry.
func subagentEntryResponse(description string) map[string]interface{} {
	return map[string]interface{}{
		"description": description,
		"content": map[string]interface{}{
			"application/json": map[string]interface{}{
				"schema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"object": map[string]string{"type": "string", "example": "coddy.subagent"},
						"item":   map[string]interface{}{"$ref": "#/components/schemas/SubagentCatalogEntry"},
					},
					"required": []string{"object", "item"},
				},
			},
		},
	}
}
