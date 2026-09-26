Feature: Project rules come from one agent's folder
  Every coding agent keeps its rules in a folder of its own, and a project that
  works with several of them usually keeps the same rules in each: Cursor's
  .cursor/rules/workflow.mdc and its Claude Code mirror .claude/rules/workflow.md.
  Coddy reads one of those folders, never all of them, so every rule reaches the
  model once: its own .coddy/rules when the project has one, else the shared
  .agents/rules, else Cursor's .cursor/rules, else Claude Code's .claude/rules.
  A folder without a rule file in it does not count, and the operator's own
  rules under CODDY_HOME/rules join whichever folder was read.

  Scenario: Cursor's folder is read and its Claude Code mirror is not
    Given a project whose ".cursor/rules" folder holds these rule files:
      | file         | frontmatter       | body                  |
      | workflow.mdc | alwaysApply: true | CURSOR_WORKFLOW_TOKEN |
    And its ".claude/rules" folder holds these rule files:
      | file        | frontmatter | body                  |
      | workflow.md |             | CLAUDE_WORKFLOW_TOKEN |
    When the operator lists the rules catalog
    Then the catalog lists "workflow" from source "cursor" in the "cursor" format
    And the catalog lists no rule from source "claude"
    And the catalog names ".claude/rules" as a folder it did not read

  Scenario: The model is told a mirrored rule once
    Given a project whose ".cursor/rules" folder holds these rule files:
      | file         | frontmatter       | body                  |
      | workflow.mdc | alwaysApply: true | CURSOR_WORKFLOW_TOKEN |
    And its ".claude/rules" folder holds these rule files:
      | file        | frontmatter | body                  |
      | workflow.md |             | CLAUDE_WORKFLOW_TOKEN |
    And a coddy agent session in that project
    When the model answers without touching any file
    Then the request carries "CURSOR_WORKFLOW_TOKEN" exactly once
    And the request carries neither "CLAUDE_WORKFLOW_TOKEN"

  Scenario: Coddy's own folder is read before any other agent's
    Given a project whose ".coddy/rules" folder holds these rule files:
      | file      | frontmatter       | body        |
      | house.mdc | alwaysApply: true | CODDY_TOKEN |
    And its ".cursor/rules" folder holds these rule files:
      | file         | frontmatter       | body         |
      | workflow.mdc | alwaysApply: true | CURSOR_TOKEN |
    And a coddy agent session in that project
    When the model answers without touching any file
    Then the request carries "CODDY_TOKEN"
    And the request carries neither "CURSOR_TOKEN"

  Scenario: Claude Code's folder is read when no folder before it holds a rule
    Given a project whose ".claude/rules" folder holds these rule files:
      | file     | frontmatter | body               |
      | style.md |             | CLAUDE_STYLE_TOKEN |
    And its ".cursor/rules" folder holds no rule file
    And a coddy agent session in that project
    When the model answers without touching any file
    Then the request carries "CLAUDE_STYLE_TOKEN"
