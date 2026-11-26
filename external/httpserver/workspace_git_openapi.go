//go:build http

package httpserver

func workspaceGraphOpenAPI() map[string]interface{} {
	str := map[string]string{"type": "string"}
	strList := map[string]interface{}{"type": "array", "items": str}
	return map[string]interface{}{
		"summary":     "Read the Git commit graph",
		"operationId": "coddyWorkspaceGraphGet",
		"description": "Read-only topological history of the repository's branches, remote branches and tags, plus HEAD, including a detached HEAD; stash and notes are left out. Requires the git executable. No fetch. Returns an empty array for an unborn repository. A read is bounded to 20 seconds and gives 400 when it runs over. Path overrides the session folder as in workspace/context. Offset pagination should be restarted with offset 0 after repository changes.",
		"parameters": []interface{}{
			map[string]interface{}{"name": "path", "in": "query", "schema": str},
			map[string]interface{}{"name": "X-Coddy-Session-ID", "in": "header", "schema": str},
			map[string]interface{}{"name": "offset", "in": "query", "schema": map[string]interface{}{"type": "integer", "default": 0, "minimum": 0, "maximum": 100000}},
			map[string]interface{}{"name": "limit", "in": "query", "schema": map[string]interface{}{"type": "integer", "default": 100, "minimum": 1, "maximum": 500}},
		},
		"responses": map[string]interface{}{
			"200": map[string]interface{}{"description": "Commit page", "content": map[string]interface{}{"application/json": map[string]interface{}{"schema": map[string]interface{}{
				"type": "object", "required": []string{"commits", "has_more"},
				"properties": map[string]interface{}{
					"has_more": map[string]string{"type": "boolean"},
					"commits": map[string]interface{}{"type": "array", "items": map[string]interface{}{
						"type": "object", "required": []string{"hash", "parents", "subject", "author", "date", "refs"},
						"properties": map[string]interface{}{"hash": str, "parents": strList, "subject": str, "author": str, "date": map[string]string{"type": "string", "format": "date-time"}, "refs": strList},
					}},
				},
			}}}},
			"400": errorResponseRef(), "404": errorResponseRef(),
		},
	}
}

func workspaceBranchesOpenAPI() map[string]interface{} {
	return map[string]interface{}{
		"summary":     "Create and switch to a local branch before a session exists",
		"operationId": "coddyWorkspaceBranchesPost",
		"description": "Creates branch at the current HEAD and switches immediately in the chosen checkout. Never resets an existing branch. The path query selects the folder, otherwise the server default is used. A session header is refused with 400: session clients must POST branch and create:true to /coddy/sessions/{id}/workspace, which enforces the conversation lock. Git failures, including invalid or existing names, return 409.",
		"parameters":  []interface{}{map[string]interface{}{"name": "path", "in": "query", "schema": map[string]string{"type": "string"}}},
		"requestBody": map[string]interface{}{"required": true, "content": map[string]interface{}{"application/json": map[string]interface{}{"schema": map[string]interface{}{
			"type": "object", "required": []string{"branch"}, "properties": map[string]interface{}{"branch": map[string]string{"type": "string"}},
		}}}},
		"responses": map[string]interface{}{
			"200": map[string]interface{}{"description": "Workspace after creation", "content": map[string]interface{}{"application/json": map[string]interface{}{"schema": map[string]string{"$ref": "#/components/schemas/CoddyWorkspaceContext"}}}},
			"400": errorResponseRef(), "409": errorResponseRef(),
		},
	}
}
