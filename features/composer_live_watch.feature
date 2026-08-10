Feature: Watching a composer turn from another client
  A turn belongs to the session, not to the request that started it. Whatever shape the
  caller asked its own answer to take, every agent turn is published to the session's
  composer relay, so any number of other clients - a browser tab, a second IDE panel -
  can attach to `GET /coddy/sessions/{id}/composer-stream` and watch it happen.

  Background:
    Given a running coddy composer server
    And an agent session

  Scenario: A browser watches a turn a script started without streaming
    Given a script starts an agent turn with "stream" set to false
    When a second client subscribes to the composer stream of that session
    Then the watching client receives the assistant text of the turn
    And the watching client receives the terminating done frame
    And the script still receives its plain JSON response

  Scenario: A client that finds no turn running is told at once
    When a second client subscribes to the composer stream of that session
    Then the watching client is told there is no active composer stream
