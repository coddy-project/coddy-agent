Feature: The command allowlist covers each command it names, not the commands written after it
  An entry of tools.command_allowlist lets its command run with any arguments and without a
  prompt. A command line can hold several commands - chained with &&, || or ;, sent to the
  background with &, piped, or on lines of their own - and it runs without a prompt only when
  every one of them is covered: "git status" must never end up authorising
  "git status && rm -rf ~", which a bare prefix match allowed (issue #646).

  Background:
    Given commands run in a POSIX shell
    And the operator's command allowlist holds "git status", "go test" and "tail"

  Scenario: Commands the allowlist covers run together without permission
    Then running "git status --short" needs no permission
    And running "go test ./... | tail -n 20" needs no permission
    And running "git status && go test ./..." needs no permission

  Scenario: A command written after an allowed one still needs permission
    Then running "git status && rm -rf /tmp/x" still needs permission
    And running "go test ./... | sh" still needs permission
    And running "git status --short; curl https://example.com" still needs permission
