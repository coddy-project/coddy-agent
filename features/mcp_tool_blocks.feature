Feature: Every kind of block an MCP result carries reaches the agent
  A tools/call result is a list of content blocks. Besides text and pictures a
  server may return an embedded resource, which carries text or binary data, a
  link to a resource, or an audio clip. The text of a resource and a link come
  into the call's result in their place; a resource of a picture type goes the
  way of a picture; any other binary data is kept as a file with the session's
  assets, and the result names where, so the agent can open it with its tools.

  Scenario: Resources, a link and an audio clip of an MCP result
    Given an MCP server that returns a screenshot
    And the session model reads MCP images
    When the model calls the MCP tool that returns every kind of block and answers
    Then the MCP result carries the text resource and the resource link in their place
    And the picture resource reaches the model as a picture
    And the PDF resource and the audio clip are kept as files the result names
