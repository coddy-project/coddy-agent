Feature: MCP tool calls reach the model and results reach the answer
  MCP tools are exposed to the model as native function definitions named
  server__tool (not as prompt text). When the model calls one, the agent
  routes the call to the owning server over its transport, feeds the result
  back into the ReAct loop, and the final answer can use it. Covered end to
  end on both surfaces with two servers so the routing is observable: "alpha"
  runs over stdio, "beta" over streamable HTTP, and each returns a distinct
  token.

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

  @acp
  Scenario: ACP session turn calls a stdio MCP tool
    Given an ACP session manager with MCP servers "alpha" over stdio and "beta" over streamable http
    And a scripted model that calls "alpha__get_token" and then answers with the tool result
    When I run an agent prompt through the ACP session flow
    Then the model was offered the tools "alpha__get_token" and "beta__get_token"
    And the final assistant message contains the alpha token
    And the "beta" server received no tool calls
