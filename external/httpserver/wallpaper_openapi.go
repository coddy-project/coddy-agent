//go:build http

package httpserver

func wallpaperErrorResponse(description string) map[string]interface{} {
	return map[string]interface{}{
		"description": description,
		"content":     map[string]interface{}{"text/plain": map[string]interface{}{"schema": map[string]string{"type": "string"}}},
	}
}

func mergeWallpaperOpenAPI(doc map[string]interface{}) {
	paths := doc["paths"].(map[string]interface{})
	paths["/coddy/wallpapers"] = map[string]interface{}{"get": map[string]interface{}{
		"operationId": "wallpapersGet",
		"summary":     "List the local wallpaper gallery",
		"description": "Reads regular files with recognized raster headers directly inside CODDY_HOME/wallpapers, sorted by name. A missing wallpapers directory returns an empty array; other I/O errors are not an empty gallery. Links, dot names and special files are never listed. PNG, JPEG, GIF and WebP headers are recognized without decoding pixels. There are no file-size, dimension, pixel or frame-count limits; the browser determines renderability. No external service is contacted. Uses the normal API bearer or browser-cookie authentication policy; query tokens are not accepted.",
		"responses": map[string]interface{}{
			"200": map[string]interface{}{"description": "The local pictures, names only", "content": map[string]interface{}{"application/json": map[string]interface{}{"schema": map[string]interface{}{
				"type": "object", "required": []string{"wallpapers"}, "properties": map[string]interface{}{"wallpapers": map[string]interface{}{
					"type": "array", "items": map[string]interface{}{"type": "object", "required": []string{"name"}, "properties": map[string]interface{}{"name": map[string]string{"type": "string"}}},
				}},
			}}}},
			"401": wallpaperErrorResponse("Authentication required"),
			"403": wallpaperErrorResponse("Requests from an opaque origin are refused"),
			"500": wallpaperErrorResponse("The gallery cannot be read"),
		},
	}}
	content := map[string]interface{}{}
	for _, kind := range []string{"image/png", "image/jpeg", "image/gif", "image/webp"} {
		content[kind] = map[string]interface{}{"schema": map[string]string{"type": "string", "format": "binary"}}
	}
	get := map[string]interface{}{
		"operationId": "wallpaperGet", "summary": "Read a local wallpaper",
		"description": "Streams the original file after recognizing its raster header, without buffering the file or decoding pixels. File size, dimensions, pixel count and GIF frame count are unrestricted. The browser determines whether pixel data is renderable. The media type is detected from the content, not the extension. Links are refused, including a linked wallpapers directory. Response headers include Cache-Control: no-store and X-Content-Type-Options: nosniff. Authentication follows the normal API policy; query tokens are not accepted.",
		"parameters":  []interface{}{map[string]interface{}{"name": "name", "in": "path", "required": true, "schema": map[string]string{"type": "string"}, "description": "A single visible file name as returned by the gallery, URL-encoded. No path separators, dot-prefixed names, colons or control characters."}},
		"responses": map[string]interface{}{
			"200": map[string]interface{}{"description": "Original raster bytes", "content": content},
			"400": wallpaperErrorResponse("Invalid file name"),
			"401": wallpaperErrorResponse("Authentication required"),
			"403": wallpaperErrorResponse("Requests from an opaque origin are refused"),
			"404": wallpaperErrorResponse("Picture absent or not a regular file"),
			"415": wallpaperErrorResponse("Unrecognized or malformed raster header"),
			"500": wallpaperErrorResponse("The gallery or picture cannot be read"),
		},
	}
	headResponses := map[string]interface{}{}
	for status, response := range get["responses"].(map[string]interface{}) {
		headResponses[status] = map[string]interface{}{"description": response.(map[string]interface{})["description"]}
	}
	uploadSchema := map[string]interface{}{
		"type": "object", "required": []string{"wallpaper", "created"}, "additionalProperties": false,
		"properties": map[string]interface{}{
			"wallpaper": map[string]interface{}{"type": "object", "required": []string{"name"}, "additionalProperties": false, "properties": map[string]interface{}{"name": map[string]string{"type": "string"}}},
			"created":   map[string]string{"type": "boolean"},
		},
	}
	uploadResponse := func(description string) map[string]interface{} {
		return map[string]interface{}{"description": description, "content": map[string]interface{}{"application/json": map[string]interface{}{"schema": uploadSchema}}}
	}
	uploadContent := map[string]interface{}{"application/octet-stream": map[string]interface{}{"schema": map[string]string{"type": "string", "format": "binary"}}}
	for kind, schema := range content {
		uploadContent[kind] = schema
	}

	paths["/coddy/wallpapers/{name}"] = map[string]interface{}{
		"get": get,
		"head": map[string]interface{}{
			"operationId": "wallpaperHead", "summary": "Read a local wallpaper's headers",
			"description": "Recognizes the raster header and returns the same media type, original Content-Length, Cache-Control and nosniff headers as GET, without a response body.",
			"parameters":  get["parameters"], "responses": headResponses,
		},
		"post": map[string]interface{}{
			"operationId": "wallpaperPost", "summary": "Upload an original wallpaper",
			"description": "Takes raw image bytes, not multipart data, and streams them into a private temporary file in CODDY_HOME/wallpapers. Creates the gallery directory when missing. PNG, JPEG, GIF and WebP headers are recognized without decoding pixels or imposing size, geometry, pixel or frame-count limits. SHA-256 matches reuse an existing picture even when the requested name differs; the returned name identifies the actual gallery item. New bytes with an occupied name use a numbered suffix before the extension, without replacing any entry. Publication is atomic, with no overwrite, and serializes deduplication across uploads in this process, not across separate processes. Failed or cancelled uploads discard their temporary file. Uses the normal bearer or same-origin browser-cookie authentication policy; query tokens are not accepted.",
			"parameters":  get["parameters"],
			"requestBody": map[string]interface{}{"required": true, "content": uploadContent},
			"responses": map[string]interface{}{
				"200": uploadResponse("Identical SHA-256 content already exists; created is false"),
				"201": uploadResponse("A new picture was stored; created is true"),
				"400": wallpaperErrorResponse("Invalid file name"),
				"401": wallpaperErrorResponse("Authentication required"),
				"403": wallpaperErrorResponse("Opaque-origin or cross-site cookie mutation refused"),
				"415": wallpaperErrorResponse("Unrecognized or malformed raster header"),
				"500": wallpaperErrorResponse("The upload, gallery scan or atomic publication failed"),
			},
		},
		"delete": map[string]interface{}{
			"operationId": "wallpaperDelete", "summary": "Delete a local wallpaper",
			"description": "Removes a named regular image from the gallery after recognizing its header. Refuses links, special files, non-images and unsafe names. The client asks for confirmation before sending this request. Authentication follows the normal bearer or same-origin browser-cookie policy; query tokens are not accepted. Returns no response body.",
			"parameters":  get["parameters"],
			"responses": map[string]interface{}{
				"204": map[string]interface{}{"description": "The picture was deleted"},
				"400": wallpaperErrorResponse("Invalid file name"),
				"401": wallpaperErrorResponse("Authentication required"),
				"403": wallpaperErrorResponse("Opaque-origin or cross-site cookie mutation refused"),
				"404": wallpaperErrorResponse("Picture absent, replaced or not a regular file"),
				"415": wallpaperErrorResponse("Unrecognized or malformed raster header"),
				"500": wallpaperErrorResponse("The gallery or picture cannot be read or removed"),
			},
		},
	}
}
