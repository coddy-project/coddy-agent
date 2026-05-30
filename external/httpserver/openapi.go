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
					"description": "Set **title** (pinned title), **selectedModelId** (YAML **`models[].model`** selector for this session), and/or **markActivityRead** (boolean) to advance the read cursor for **activitySeq**. **markActivityRead** updates only activity counters in **session.json** and does not change **updatedAt** (history order stays stable until new chat content is saved).",
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
												"type": "array",
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
		},
		"components": map[string]interface{}{
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
