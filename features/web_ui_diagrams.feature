Feature: Diagrams and formulas in the web UI
  The agent's Mermaid diagrams, SVG pictures and LaTeX formulas show up rendered in the chat,
  with their source a click away. The renderers are loaded on demand, so the page a browser
  opens first stays as small as it was.

  Scenario: The renderers ship as chunks loaded on demand
    Given the embedded web UI build
    Then the diagram and formula renderers are not part of app.js
    And app.js stays within its size budget
    And the renderers are embedded as content-hashed chunks

  Scenario: A browser keeps a chunk it has fetched
    Given the embedded web UI build
    When a browser fetches a renderer chunk
    Then the chunk is served as JavaScript
    And the response may be cached for a year without revalidation
