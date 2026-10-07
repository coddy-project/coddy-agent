Feature: The web UI tells the model what it draws
  The web UI draws Mermaid and SVG fences as pictures and typesets LaTeX. A
  turn sent from it carries a system prompt block saying so, for that turn
  only, so the agent answers with a diagram or a formula when one says it
  better. Other clients of the same API keep the plain prompt.

  Scenario: A turn from the web UI carries the web UI's block
    Given a Coddy HTTP server with a recording agent
    When the web UI sends "draw the request path" to /v1/responses
    Then the turn's system prompt includes "## Answering in the Coddy web UI"
    And the block names mermaid fences and LaTeX formulas
    And the session keeps no trace of the block after the turn
