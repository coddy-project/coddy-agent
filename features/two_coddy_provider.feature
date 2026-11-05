Feature: Two Coddy processes chained as client agent and direct model provider
  A real Coddy agent (A) talks to a second real Coddy (B) configured as an
  OpenAI-compatible provider, and B talks to a scripted recording upstream.
  The chain exercises reasoning replay (#701) and tool forwarding end to end
  with no live LLM.

  # The happy path: A runs its own ReAct loop and its own read tool, B only
  # relays to the scripted upstream, and the follow-up turn replays the
  # assistant reasoning and the tool result without duplicating them.
  Scenario: A drives B through a tool turn and a follow-up
    Given a scripted upstream that answers a read tool call then a final answer
    And a Coddy server B whose provider points at that upstream
    And a Coddy server A whose provider points at B
    When A runs an agent turn asking to read note.txt in its workspace
    Then the read tool executes in the workspace of A only
    And the upstream sees the assistant reasoning and the tool result on the follow-up
    And the follow-up answer confirms the tool result
