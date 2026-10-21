Feature: MCP tool calls reach the model and results reach the answer
  MCP tools are exposed to the model as native function definitions named
  server__tool (not as prompt text). When the model calls one, the agent
  routes the call to the owning server over its transport, feeds the result
  back into the ReAct loop, and the final answer can use it. Covered end to
  end on both surfaces with two servers so the routing is observable: "alpha"
  runs over stdio, "beta" over streamable HTTP, and each returns a distinct
  token. How a server is started is the operator's choice, and every kind
  works the same: a program run directly, an npm package run through npx
  (saved and started exactly as it was written), a remote server over
  streamable HTTP and one over the legacy SSE transport. A tool that returns
  a screenshot hands it to a model that reads images and to the web UI over
  each of them, a picture over 3 MiB included: more than 4 MiB of base64 on
  one line of an event stream.

  @openai
  Scenario: HTTP gateway turns call MCP tools across transports and honor disable switches
    Given a coddy HTTP server with MCP servers "alpha" over stdio and "beta" over streamable http
    And a scripted model that calls "beta__get_token" and then answers with the tool result
    When I send an agent prompt over POST /v1/responses
    Then the model was offered the tools "alpha__get_token" and "beta__get_token"
    And the final assistant message contains the beta token
    And the "beta" server received exactly one tool call
    When I send another agent prompt with the model now calling "alpha__get_token"
    Then the final assistant message contains the alpha token
    When I disable the tool "get_token" of MCP server "beta" over the management API
    And I send another agent prompt with the model now calling "alpha__get_token"
    Then the model was not offered the tool "beta__get_token"

  @openai
  Scenario: A turn calls tools on servers run as a binary, through npx and over the network
    Given a coddy HTTP server with MCP servers "native" run as a binary, "remote" over streamable http and "legacy" over sse
    And a scripted model that calls the tool of every MCP server in turn and then answers with their results
    When I register the MCP server "packaged" that npx starts from a local package through the management API
    Then the project mcp.json runs "packaged" with the command and arguments as they were sent
    When I send an agent prompt over POST /v1/responses
    Then the model was offered the tool of every MCP server
    And the final assistant message contains the token of every MCP server

  @openai
  Scenario: Screenshots MCP tools return reach a model that reads images and the web UI over every transport
    Given a coddy HTTP server whose model reads images, with screenshot tools on MCP servers "native" run as a binary, "remote" over streamable http, "streamed" over streamable http answering with event streams and "legacy" over sse
    And a scripted model that takes a screenshot with every MCP server in turn and then answers
    When I send an agent prompt over POST /v1/responses with streaming on
    Then the stream gives every screenshot call its picture for the web UI
    And the model saw every screenshot right after its tool result
    And the transcript keeps every screenshot on its tool row and serves the picture the server returned

  @acp
  Scenario: ACP session turn calls a stdio MCP tool
    Given an ACP session manager with MCP servers "alpha" over stdio and "beta" over streamable http
    And a scripted model that calls "alpha__get_token" and then answers with the tool result
    When I run an agent prompt through the ACP session flow
    Then the model was offered the tools "alpha__get_token" and "beta__get_token"
    And the final assistant message contains the alpha token
    And the "beta" server received no tool calls
