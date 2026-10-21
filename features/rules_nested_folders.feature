Feature: The rule folder of a subfolder applies to that subtree
  A session opened on a folder that holds several projects, each a repository
  with rules of its own, reads the rule folder of the workspace when it starts
  and the rule folder of a subfolder the first time a filesystem tool reaches a
  path under it. Nothing is walked to find them. A nested rule applies to its
  folder and everything below it, never above or beside it: its globs are
  anchored at its folder, and it arrives with the result of the call that first
  reached it, so the system message stays the one the session started with.

  Background:
    Given a group folder whose own Cursor rule "group" always applies and reads "GROUP_RULE_TOKEN"
    And its subproject "mobile" keeps a Cursor rule "mobile" that always applies and reads "MOBILE_RULE_TOKEN", with a copy for Claude Code
    And its subproject "db" keeps a Coddy rule "db" for "migrations/**" that reads "DB_RULE_TOKEN"

  Scenario: Each subproject's rules reach the model only inside that subproject
    Given a coddy agent session in the group folder
    When the model reads "db/schema.sql", then "db/migrations/0001.sql", then "mobile/app.ts", and answers
    Then every request carries "GROUP_RULE_TOKEN" in its system message
    And no system message carries "MOBILE_RULE_TOKEN" or "DB_RULE_TOKEN"
    And the read of "db/schema.sql" brings in no rule
    And "DB_RULE_TOKEN" reaches the model with the first read of "db/migrations/0001.sql"
    And "MOBILE_RULE_TOKEN" reaches the model with the first read of "mobile/app.ts"
    And no request carries the copy of any rule kept for Claude Code
    And every request carries each rule at most once

  Scenario: A subproject's rule named like a rule of the group applies next to it
    Given the group folder and its subproject "mobile" each keep a Cursor rule "workflow" that always applies, reading "GROUP_WORKFLOW_TOKEN" and "MOBILE_WORKFLOW_TOKEN"
    And a coddy agent session in the group folder
    When the model reads "mobile/app.ts", and answers
    Then every request carries "GROUP_WORKFLOW_TOKEN" in its system message
    And "MOBILE_WORKFLOW_TOKEN" and "MOBILE_RULE_TOKEN" reach the model with the first read of "mobile/app.ts"
    And every request carries each rule at most once
