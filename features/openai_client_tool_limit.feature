Feature: A configurable limit on the tools a client offers
  A direct model server takes as many client tools as httpserver.max_client_tools
  allows, so a client whose MCP servers offer more than OpenAI's 128 can still
  use it, and the model receives every tool. The limit is on the model's row of
  GET /v1/models, so a client sizes its request before it sends one.

  Scenario: A chat completion offering more than 128 tools reaches a server configured for them
    Given a direct model server that takes up to 200 client tools
    When an OpenAI client posts a chat completion offering 177 tools
    Then the model receives all 177 tools and the client receives its answer

  Scenario: A client reads the configured limit off the model listing
    Given a direct model server that takes up to 200 client tools
    When an OpenAI client asks for the model listing
    Then the direct model row says it takes 200 client tools
    And the agent row says it takes none of the client's tools
