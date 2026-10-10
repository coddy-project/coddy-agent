Feature: The server reports the room left on the disk that stores the sessions
  GET /coddy/info carries a storage object: the state against
  sessions.min_free_mb ("ok", "low" under the threshold, "full" once a save has
  failed for want of room), the figures of the disk that decides it and the
  threshold. The web UI warns from it before a save fails. When one does, the
  server tells every client on the events stream (event: storage_status) at
  once, because the notice cannot be written into the transcript the failed
  save was trying to keep (issue #465).

  Scenario: A disk with room is reported ok with its figures
    Given a running coddy HTTP server whose warning threshold is 1 MB
    When a client reads /coddy/info
    Then the storage state is "ok"
    And the answer carries the free and the total bytes of the disk
    And the answer carries the threshold of 1 MB

  Scenario: A disk under the threshold is reported low
    Given a running coddy HTTP server whose warning threshold is more than the disk holds
    When a client reads /coddy/info
    Then the storage state is "low"

  Scenario: A full disk is announced to every client at once
    Given a running coddy HTTP server whose warning threshold is 1 MB
    And a client is listening to the events stream
    And the volume that holds the sessions folder has no room left
    When a client posts a first message to /v1/responses without a session
    Then the answer is 507 Insufficient Storage
    And the events stream announces that saves are failing
