Feature: A detached subagent's permission prompt reaches every surface that can show it
  A background subagent outlives the turn that spawned it, so when it needs a
  permission the chat stream of that turn is gone. `coddy serve` hosts several
  surfaces over one session manager - the web UI, a remote console attached to
  it, a Telegram bot - and any of them may be where the person reading that
  conversation is. So the runtime does not pick one: it offers the prompt to
  every surface that can show it, and the first answer settles it everywhere.
  The surfaces that did not answer withdraw the prompt, so nobody is left with a
  button that no longer does anything.

  A surface that cannot show a given prompt - a chat bot asked about a session
  that was never a chat of its own - stays out of it, and only when no surface
  at all can show the prompt is the subagent refused with a reason.

  Scenario: the prompt reaches every surface, and the first answer settles it
    Given a web surface and a chat surface that can both show the prompt
    When a detached subagent "writer" asks for permission
    Then the web surface shows the prompt of "writer"
    And the chat surface shows the prompt of "writer"
    When the chat surface answers "allow"
    Then the subagent is answered "allow"
    And the prompt is withdrawn from the web surface

  Scenario: a surface that does not own the conversation stays out of it
    Given a web surface that can show the prompt
    And a chat surface that does not know the parent session
    When a detached subagent "writer" asks for permission
    Then the web surface shows the prompt of "writer"
    And the chat surface shows nothing
    When the web surface answers "reject"
    Then the subagent is answered "reject"
