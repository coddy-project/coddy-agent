Feature: List sessions with one History scan
  Scenario Outline: History shares its global scan
    Given a stored conversation and its subagent
    When I list History with include_subagents set to "<include>"
    Then the HTTP response contains "<ids>" and global active count 0
    And the listing selection uses one filesystem scan

    Examples:
      | include | ids                       |
      | false   | sess_example              |
      | true    | sess_example,sess_child   |
