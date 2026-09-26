Feature: Session settings from the dialogue
  The model, the reasoning level, the operating mode and the permission mode
  of a session change from the conversation itself: a settings command at the
  start of a message changes them for the session or, with --once or
  --count=N, for that many turns; a prompt that is only commands runs no turn.
  The permission dialog can switch the session to bypass for the rest of the
  session (#292). Every change is published as a versioned snapshot, so every
  browser tab mirrors it. A model switched while a turn runs answers from the
  turn's next request, and the answer in flight keeps the name of the model
  that wrote it (#362).

  Background:
    Given a running coddy server with the models "fake/a" and "fake/b"
    And a chat session

  Scenario: A settings command alone changes the session without a turn
    Given a browser watches the server events
    When the user sends "/model fake/b"
    Then the answer says "Model: fake/b for this session"
    And the session model is "fake/b"
    And no model was called
    And the browser was told the session model is "fake/b"

  Scenario: A command with --once runs one turn on another model
    When the user sends "/model fake/b --once hello"
    And the user sends "and again"
    Then the model "b" answered 1 request
    And the model "a" answered 1 request
    And the session model is "fake/a"

  Scenario: The permission dialog switches the session to bypass
    Given the server asks before running commands
    And the model runs two commands in one step
    When the user sends "run both" and answers the first prompt with "allow_session_bypass"
    Then 1 permission prompt was shown
    And the session permission mode is "bypass"
    And both commands ran

  Scenario: A session switched to ask is asked on a server configured for bypass
    Given the server runs commands without asking
    And the model runs one command
    And the session permission mode is switched to "ask" over the API
    When the user sends "run it" and answers the first prompt with "allow"
    Then 1 permission prompt was shown

  Scenario: A model switched during a turn answers the turn's next step
    Given the browser switches the session to "fake/b" while the model "a" answers
    When the user sends "go on"
    Then the model "a" answered 1 request
    And the model "b" answered 1 request
    And the transcript signs the answers "fake/a, fake/b"
