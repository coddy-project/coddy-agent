//go:build http

package httpserver

// mergeCanvasOpenAPI adds the canvas routes: the boards of a chat (.canvas
// files of its workspace), their frames, the browser's reports and the frames'
// own addresses.
func mergeCanvasOpenAPI(doc map[string]interface{}) {
	paths := doc["paths"].(map[string]interface{})
	prop := func(kind string) map[string]interface{} { return map[string]interface{}{"type": kind} }
	object := func(props map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{"type": "object", "properties": props}
	}
	id := map[string]interface{}{"name": "id", "in": "path", "required": true, "schema": prop("string"), "description": "Session id; a subagent's id acts on the canvas of the top session of its spawn tree."}
	frameParam := map[string]interface{}{"name": "frame", "in": "path", "required": true, "schema": prop("string"), "description": "The id of the frame's node in the file."}
	fileParam := func(required bool, description string) map[string]interface{} {
		return map[string]interface{}{"name": "file", "in": "query", "required": required, "schema": prop("string"), "description": description}
	}
	theFile := fileParam(true, "The .canvas file, relative to the session's workspace, outside dot-named folders.")
	jsonBody := func(schema map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{"required": true, "content": map[string]interface{}{"application/json": map[string]interface{}{"schema": schema}}}
	}
	ok := func(description string, schema map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{"description": description, "content": map[string]interface{}{"application/json": map[string]interface{}{"schema": schema}}}
	}
	report := object(map[string]interface{}{
		"stamp": prop("string"), "loaded": prop("boolean"), "closed": prop("boolean"), "error": prop("string"), "title": prop("string"),
		"errors": prop("integer"), "warnings": prop("integer"), "at": map[string]interface{}{"type": "string", "format": "date-time"},
	})
	frame := object(map[string]interface{}{
		"id": prop("string"), "path": prop("string"), "root": prop("string"), "title": prop("string"),
		"device": map[string]interface{}{"type": "string", "enum": []string{"desktop", "tablet", "phone"}},
		"width":  prop("integer"), "height": prop("integer"), "x": prop("integer"), "y": prop("integer"),
		"added_by":   prop("string"),
		"gen":        map[string]interface{}{"type": "integer", "description": "The frame's generation; its address is signed for it, and a rotate moves it on."},
		"stamp":      map[string]interface{}{"type": "string", "description": "Digest of the names, sizes and modification times of the files under the frame's root; it moves when they change."},
		"url":        map[string]interface{}{"type": "string", "description": "The frame's address relative to the API base: /coddy/canvas/<capability>/<page>. Valid while the file holds the frame at this generation, for 12 hours."},
		"expires_at": map[string]interface{}{"type": "string", "format": "date-time"},
		"report":     report,
	})
	item := object(map[string]interface{}{
		"id": prop("string"), "type": map[string]interface{}{"type": "string", "enum": []string{"text", "file", "link", "group"}},
		"x": prop("integer"), "y": prop("integer"), "width": prop("integer"), "height": prop("integer"),
		"color": map[string]interface{}{"type": "string", "description": "A preset \"1\" to \"6\" or a hex color, as JSON Canvas writes it."},
		"text":  prop("string"), "file": prop("string"), "url": prop("string"), "label": prop("string"),
		"note": map[string]interface{}{"type": "string", "description": "Why a file node naming a page is not a live frame."},
	})
	edge := object(map[string]interface{}{
		"id": prop("string"), "fromNode": prop("string"), "fromSide": prop("string"), "fromEnd": prop("string"),
		"toNode": prop("string"), "toSide": prop("string"), "toEnd": prop("string"), "color": prop("string"), "label": prop("string"),
	})
	board := object(map[string]interface{}{
		"session_id": prop("string"),
		"file":       map[string]interface{}{"type": "string", "description": "The board's .canvas file, relative to the workspace; empty while the chat has no canvas."},
		"current":    map[string]interface{}{"type": "boolean", "description": "Whether the agent shows pages on this file when it names none."},
		"revision":   map[string]interface{}{"type": "integer", "description": "Moves with every change made through the canvas in any of the chat's files."},
		"stamp":      map[string]interface{}{"type": "string", "description": "Moves when the file changes, by whoever changed it; \"missing\" when it is gone."},
		"error":      map[string]interface{}{"type": "string", "description": "Why the file could not be read; nothing of it is shown or written then."},
		"frames":     map[string]interface{}{"type": "array", "items": frame},
		"items":      map[string]interface{}{"type": "array", "items": item, "description": "The file's nodes that are not live frames."},
		"edges":      map[string]interface{}{"type": "array", "items": edge},
	})
	errs := func(codes ...string) map[string]interface{} {
		out := map[string]interface{}{}
		for _, c := range codes {
			out[c] = errorResponseRef()
		}
		return out
	}
	with := func(base map[string]interface{}, code string, resp map[string]interface{}) map[string]interface{} {
		base[code] = resp
		return base
	}
	paths["/coddy/sessions/{id}/canvas"] = map[string]interface{}{"get": map[string]interface{}{
		"operationId": "coddyCanvasGet", "summary": "Read a canvas of the chat",
		"description": "A .canvas file of the session's workspace (JSON Canvas, the format of Obsidian's canvases) as the canvas shows it: its file nodes naming HTML pages as frames, each with its stamp, a fresh address and the counts of the newest browser report, its other nodes and its edges. Without file, the file the agent shows pages on, or an answer with an empty file while the chat has none. Reading writes nothing. 404 while tools.canvas.enable is false.",
		"parameters":  []interface{}{id, fileParam(false, "The .canvas file; default the chat's current one.")},
		"responses":   with(errs("400", "404", "409"), "200", ok("The board", board)),
	}}
	paths["/coddy/sessions/{id}/canvas/open"] = map[string]interface{}{"post": map[string]interface{}{
		"operationId": "coddyCanvasOpen", "summary": "Make a .canvas file the chat's canvas",
		"description": "The agent shows pages on this file from now on when it names none; the web UI calls it when the person opens the file. Writes nothing to the file. 400 for a file that is missing or does not parse.",
		"parameters":  []interface{}{id, theFile},
		"responses":   with(errs("400", "404", "409"), "200", ok("The board", board)),
	}}
	paths["/coddy/sessions/{id}/canvas/frames"] = map[string]interface{}{"post": map[string]interface{}{
		"operationId": "coddyCanvasFrameAdd", "summary": "Put a page of the workspace on a canvas",
		"description": "Adds a file node for the page to the file, or, for the same page at the same size, returns the frame already there with reason shown. Without file the page goes on the chat's current canvas, or on a new canvas/<page folder>.canvas (created says so). The page must be an .html or .htm file of the session's workspace outside dot-named folders; the root, by default the page's folder, must contain it. 409 past 24 frames.",
		"parameters":  []interface{}{id, fileParam(false, "An existing .canvas file; default the chat's current one.")},
		"requestBody": jsonBody(object(map[string]interface{}{"path": prop("string"), "root": prop("string"), "title": prop("string"), "device": prop("string"), "width": prop("integer"), "height": prop("integer")})),
		"responses": with(errs("400", "404", "409"), "200", ok("The frame", object(map[string]interface{}{
			"reason": prop("string"), "file": prop("string"), "created": prop("boolean"), "frame": frame, "revision": prop("integer"),
		}))),
	}}
	paths["/coddy/sessions/{id}/canvas/frames/{frame}"] = map[string]interface{}{"delete": map[string]interface{}{
		"operationId": "coddyCanvasFrameRemove", "summary": "Take a frame off a canvas",
		"description": "Removes the frame's node and the edges at it from the file; the page's files stay, and every address of the frame stops working.",
		"parameters":  []interface{}{id, frameParam, theFile},
		"responses":   with(errs("400", "404"), "200", ok("Removed", object(map[string]interface{}{"revision": prop("integer")}))),
	}}
	paths["/coddy/sessions/{id}/canvas/frames/{frame}/rotate"] = map[string]interface{}{"post": map[string]interface{}{
		"operationId": "coddyCanvasFrameRotate", "summary": "Revoke a frame's addresses",
		"description": "Moves the frame to its next generation, kept in the session and never in the file, so every address handed out before stops working; the web UI calls it when the frame's page navigated away from the canvas.",
		"parameters":  []interface{}{id, frameParam, theFile},
		"responses":   with(errs("400", "404"), "200", ok("The frame with its new address", object(map[string]interface{}{"frame": frame, "revision": prop("integer")}))),
	}}
	paths["/coddy/sessions/{id}/canvas/nodes"] = map[string]interface{}{"patch": map[string]interface{}{
		"operationId": "coddyCanvasNodesUpdate", "summary": "Move or resize nodes of a canvas",
		"description": "Frames (by a size or a device), text cards, links and groups, in one change written to the file once (a group dragged with what it holds): all of them, or none and 404 when one names a node the file does not hold. Only the nodes named are rewritten, in the key order Obsidian writes. At most 500.",
		"parameters":  []interface{}{id, theFile},
		"requestBody": jsonBody(object(map[string]interface{}{"nodes": map[string]interface{}{"type": "array", "items": object(map[string]interface{}{
			"id": prop("string"), "x": prop("integer"), "y": prop("integer"), "width": prop("integer"), "height": prop("integer"),
			"device": map[string]interface{}{"type": "string", "enum": []string{"desktop", "tablet", "phone"}, "description": "A frame's size by name, instead of width and height."},
		})}})),
		"responses": with(errs("400", "404"), "200", ok("Changed", object(map[string]interface{}{"revision": prop("integer")}))),
	}}
	paths["/coddy/sessions/{id}/canvas/frames/{frame}/report"] = map[string]interface{}{"post": map[string]interface{}{
		"operationId": "coddyCanvasFrameReport", "summary": "Report what the browser saw in a frame",
		"description": "Kept in memory, the newest per frame, at most 20 console lines of at most 1,000 characters; canvas_inspect reads it. stamp names the files the frame was loaded for; closed says the frame was put away.",
		"parameters":  []interface{}{id, frameParam, theFile},
		"requestBody": jsonBody(object(map[string]interface{}{
			"stamp": prop("string"), "loaded": prop("boolean"), "closed": prop("boolean"), "error": prop("string"), "title": prop("string"),
			"console": map[string]interface{}{"type": "array", "items": object(map[string]interface{}{"level": map[string]interface{}{"type": "string", "enum": []string{"error", "warning"}}, "message": prop("string"), "source": prop("string"), "line": prop("integer"), "column": prop("integer")})},
		})),
		"responses": with(errs("400", "404"), "204", map[string]interface{}{"description": "Stored"}),
	}}
	file := map[string]interface{}{
		"summary": "Serve a file of a frame",
		"description": "Authenticated by the capability in the path alone (no bearer, no cookie), valid while the .canvas file holds the frame at the generation it was signed for, in the session's working directory. Serves the files of the frame's root through os.Root: GET and HEAD, no dot-named paths, no symbolic links, a folder's index.html, never a listing. " +
			"Every response carries Content-Security-Policy: sandbox allow-scripts allow-forms allow-modals with the allowed sources, nosniff, Referrer-Policy: no-referrer, Access-Control-Allow-Origin: * and Cache-Control: no-store; an HTML page gets the bridge script at the start of its head. A preflight OPTIONS is answered with Access-Control-Allow-Origin: *. " +
			"Every other route refuses a request whose Origin is null (403).",
		"parameters": []interface{}{
			map[string]interface{}{"name": "capability", "in": "path", "required": true, "schema": prop("string")},
			map[string]interface{}{"name": "path", "in": "path", "required": true, "schema": prop("string"), "description": "The file, relative to the frame's root; .coddy/bridge.js is the bridge."},
		},
		"responses": map[string]interface{}{
			"200": map[string]interface{}{"description": "File bytes", "content": map[string]interface{}{"application/octet-stream": map[string]interface{}{"schema": map[string]interface{}{"type": "string", "format": "binary"}}}},
			"301": map[string]interface{}{"description": "A folder without its trailing slash"},
			"401": map[string]interface{}{"description": "The address is invalid, expired or no longer applies"},
			"404": map[string]interface{}{"description": "No such file of the frame"},
			"405": map[string]interface{}{"description": "Not GET or HEAD"},
		},
	}
	get := map[string]interface{}{"operationId": "coddyCanvasFileGet"}
	head := map[string]interface{}{"operationId": "coddyCanvasFileHead"}
	for k, v := range file {
		get[k] = v
		head[k] = v
	}
	paths["/coddy/canvas/{capability}/{path}"] = map[string]interface{}{"get": get, "head": head}
}
