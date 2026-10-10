Feature: The settings form edits the tools a model is offered
  A models[] row narrows the tools a session is offered while it runs on that
  model with two lists, tools (the allowlist) and disallowed_tools (the
  denylist). Settings, Logical models, a row shows them in a Tools block under
  Reasoning: the allowed tools and then the disallowed ones, each a list of
  names with Add and a trash, named in the language of the interface. The
  block says how an entry is written (an exact name, * or a prefix*, an MCP
  tool as server__tool) and how the lists combine, and each list says what it
  does on its own. An edit lands on the row's own key and nowhere else.

  Scenario: A model row has a Tools block with both lists
    Then the logical model form groups its fields: model, generation, reasoning, tools
    And the tool lists of a model row are edited in place and saved on the row
    And a model row without tool lists shows two empty lists and writes nothing until one is added to
    And a model added in Settings starts with both tool lists empty
    And the Tools block says how an entry is written and how the lists combine

  Scenario: The tool lists are named in Russian
    Then the tool lists and their block are named in Russian, with no English left over
