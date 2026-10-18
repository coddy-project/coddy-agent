//go:build http

package httpserver

// mergeCanvasOpenAPI adds the canvas routes: the board of a chat, its frames,
// the browser's reports and the frames' own addresses.
func mergeCanvasOpenAPI(doc map[string]interface{}) {
	paths := doc["paths"].(map[string]interface{})
	prop := func(kind string) map[string]interface{} { return map[string]interface{}{"type": kind} }
	object := func(props map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{"type": "object", "properties": props}
	}
	id := map[string]interface{}{"name": "id", "in": "path", "required": true, "schema": prop("string"), "description": "Session id; a subagent's id acts on the board of the top session of its spawn tree."}
	frameParam := map[string]interface{}{"name": "frame", "in": "path", "required": true, "schema": prop("string")}
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
		"added_by": prop("string"), "added_at": map[string]interface{}{"type": "string", "format": "date-time"},
		"shown_at":   map[string]interface{}{"type": "string", "format": "date-time"},
		"gen":        map[string]interface{}{"type": "integer", "description": "The frame's generation; its address is signed for it, and a rotate moves it on."},
		"stamp":      map[string]interface{}{"type": "string", "description": "Digest of the names, sizes and modification times of the files under the frame's root; it moves when they change."},
		"url":        map[string]interface{}{"type": "string", "description": "The frame's address relative to the API base: /coddy/canvas/<capability>/<page>. Valid while the frame is on the board at this generation, for 12 hours."},
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
	link := object(map[string]interface{}{
		"path":  map[string]interface{}{"type": "string", "description": "The linked .canvas file, relative to the workspace."},
		"stamp": map[string]interface{}{"type": "string", "description": "Moves when the file changes; \"missing\" when it is gone."},
		"error": map[string]interface{}{"type": "string", "description": "Why the file could not be read; the board then shows nothing of it."},
	})
	board := object(map[string]interface{}{
		"session_id": prop("string"), "revision": prop("integer"), "frames": map[string]interface{}{"type": "array", "items": frame},
		"link":  link,
		"items": map[string]interface{}{"type": "array", "items": item, "description": "The nodes of a linked file that are not live frames."},
		"edges": map[string]interface{}{"type": "array", "items": edge},
	})
	canvasFile := jsonBody(object(map[string]interface{}{"path": map[string]interface{}{"type": "string", "description": "A .canvas file, relative to the session's workspace, outside dot-named folders."}}))
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
		"operationId": "coddyCanvasGet", "summary": "Read the chat's canvas",
		"description": "The frames of the board, each with its stamp, a fresh address and the counts of the newest browser report. 404 while tools.canvas.enable is false.",
		"parameters":  []interface{}{id},
		"responses":   with(errs("400", "404", "409"), "200", ok("The board", board)),
	}}
	paths["/coddy/sessions/{id}/canvas/frames"] = map[string]interface{}{"post": map[string]interface{}{
		"operationId": "coddyCanvasFrameAdd", "summary": "Put a page of the workspace on the canvas",
		"description": "The same page at the same size is one frame: showing it again returns that frame with reason shown. The page must be an .html or .htm file of the session's workspace outside dot-named folders; the root, by default the page's folder, must contain it. 409 past 24 frames.",
		"parameters":  []interface{}{id},
		"requestBody": jsonBody(object(map[string]interface{}{"path": prop("string"), "root": prop("string"), "title": prop("string"), "device": prop("string"), "width": prop("integer"), "height": prop("integer")})),
		"responses":   with(errs("400", "404", "409"), "200", ok("The frame", object(map[string]interface{}{"reason": prop("string"), "frame": frame, "revision": prop("integer")}))),
	}}
	paths["/coddy/sessions/{id}/canvas/frames/{frame}"] = map[string]interface{}{
		"patch": map[string]interface{}{
			"operationId": "coddyCanvasFrameUpdate", "summary": "Move, resize or rename a frame",
			"parameters":  []interface{}{id, frameParam},
			"requestBody": jsonBody(object(map[string]interface{}{"title": prop("string"), "device": prop("string"), "width": prop("integer"), "height": prop("integer"), "x": prop("integer"), "y": prop("integer")})),
			"responses":   with(errs("400", "404"), "200", ok("The frame", object(map[string]interface{}{"frame": frame, "revision": prop("integer")}))),
		},
		"delete": map[string]interface{}{
			"operationId": "coddyCanvasFrameRemove", "summary": "Take a frame off the canvas",
			"description": "The files stay; every address of the frame stops working.",
			"parameters":  []interface{}{id, frameParam},
			"responses":   with(errs("404"), "200", ok("Removed", object(map[string]interface{}{"revision": prop("integer")}))),
		},
	}
	paths["/coddy/sessions/{id}/canvas/frames/{frame}/rotate"] = map[string]interface{}{"post": map[string]interface{}{
		"operationId": "coddyCanvasFrameRotate", "summary": "Revoke a frame's addresses",
		"description": "Moves the frame to its next generation, so every address handed out before stops working; the web UI calls it when the frame's page navigated away from the canvas.",
		"parameters":  []interface{}{id, frameParam},
		"responses":   with(errs("404"), "200", ok("The frame with its new address", object(map[string]interface{}{"frame": frame, "revision": prop("integer")}))),
	}}
	paths["/coddy/sessions/{id}/canvas/nodes"] = map[string]interface{}{"patch": map[string]interface{}{
		"operationId": "coddyCanvasNodesUpdate", "summary": "Move or resize nodes of the canvas",
		"description": "Frames, and the text cards, links and groups of a linked .canvas file, in one change written to the file once (a group dragged with what it holds): all of them, or none and 404 when one names a node the board does not hold. At most 500.",
		"parameters":  []interface{}{id},
		"requestBody": jsonBody(object(map[string]interface{}{"nodes": map[string]interface{}{"type": "array", "items": object(map[string]interface{}{"id": prop("string"), "x": prop("integer"), "y": prop("integer"), "width": prop("integer"), "height": prop("integer")})}})),
		"responses":   with(errs("400", "404"), "200", ok("Changed", object(map[string]interface{}{"revision": prop("integer")}))),
	}}
	paths["/coddy/sessions/{id}/canvas/link"] = map[string]interface{}{
		"post": map[string]interface{}{
			"operationId": "coddyCanvasLink", "summary": "Open a .canvas file of the workspace on the canvas",
			"description": "From now on the board is that JSON Canvas file (the format of Obsidian's canvases): its file nodes naming HTML pages are live frames, the other nodes and the edges are drawn, and every change is written back to the file, keeping what Coddy does not know. Linking writes nothing. The chat's own frames wait until the link is undone.",
			"parameters":  []interface{}{id},
			"requestBody": canvasFile,
			"responses":   with(errs("400", "404", "409"), "200", ok("The board", board)),
		},
		"delete": map[string]interface{}{
			"operationId": "coddyCanvasUnlink", "summary": "Give the chat its own canvas back",
			"description": "The file stays as it is. 409 while the board is not linked.",
			"parameters":  []interface{}{id},
			"responses":   with(errs("404", "409"), "200", ok("The board", board)),
		},
	}
	paths["/coddy/sessions/{id}/canvas/export"] = map[string]interface{}{"post": map[string]interface{}{
		"operationId": "coddyCanvasExport", "summary": "Save the canvas as a .canvas file",
		"description": "Writes the chat's own board as a new JSON Canvas file of the workspace, making the folders it needs, and links the board to it. 409 when the file exists or the board is already linked.",
		"parameters":  []interface{}{id},
		"requestBody": canvasFile,
		"responses":   with(errs("400", "404", "409"), "200", ok("The board", board)),
	}}
	paths["/coddy/sessions/{id}/canvas/frames/{frame}/report"] = map[string]interface{}{"post": map[string]interface{}{
		"operationId": "coddyCanvasFrameReport", "summary": "Report what the browser saw in a frame",
		"description": "Kept in memory, the newest per frame, at most 20 console lines of at most 1,000 characters; canvas_inspect reads it. stamp names the files the frame was loaded for; closed says the frame was put away.",
		"parameters":  []interface{}{id, frameParam},
		"requestBody": jsonBody(object(map[string]interface{}{
			"stamp": prop("string"), "loaded": prop("boolean"), "closed": prop("boolean"), "error": prop("string"), "title": prop("string"),
			"console": map[string]interface{}{"type": "array", "items": object(map[string]interface{}{"level": map[string]interface{}{"type": "string", "enum": []string{"error", "warning"}}, "message": prop("string"), "source": prop("string"), "line": prop("integer"), "column": prop("integer")})},
		})),
		"responses": with(errs("400", "404"), "204", map[string]interface{}{"description": "Stored"}),
	}}
	file := map[string]interface{}{
		"summary": "Serve a file of a frame",
		"description": "Authenticated by the capability in the path alone (no bearer, no cookie), valid while the frame is on the board at the generation it was signed for. Serves the files of the frame's root through os.Root: GET and HEAD, no dot-named paths, no symbolic links, a folder's index.html, never a listing. " +
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
