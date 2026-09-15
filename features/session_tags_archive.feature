Feature: Tags and the archive over the stored sessions
  A history that has grown for months is narrowed rather than scrolled. Every session
  carries the tags the title generation proposed, an archived session leaves the working
  list without leaving the disk, and the listing comes back in the order the operator
  asked for. What the archive holds is emptied in one request.

  Background:
    Given a running coddy HTTP server

  Scenario: Tags set on a session come back on its row
    Given 3 stored sessions
    When I tag session 2 with "Backend, API, backend"
    Then session 2 reports tags "backend, api"
    And the other sessions report no tags

  Scenario: Listing only the sessions carrying a tag
    Given 3 stored sessions
    And session 1 is tagged "backend"
    And session 3 is tagged "backend, ui"
    When I list the sessions tagged "backend"
    Then the listing holds sessions 1 and 3

  Scenario: An archived session leaves the working list
    Given 3 stored sessions
    When I archive session 2
    Then the default listing holds sessions 1 and 3
    And the archived listing holds session 2
    And the full listing holds every session

  Scenario: Emptying the archive in one request
    Given 3 stored sessions
    And session 1 is archived
    And session 3 is archived
    When I delete every archived session in one request
    Then the response reports 2 deleted sessions
    And only session 2 is left

  Scenario: Ordering the listing by a column
    Given 3 stored sessions
    And session 1 is titled "Gamma"
    And session 2 is titled "alpha"
    And session 3 is titled "Beta"
    When I list the sessions sorted by "title" ascending
    Then the listing reads "alpha", "Beta", "Gamma"

  Scenario: The title suggestion proposes tags with the title
    Given the title model answers "Refactor the memory API" with tags "backend, memory"
    When I ask for a description of a long request
    Then the description is "Refactor the memory API"
    And the description proposes tags "backend, memory"

  Scenario: Telling the messenger chats apart from the ones started here
    Given 3 stored sessions
    And session 2 was started by the telegram gateway
    When I list the sessions of the gateways
    Then the listing holds session 2
    When I list the sessions started here
    Then the listing holds sessions 1 and 3
