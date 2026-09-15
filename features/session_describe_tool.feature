Feature: The agent files the session it is working in
  A conversation is listed under a title and filed under tags. Both are written
  by the describe call that names a new chat, and both go stale: what started as
  "fix the failing test" is three hours later a rewrite of the session store,
  still carrying the name of its first minute.

  "session_describe" hands that filing to the model. Called with no arguments it
  reports what the session carries now; "title" renames it, "tags" replaces the
  whole label set, and "add_tags" / "remove_tags" change a few and leave the rest
  alone, so a model filing one more topic does not have to know - or guess - the
  labels already there.

  What it writes is what an operator would have written by hand, through the same
  normalization: lower case, inner whitespace as a hyphen, duplicates dropped, at
  most eight labels. The answer reports the stored spelling rather than the asked
  one, so the model can see what actually landed.

  Background:
    Given a stored session titled "fix the failing test" tagged "tests"

  Scenario: The model renames the session it has drifted away from
    When the model calls session_describe with:
      """
      {"title": "Rewrite the session store"}
      """
    Then the tool reports the title "Rewrite the session store"
    And the stored session is titled "Rewrite the session store"
    And the stored session is tagged "tests"

  Scenario: One more label, without losing the ones already there
    When the model calls session_describe with:
      """
      {"add_tags": ["Session Store", "persistence"]}
      """
    Then the tool reports the tags "tests, session-store, persistence"
    And the stored session is tagged "tests, session-store, persistence"

  Scenario: A label that no longer fits is dropped by name
    When the model calls session_describe with:
      """
      {"add_tags": ["persistence"], "remove_tags": ["TESTS"]}
      """
    Then the tool reports the tags "persistence"
    And the stored session is tagged "persistence"

  Scenario: Replacing the whole set files the session from scratch
    When the model calls session_describe with:
      """
      {"tags": ["session store", "http api"]}
      """
    Then the tool reports the tags "session-store, http-api"
    And the stored session is tagged "session-store, http-api"

  Scenario: Reading the filing before changing it
    When the model calls session_describe with:
      """
      {}
      """
    Then the tool reports the title "fix the failing test"
    And the tool reports the tags "tests"
    And the tool reports that nothing changed
    And the stored session is tagged "tests"
