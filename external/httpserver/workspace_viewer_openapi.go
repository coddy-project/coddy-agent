//go:build http

package httpserver

func mergeWorkspaceViewerOpenAPI(doc map[string]interface{}) {
	paths := doc["paths"].(map[string]interface{})
	property := func(kind string) map[string]interface{} { return map[string]interface{}{"type": kind} }
	param := func(name, kind, description string) map[string]interface{} {
		return map[string]interface{}{"name": name, "in": "query", "schema": property(kind), "description": description}
	}
	id := map[string]interface{}{"name": "id", "in": "path", "required": true, "schema": property("string")}
	path := param("path_rel", "string", "Workspace-relative path. Empty selects the tree root; raw and text require a file. No absolute paths or traversal.")
	response := func(schema map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{"description": "Workspace preview", "content": map[string]interface{}{"application/json": map[string]interface{}{"schema": schema}}}
	}
	object := func(props map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{"type": "object", "properties": props}
	}
	entry := object(map[string]interface{}{"name": property("string"), "path_rel": property("string"), "kind": map[string]interface{}{"type": "string", "enum": []string{"file", "directory", "symlink", "special"}}, "size_bytes": property("integer"), "mod_time": map[string]interface{}{"type": "string", "format": "date-time"}})
	paths["/coddy/sessions/{id}/workspace/tree"] = map[string]interface{}{"get": map[string]interface{}{
		"operationId": "coddyWorkspaceTreeGet", "summary": "List one workspace directory",
		"description": "Folders first, then files, each group sorted by name; one level only. Symlinks and special files are listed without following them. Hidden names, node_modules and vendor are hidden unless include_hidden=1; this is a navigation filter, not access control.",
		"parameters":  []interface{}{id, path, param("limit", "integer", "Default 200; 1..1000."), param("cursor", "string", "next_cursor of the previous page, passed back as it is; an opaque position in the listing (folders first, then files, each by name)."), param("include_hidden", "string", "Set to 1 to list hidden entries.")},
		"responses":   map[string]interface{}{"200": response(object(map[string]interface{}{"entries": map[string]interface{}{"type": "array", "items": entry}, "next_cursor": property("string"), "has_more": property("boolean")})), "400": errorResponseRef(), "404": errorResponseRef(), "500": errorResponseRef()},
	}}
	raw := map[string]interface{}{
		"summary":     "Read a regular workspace file",
		"description": "os.Root confines access to the session workspace. Fixed extension-plus-byte allowlist permits inline raster images, audio, video, PDF and plain .txt. HTML, SVG and unknown formats always download as application/octet-stream. A valid signed access_token substitutes for normal API authentication on this route only, including HEAD and Range. It binds session, workspace, path and download mode for at most one hour. Responses set private,no-cache, a weak metadata ETag, nosniff, no-referrer and sandbox CSP.",
		"parameters":  []interface{}{id, path, param("download", "string", "1 forces an attachment; otherwise view mode."), param("access_token", "string", "Optional signed capability minted by media-token."), map[string]interface{}{"name": "Range", "in": "header", "schema": property("string")}, map[string]interface{}{"name": "If-None-Match", "in": "header", "schema": property("string")}},
		"responses":   map[string]interface{}{"200": map[string]interface{}{"description": "File bytes (no body for HEAD)", "content": map[string]interface{}{"application/octet-stream": map[string]interface{}{"schema": map[string]interface{}{"type": "string", "format": "binary"}}}}, "206": map[string]interface{}{"description": "Requested byte range with Content-Range and Accept-Ranges"}, "304": map[string]interface{}{"description": "Unchanged file"}, "400": errorResponseRef(), "401": errorResponseRef(), "404": errorResponseRef(), "416": map[string]interface{}{"description": "Unsatisfiable range"}, "500": errorResponseRef()},
	}
	get := make(map[string]interface{}, len(raw)+1)
	head := make(map[string]interface{}, len(raw)+1)
	for key, value := range raw {
		get[key] = value
		head[key] = value
	}
	get["operationId"] = "coddyWorkspaceRawGet"
	head["operationId"] = "coddyWorkspaceRawHead"
	paths["/coddy/sessions/{id}/workspace/raw"] = map[string]interface{}{"get": get, "head": head}
	textProps := map[string]interface{}{"path_rel": property("string"), "lines": map[string]interface{}{"type": "array", "items": property("string")}, "offset": property("integer"), "next_offset": property("integer"), "has_more": property("boolean"), "total_lines_known": property("boolean"), "mod_time": map[string]interface{}{"type": "string", "format": "date-time"}, "size_bytes": property("integer"), "etag": property("string"), "charset": property("string")}
	paths["/coddy/sessions/{id}/workspace/text"] = map[string]interface{}{"get": map[string]interface{}{
		"operationId": "coddyWorkspaceTextGet", "summary": "Read a streaming UTF-8 line window",
		"description": "Detects a bounded encoding sample and decodes incrementally, including legacy encodings. No 512 KiB file cap. Lines have no terminators; offset is zero-based and the UI numbers lines from one. Each page and each line are bounded to 1 MiB. total_lines_known is true when EOF was reached. A changed expected ETag or a concurrent replacement returns 409.",
		"parameters":  []interface{}{id, path, param("offset", "integer", "Zero-based line offset, default 0; maximum 10000000."), param("max_lines", "integer", "Default 300; 1..1000."), param("etag", "string", "Expected file version from a previous page."), map[string]interface{}{"name": "If-None-Match", "in": "header", "schema": property("string")}},
		"responses":   map[string]interface{}{"200": response(object(textProps)), "304": map[string]interface{}{"description": "Unchanged file"}, "400": errorResponseRef(), "404": errorResponseRef(), "409": errorResponseRef(), "413": errorResponseRef(), "415": errorResponseRef()},
	}}
	paths["/coddy/sessions/{id}/workspace/media-token"] = map[string]interface{}{"post": map[string]interface{}{
		"operationId": "coddyWorkspaceMediaTokenPost", "summary": "Mint a scoped native media or download URL token",
		"description": "Requires normal API authentication. Validates a regular file first. The signature key is domain-separated from the configured API credential (or password login hash), so matching replicas and restarts preserve URLs; an unauthenticated server uses a random per-process key. Rotating the credential invalidates outstanding tokens. Cache-Control: no-store.",
		"parameters":  []interface{}{id}, "requestBody": map[string]interface{}{"required": true, "content": map[string]interface{}{"application/json": map[string]interface{}{"schema": map[string]interface{}{"type": "object", "required": []string{"path_rel"}, "properties": map[string]interface{}{"path_rel": property("string"), "download": property("boolean")}}}}},
		"responses": map[string]interface{}{"200": response(object(map[string]interface{}{"token": property("string"), "expires_at": map[string]interface{}{"type": "string", "format": "date-time"}})), "400": errorResponseRef(), "401": errorResponseRef(), "404": errorResponseRef()},
	}}
}
