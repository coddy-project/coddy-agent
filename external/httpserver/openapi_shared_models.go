//go:build http

package httpserver

import "github.com/EvilFreelancer/coddy-agent/internal/llm"

// mergeSharedModelsOpenAPI adds the routes a Coddy shares its models through
// (docs/plans/remote-model-provider.md): the listing, the reserved usage route
// and the completions stream, with the error object, the frames and the
// credential rules. The schemas mirror internal/llm's wire types, which the
// handler and the `coddy` provider both encode through.
func mergeSharedModelsOpenAPI(doc map[string]interface{}) {
	paths := doc["paths"].(map[string]interface{})
	schemas := doc["components"].(map[string]interface{})["schemas"].(map[string]interface{})

	str := map[string]interface{}{"type": "string"}
	integer := map[string]interface{}{"type": "integer"}
	boolean := map[string]interface{}{"type": "boolean"}
	ref := func(name string) map[string]interface{} {
		return map[string]interface{}{"$ref": "#/components/schemas/" + name}
	}
	arrayOf := func(item map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{"type": "array", "items": item}
	}
	errorBody := func(description string) map[string]interface{} {
		return map[string]interface{}{
			"description": description,
			"content": map[string]interface{}{
				"application/json": map[string]interface{}{"schema": ref("CoddyLLMError")},
			},
		}
	}

	schemas["CoddyLLMError"] = map[string]interface{}{
		"type": "object",
		"description": "The error object of the shared-model routes: the flat JSON body of a request that was refused before its stream started, and, with `type: error`, the frame that ends a stream. " +
			"`message` is built by the remote from `kind` and `status` and says nothing of the upstream (never the provider's name or address, the selector or the upstream model id). " +
			"`kind` is one of `busy` (429, with `Retry-After: 1`), `rate` (the upstream provider is rate limiting), `quota` (a usage limit; `reset_at` and `retry_after_s` name the pause), `upstream` (the provider failed; `cause` is `status`, `stall`, `truncated` or `timeout`), `invalid` (400, 404, 408, 413) and `auth` (401, 403). " +
			"`code` refines `invalid`: `protocol_mismatch` (the message names both versions), `stale_revision` (`revision` is the row's current one), `unknown_model`, `request_too_large`, `body_timeout`, `invalid_option`. " +
			"`emitted` counts the chunk frames written before the error; a client keeps its own count.",
		"required": []string{"kind", "emitted"},
		"properties": map[string]interface{}{
			"type":          map[string]interface{}{"type": "string", "enum": []string{"error"}, "description": "Present on the error frame of a stream only."},
			"status":        map[string]interface{}{"type": "integer", "description": "Informational: the client decides by kind and cause."},
			"kind":          map[string]interface{}{"type": "string", "enum": []string{llm.WireKindBusy, llm.WireKindRate, llm.WireKindQuota, llm.WireKindUpstream, llm.WireKindInvalid, llm.WireKindAuth}},
			"code":          str,
			"message":       str,
			"retry_after_s": map[string]interface{}{"type": "number"},
			"reset_at":      map[string]interface{}{"type": "string", "format": "date-time"},
			"emitted":       boolean,
			"cause":         map[string]interface{}{"type": "string", "enum": []string{llm.WireCauseStatus, llm.WireCauseStall, llm.WireCauseTruncated, llm.WireCauseTimeout}, "description": "Meaningful for kind upstream only."},
			"revision":      map[string]interface{}{"type": "string", "description": "The row's current revision, on a stale_revision answer."},
		},
	}
	schemas["CoddyLLMModelRow"] = map[string]interface{}{
		"type":        "object",
		"description": "One shared model, under its alias. `revision` is an opaque keyed hash that changes whenever anything a client can observe about the row changes, or the model the alias points at; it is the same before and after a restart of the remote while nothing changed.",
		"required":    []string{"id", "revision", "max_context_tokens", "multimodal", "allow_reasoning_off"},
		"properties": map[string]interface{}{
			"id":                  map[string]interface{}{"type": "string", "description": "The alias: the only name of the model that leaves the host."},
			"revision":            str,
			"max_context_tokens":  integer,
			"multimodal":          boolean,
			"reasoning_levels":    arrayOf(str),
			"reasoning_default":   str,
			"allow_reasoning_off": boolean,
		},
	}
	schemas["CoddyLLMModelList"] = map[string]interface{}{
		"type":     "object",
		"required": []string{"protocol", "data"},
		"properties": map[string]interface{}{
			"protocol": map[string]interface{}{"type": "integer", "description": "The version of the wire; compared strictly."},
			"data":     arrayOf(ref("CoddyLLMModelRow")),
		},
	}
	schemas["CoddyLLMToolCall"] = map[string]interface{}{
		"type":     "object",
		"required": []string{"id", "name", "input"},
		"properties": map[string]interface{}{
			"id":    str,
			"name":  str,
			"input": map[string]interface{}{"type": "string", "description": "The raw JSON arguments."},
		},
	}
	schemas["CoddyLLMMessage"] = map[string]interface{}{
		"type":        "object",
		"description": "The part of a message a provider sees. A reasoning signature is the opaque envelope the remote sealed on the way out (tagged with the model it came from) and is sent back unchanged: an envelope of another model, or none, is dropped, never an error.",
		"required":    []string{"role", "content"},
		"properties": map[string]interface{}{
			"role":    map[string]interface{}{"type": "string", "enum": []string{"system", "user", "assistant", "tool"}},
			"content": str,
			"image_parts": arrayOf(map[string]interface{}{
				"type":     "object",
				"required": []string{"data_url"},
				"properties": map[string]interface{}{
					"data_url":  str,
					"mime_type": str,
					"name":      str,
				},
			}),
			"reasoning":           str,
			"reasoning_signature": str,
			"tool_calls":          arrayOf(ref("CoddyLLMToolCall")),
			"tool_call_id":        str,
		},
	}
	schemas["CoddyLLMRequest"] = map[string]interface{}{
		"type":     "object",
		"required": []string{"protocol", "model", "messages"},
		"properties": map[string]interface{}{
			"protocol": map[string]interface{}{"type": "integer", "description": "Compared strictly; a mismatch is an invalid error with code protocol_mismatch that names both versions."},
			"model":    map[string]interface{}{"type": "string", "description": "The alias. A provider/model selector is a 404."},
			"messages": arrayOf(ref("CoddyLLMMessage")),
			"tools": arrayOf(map[string]interface{}{
				"type":     "object",
				"required": []string{"name", "description", "input_schema"},
				"properties": map[string]interface{}{
					"name":         str,
					"description":  str,
					"input_schema": map[string]interface{}{"type": "object"},
				},
			}),
			"options": map[string]interface{}{
				"type":        "object",
				"description": "Only what the client sets explicitly; an absent option means \"as the row is configured\".",
				"properties": map[string]interface{}{
					"max_tokens":        map[string]interface{}{"type": "integer", "description": "The smaller of this and the row's own ceiling."},
					"temperature":       map[string]interface{}{"type": "number"},
					"reasoning_effort":  map[string]interface{}{"type": "string", "description": "Omitted means the row's reasoning_default; `off` only when the row has allow_reasoning_off."},
					"retry_budget_ms":   map[string]interface{}{"type": "integer", "format": "int64", "description": "The caller's remaining budget for waiting on a usage limit: a longer named pause is reported as a quota error at once."},
					"expected_revision": map[string]interface{}{"type": "string", "description": "The revision of the row in the listing the client last read; a different current one is answered 400 invalid, code stale_revision, before the provider is built."},
				},
			},
		},
	}
	schemas["CoddyLLMChunk"] = map[string]interface{}{
		"type":        "object",
		"description": "A progress notification mirroring a provider stream chunk. A tool call runs only from the final frame.",
		"required":    []string{"type"},
		"properties": map[string]interface{}{
			"type":            map[string]interface{}{"type": "string", "enum": []string{llm.WireTypeChunk}},
			"text_delta":      str,
			"reasoning_delta": str,
			"tool_call":       ref("CoddyLLMToolCall"),
			"tool_call_delta": ref("CoddyLLMToolCall"),
			"tool_call_named": ref("CoddyLLMToolCall"),
			"stop_reason":     str,
			"input_tokens":    integer,
			"output_tokens":   integer,
		},
	}
	schemas["CoddyLLMFinal"] = map[string]interface{}{
		"type":        "object",
		"description": "The authoritative response exactly as the remote's provider returned it, its reasoning signature sealed in an envelope tagged with the model.",
		"required":    []string{"type", "content", "input_tokens", "output_tokens", "cached_input_tokens"},
		"properties": map[string]interface{}{
			"type":                map[string]interface{}{"type": "string", "enum": []string{llm.WireTypeFinal}},
			"content":             str,
			"tool_calls":          arrayOf(ref("CoddyLLMToolCall")),
			"reasoning":           str,
			"reasoning_signature": str,
			"stop_reason":         str,
			"input_tokens":        integer,
			"output_tokens":       integer,
			"cached_input_tokens": integer,
		},
	}

	bearer := []interface{}{
		map[string]interface{}{"bearerAuth": []interface{}{}},
		map[string]interface{}{"cookieAuth": []interface{}{}},
	}
	access := "**Credentials.** A token of the LLM-only class (**`httpserver.shared_models.tokens`**) opens this route and the other two shared-model routes and nothing else: every other route answers it **401** like an unknown token. The main token and a signed-in browser open every route. " +
		"Any shared-model token closes the whole API gate, so a node that holds only shared-model tokens refuses every route but these three to everyone. " +
		"While **no credential of any class** is configured and **`httpserver.allow_insecure`** is not set, the route answers **403** with `kind: auth` (on a loopback listener too); once a credential exists an anonymous caller gets the gate's **401**. "

	paths[llm.CoddyModelsPath] = map[string]interface{}{"get": map[string]interface{}{
		"operationId": "coddyLLMModelsGet",
		"summary":     "List the models this Coddy shares",
		"description": "The `models[]` rows carrying **`shared_as`**, under their aliases, and nothing else: no provider name, no upstream model id, no `stream` field (the wire is always a stream). " + access,
		"security":    bearer,
		"responses": map[string]interface{}{
			"200": map[string]interface{}{
				"description": "The listing.",
				"content": map[string]interface{}{
					"application/json": map[string]interface{}{"schema": ref("CoddyLLMModelList")},
				},
			},
			"401": map[string]interface{}{"description": "Unauthorized (plain text)."},
			"403": errorBody("No credential is configured: shared models need authentication."),
		},
	}}

	paths["/coddy/llm/models/{alias}/usage"] = map[string]interface{}{"get": map[string]interface{}{
		"operationId": "coddyLLMModelUsageGet",
		"summary":     "Reserved: the usage of a shared model",
		"description": "Reserved for the second phase, which answers a projection of the account usage under the alias. Until then every alias answers **404**, and the answer names nothing. " + access,
		"security":    bearer,
		"parameters": []interface{}{
			map[string]interface{}{"name": "alias", "in": "path", "required": true, "schema": str},
		},
		"responses": map[string]interface{}{
			"401": map[string]interface{}{"description": "Unauthorized (plain text)."},
			"403": errorBody("No credential is configured."),
			"404": errorBody("Not offered by this remote."),
		},
	}}

	paths[llm.CoddyCompletionsPath] = map[string]interface{}{"post": map[string]interface{}{
		"operationId": "coddyLLMCompletionsPost",
		"summary":     "Run one stateless model call of a shared model",
		"description": "One model turn of another Coddy's harness: the request carries the history and the tool definitions, the remote runs the provider the row is configured with and answers **text/event-stream only**. " +
			"The call is **stateless**: no session, no hooks, no rules, nothing written to disk, no turn lock. The wire is always a stream, whatever `stream` the row has; a row with `stream: false` computes in one piece and is bounded by **`httpserver.shared_models.max_call_ms`**. " +
			access +
			"**Limit.** At most **`httpserver.shared_models.max_streams`** (5) calls run at once per credential, any alias: a further call is refused at once with **429**, `kind: busy` and `Retry-After: 1`, before its body is read and before the provider is called. " +
			"The slot is taken right after authentication; a `Content-Length` above 32 MiB is a **413** before any read, and a body that does not arrive within 30 s is a **408**; every refusal frees the slot. Clients send `Expect: 100-continue`, so a refused call does not upload the history. " +
			"**Stream.** One frame per `data:` line with no `event:` field: `chunk` (progress), then exactly one terminal frame, `final` or `error`; anything after it is not sent. The response is flushed after every frame. Comment lines `: hb` are heartbeats: the first is written right after the body has been read and checked, before the provider is called, then so that no two bytes are more than 15 s apart, while the model is silent too. " +
			"Each write has a deadline of about 60 s, so a peer that stops reading cuts the call; a disconnect cancels the upstream call. " +
			"A streamed row that makes no progress for **`agent.llm_stream_idle_timeout_ms`**, counted from the start of the provider call, ends as `error{kind: upstream, cause: stall}` (`emitted` says whether a chunk went out before). " +
			"The request body is at most 32 MiB and opens at most 524288 JSON objects and arrays (a real history is far below both): a larger one is a **413** with `code: request_too_large` before it is decoded.",
		"security": bearer,
		"requestBody": map[string]interface{}{
			"required": true,
			"content": map[string]interface{}{
				"application/json": map[string]interface{}{"schema": ref("CoddyLLMRequest")},
			},
		},
		"responses": map[string]interface{}{
			"200": map[string]interface{}{
				"description": "An event stream of `chunk` frames (CoddyLLMChunk) ending with exactly one `final` (CoddyLLMFinal) or `error` (CoddyLLMError with `type: error`) frame.",
				"headers": map[string]interface{}{
					"X-Coddy-Request-ID": map[string]interface{}{"description": "Names the call in the remote's log.", "schema": str},
				},
				"content": map[string]interface{}{
					"text/event-stream": map[string]interface{}{
						"schema": map[string]interface{}{
							"type":        "string",
							"description": "`data: {\"type\":\"chunk\"|\"final\"|\"error\",...}` lines separated by blank lines, with `: hb` comment lines between them.",
						},
					},
				},
			},
			"400": errorBody("Invalid request: protocol mismatch, stale revision (the answer carries the row's current `revision`), a malformed body, or an option the row or its provider cannot take. The message names the alias."),
			"401": map[string]interface{}{"description": "Unauthorized (plain text)."},
			"403": errorBody("No credential is configured: shared models need authentication."),
			"404": errorBody("No shared model is offered under that name. The alias is the only name that finds a row; a provider/model selector is a 404."),
			"408": errorBody("The request body did not arrive within 30 s."),
			"413": errorBody("The request is larger than 32 MiB, or opens more than 524288 JSON objects and arrays."),
			"429": map[string]interface{}{
				"description": "Busy: this credential already has `max_streams` calls running.",
				"headers": map[string]interface{}{
					"Retry-After": map[string]interface{}{"description": "1", "schema": str},
				},
				"content": map[string]interface{}{
					"application/json": map[string]interface{}{"schema": ref("CoddyLLMError")},
				},
			},
			"502": errorBody("The remote could not set the model up; its operator finds the reason in the remote's log."),
		},
	}}
}
