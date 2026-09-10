Feature: a turn started outside the web UI is watchable in it
  A Telegram message runs a turn on a shared session. The chat is the only surface
  that can answer a permission prompt, but a browser watching that session sees the
  turn stream while it happens, and can continue the conversation afterwards.

  Scenario: the assistant text reaches both surfaces
    Given a live session "gw_mirror"
    And a chat sender attached to that session
    When the turn is mirrored and the agent emits "hello from telegram"
    Then the chat sender received "hello from telegram"
    And a web client watching session "gw_mirror" receives "hello from telegram"

  Scenario: only the chat is asked for permission
    Given a live session "gw_mirror"
    And a chat sender attached to that session
    When the turn is mirrored and a tool asks for permission
    Then the chat sender answered the permission request
    And the web client was not asked to answer it

  Scenario: the mirror is released when the turn ends
    Given a live session "gw_mirror"
    And a chat sender attached to that session
    When the turn is mirrored and then finishes
    Then no relay is left registered for session "gw_mirror"

  Scenario: a turn already being watched is not interrupted by a second surface
    Given a live session "gw_mirror"
    And a chat sender attached to that session
    And a turn already publishing on that session
    When the turn is mirrored
    Then the existing watcher still has its stream
