Feature: Structured tool cards
  The transcript presents known tool calls as readable actions and results.

  Scenario: A model switch names its settings
    Then the model switch card shows the model, reasoning and lifetime

  Scenario: An HTTP exchange is readable
    Then the HTTP card separates the request and response and masks credentials

  Scenario: Background work is identifiable
    Then the background task card lists task ids and states
    And the preview server card links to its address

  Scenario: Documentation search presents references
    Then the documentation search card lists matching sections

  Scenario: Design plans retain their structure
    Then the plan cards show the list, document and saved identity
