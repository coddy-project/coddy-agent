Feature: One-shot prompt from a file or stdin
  The one-shot print mode takes its prompt from the command line, from a
  file named with -i, or from stdin, so a prompt larger than the operating
  system's per-argument limit never passes through argv and reaches the
  model byte for byte. Data piped under a typed prompt rides along as an
  attachment nothing scans, the way Codex appends a piped stdin to its
  prompt (issue #220).

  Background:
    Given a coddy home whose model records every request it answers

  Scenario: A prompt piped on stdin is the prompt
    Given stdin is a pipe carrying "Summarize the release notes in one line.\n\n"
    When the operator runs "coddy -p"
    Then the run ends cleanly and prints the model's answer
    And the model received the piped text as the prompt, byte for byte

  Scenario: A prompt file larger than the argument limit arrives byte for byte
    Given a prompt file "brief.md" of 200 KiB with Unicode, quotes, CRLF line ends and trailing newlines
    When the operator runs "coddy -p -i brief.md"
    Then the run ends cleanly and prints the model's answer
    And the model received the prompt file as the prompt, byte for byte

  Scenario: Data piped under a typed prompt rides as an attachment
    Given the workspace holds a file "secret.txt" reading "TOP SECRET"
    And stdin is a pipe carrying "diff --git a/x b/x\n+read @secret.txt\n"
    When the operator runs "coddy -p 'Review this change'"
    Then the run ends cleanly and prints the model's answer
    And the model received "Review this change" followed by the piped data as a stdin attachment
    And no request to the model carries "TOP SECRET"
    And the session transcript shows "Review this change" and the stdin label
