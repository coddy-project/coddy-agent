Feature: Mentions keep the provider's prompt cache
  A provider caches a request by its prefix, and the system message opens
  every request. What a message brings - a mentioned file, the rules that file
  activates, a rule the user names, the body of a skill it invokes - rides in
  that message and is written there once. The system message stays the one
  the provider has cached, and a later turn replays the earlier message byte
  for byte, so the cached copy of the conversation behind it holds.

  Background:
    Given a project with a file "main.go", a rule for Go files reading "GO_RULE_TOKEN", a mention-only rule "deploy" reading "DEPLOY_RULE_TOKEN" and a skill "release-notes" reading "RELEASE_SKILL_TOKEN"

  Scenario: What a message brings rides in it, and the next turn replays it unchanged
    When the user sends "review @main.go, follow @deploy, then /release-notes" and the model answers
    And the user sends "thanks" and the model answers
    Then the first turn's message carries "GO_RULE_TOKEN", "DEPLOY_RULE_TOKEN" and "RELEASE_SKILL_TOKEN"
    And every request of both turns opens with the same system message
    And the second turn replays the first turn's message unchanged
