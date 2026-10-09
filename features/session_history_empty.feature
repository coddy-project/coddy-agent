Feature: A conversation nobody wrote in stays out of History
  A session is opened before its first prompt: a console starts with one, an
  editor opens a thread, a web chat asks for its folder before it sends. One
  that is left without a prompt holds nothing to come back to, so the listings
  people read leave it out until its first message is saved, and a bulk delete
  of the whole history still removes it.

  Background:
    Given a running coddy HTTP server

  Scenario: A session left without a prompt is not listed
    Given 2 stored sessions
    And a session that was opened and left without a prompt
    When I list the sessions
    Then the listing holds sessions 1 and 2
    And the listing leaves out the session left without a prompt

  Scenario: The first prompt brings the session into History
    Given a session that was opened and left without a prompt
    When that session's first prompt is saved
    And I list the sessions
    Then the listing holds the session that was left without a prompt

  Scenario: Deleting the whole history removes a session nobody wrote in
    Given 2 stored sessions
    And a session that was opened and left without a prompt
    When I delete every session in one request
    Then no session bundle is left on disk
