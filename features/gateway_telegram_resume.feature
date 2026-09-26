Feature: Resuming another session from a Telegram chat
  A chat is bound to one session, and /clear replaces it with a fresh one: the
  session it left stays on disk, out of the chat's reach. /resume is the way
  back. Alone it offers the sessions the server keeps as a keyboard of titles,
  newest first, and a tap binds the chat to the one chosen. With a query it
  resumes the session whose id or title the words name, and offers the keyboard
  when more than one does. The choice rewrites the same mapping /clear writes,
  so it survives a restart of the gateway. The reply names what the resumed
  session runs on: its own model and reasoning level, never the ones last
  picked on this bot (#362).

  Background:
    Given a telegram gateway over a server keeping these sessions:
      | id                            | title                  | updated              |
      | sess_aaaaaaaaaaaaaaaaaaaaaaaa | Fix the login redirect | 2026-09-18T10:00:00Z |
      | sess_bbbbbbbbbbbbbbbbbbbbbbbb | Login form validation  | 2026-09-18T09:00:00Z |
      | sess_cccccccccccccccccccccccc | Write release notes    | 2026-09-17T18:00:00Z |

  Scenario: The keyboard lists the sessions by title and a tap continues the one chosen
    When the user sends "/resume"
    Then the chat is offered a keyboard with the buttons:
      | Fix the login redirect |
      | Login form validation  |
      | Write release notes    |
    When the user taps the button for "Write release notes"
    And the user sends "what is left?"
    Then the agent was prompted in the session "sess_cccccccccccccccccccccccc"

  Scenario: A query naming one session resumes it without the keyboard
    When the user sends "/resume release notes"
    Then the chat received "Resumed: Write release notes"
    When the user sends "continue"
    Then the agent was prompted in the session "sess_cccccccccccccccccccccccc"

  Scenario: The reply names the model and the level the resumed session runs on
    Given the session "sess_cccccccccccccccccccccccc" runs on the model "stub/thinker" with reasoning "high"
    When the user sends "/resume release notes"
    Then the chat received "Model: stub/thinker, reasoning high"

  Scenario: A unique id prefix names a session too
    When the user sends "/resume sess_bbbb"
    And the user sends "continue"
    Then the agent was prompted in the session "sess_bbbbbbbbbbbbbbbbbbbbbbbb"

  Scenario: A query matching several sessions offers the choice
    When the user sends "/resume login"
    Then the chat is offered a keyboard with the buttons:
      | Fix the login redirect |
      | Login form validation  |
    When the user taps the button for "Login form validation"
    And the user sends "continue"
    Then the agent was prompted in the session "sess_bbbbbbbbbbbbbbbbbbbbbbbb"

  Scenario: A query nothing matches is answered
    When the user sends "/resume deploy"
    Then the chat received "No session matches"

  Scenario: The session of the chat is marked on the keyboard
    When the user sends "hello"
    And the user sends "/resume"
    Then the keyboard marks the session behind the chat as the current one

  Scenario: The choice survives a restart of the gateway
    When the user sends "/resume sess_cccc"
    And the gateway is restarted over the same session store
    And the user sends "still here?"
    Then the agent was prompted in the session "sess_cccccccccccccccccccccccc"
