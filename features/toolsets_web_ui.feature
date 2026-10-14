Feature: The web UI offers and edits toolsets
  A toolsets[] preset names the tools a session is offered (tools, the
  allowlist, and disallowed_tools, the denylist). Settings has a Toolsets tab
  that edits the presets, and a logical model's row names its own preset in a
  Tools block under Reasoning, picked from the presets or none. The composer
  shows a toolset chip right after the permission chip while the configuration
  has a preset, and none otherwise; a chip picked before the chat has a
  session rides in with its first message.

  Scenario: A model row names its toolset and the Toolsets tab edits the presets
    Then the logical model form groups its fields: model, generation, reasoning, tools
    And a model row picks its toolset from the presets, or none
    And the Toolsets tab edits the lists of a preset in place
    And renaming a preset renames it on the models that name it
    And a toolset added in Settings starts with both lists empty

  Scenario: The composer offers the toolset while there is a preset
    Then the toolset chip follows the permission chip and switches the toolset
    And the toolset chip appears once a reload brings a preset
    And a toolset picked on the start screen rides in with the first message

  Scenario: Toolsets are named in Russian
    Then the toolset of a model and the lists of a preset are named in Russian
