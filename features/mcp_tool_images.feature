Feature: MCP image results reach the model and session
  A screenshot returned as an MCP image block belongs to the tool result.

  Scenario: A multimodal model receives an MCP screenshot
    Given an MCP server that returns a screenshot
    And the session model reads MCP images
    When the model calls the MCP screenshot tool and answers
    Then the MCP result keeps its screenshot in the transcript and session assets
    And the next model request carries the screenshot after the MCP result
    And the MCP tool update gives the surfaces the screenshot

  Scenario: A text-only model does not receive an MCP screenshot
    Given an MCP server that returns a screenshot
    When the text-only model calls the MCP screenshot tool and answers
    Then the MCP result keeps its screenshot in the transcript and session assets
    And the next model request names the screenshot without image data
