Feature: Structured tool cards
  The transcript presents known tool calls, and the calls of MCP servers, as
  readable actions and results instead of their JSON arguments over the raw
  text the tool wrote for the model.

  Scenario: A model switch names its settings
    Then the model switch card shows the model, reasoning and lifetime

  Scenario: An HTTP exchange is readable
    Then the HTTP card separates the request and response and masks credentials
    And the HTTP card names the real address, the proxy and the certificate check

  Scenario: Background work is identifiable
    Then the background task card lists task ids and states
    And the preview server card links to its address

  Scenario: Documentation presents references and sections
    Then the documentation search card lists matching sections
    And the documentation read card renders the section with a link to the reader

  Scenario: Design plans retain their structure
    Then the plan cards show the list, document and saved identity

  Scenario: Session filing, configuration and memory read as fields
    Then the session filing card shows the title, the tags and what changed
    And the configuration card shows its answer as fields
    And the memory cards render notes and list hits

  Scenario: An MCP server's call reads as fields and its answer as what it is
    Then the MCP card shows its arguments and a JSON answer as fields
    And the MCP card renders a Markdown answer as a document

  Scenario: A card shows the numbers a server sent, not the ones JavaScript can hold
    Then the MCP card shows an id past 2^53 and a repeated key as the server wrote them
    And the MCP card shows a JSON array answer as the server's own text, indented

  Scenario: A long command on a phone keeps its output below it
    Then the phone cap never reaches a command block
