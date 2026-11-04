Feature: Share unchanged instruction text between session catalogs
  Loading another session in the same workspace should not retain another
  allocation of each rule and skill body. Each catalog still owns its objects.

  Scenario: Repeated discovery shares text and observes later file edits
    Given a workspace with a rule and a skill
    When instruction catalogs are loaded for 30 sessions
    Then the catalogs own separate objects sharing the unchanged text
    When the rule and skill files are edited
    Then a fresh catalog sees the edits while earlier catalogs keep their text
