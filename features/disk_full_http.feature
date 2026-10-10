Feature: A full disk is named in the answer to a new chat
  When the volume that holds the sessions folder has no room left, a request that
  needs a new session is answered 507 Insufficient Storage with the cause in
  the error message, instead of 500 "session unavailable". The web UI shows
  the server's message beside the status, so the person sees why the chat did
  not start. A new chat that picked a folder creates its session through the
  workspace route before its first message, so that route says the same
  (issue #465).

  Scenario Outline: A first message on a full disk is told the disk is full
    Given a running coddy HTTP server
    And the volume that holds the sessions folder has no room left
    When a client posts a first message to <endpoint> without a session
    Then the answer is 507 Insufficient Storage
    And the error message says there is no space left on the device

    Examples:
      | endpoint             |
      | /v1/responses        |
      | /v1/chat/completions |

  Scenario: Picking the folder of a new chat on a full disk is told the disk is full
    Given a running coddy HTTP server
    And the volume that holds the sessions folder has no room left
    When a client picks a folder for a new chat
    Then the answer is 507 Insufficient Storage
    And the error message says there is no space left on the device
