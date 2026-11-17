Feature: Reviewing and discarding the edits of a session's working copy
  The Edits view of a chat reads what git reports for the session's folder:
  every tracked file that differs from HEAD and every new file git does not
  ignore, whoever made the change. Nothing is recorded per turn. The view can
  discard the uncommitted changes, one file at a time or all of them.

  Background:
    Given a running coddy HTTP server whose workspace is a git repository
    And the repository has "notes.txt" committed as "one\ntwo\nthree\n"

  Scenario: A clean working copy has nothing to show
    When I ask what the working copy changed
    Then no files are reported as changed
    And git is named as the version control

  Scenario: A changed file carries a fingerprint of its content
    When the agent runs a turn that writes "notes.txt" as "one\nTWO\nthree\n"
    And I ask what the working copy changed
    Then "notes.txt" is reported with a content digest

  Scenario: An image file is marked and its raw sides are served
    Given the repository has "pic.png" committed as a PNG image
    When "pic.png" is rewritten on disk with another PNG image
    And I ask what the working copy changed
    Then "pic.png" is reported as an image with its raw sizes
    And the raw "before" side of "pic.png" is served as "image/png"
    And the raw "after" side of "pic.png" is served as "image/png"

  Scenario: An edited file is reported with its line counts
    When the agent runs a turn that writes "notes.txt" as "one\nTWO\nthree\n"
    And I ask what the working copy changed
    Then 1 file is reported as changed with 1 addition and 1 deletion
    And "notes.txt" is reported as "modified"

  Scenario: A new file git does not ignore is an addition
    When the agent runs a turn that writes "extra.txt" as "fresh\n"
    And I ask what the working copy changed
    Then "extra.txt" is reported as "added"

  Scenario: An edit made outside the agent is an edit too
    When "notes.txt" is changed on disk to "ONE\ntwo\nthree\n"
    And I ask what the working copy changed
    Then "notes.txt" is reported as "modified"

  Scenario: The viewer reads the diff of one file
    When the agent runs a turn that writes "notes.txt" as "one\nTWO\nthree\n"
    And I open the diff for "notes.txt"
    Then the diff removes "two" and adds "TWO"

  Scenario: A line replaced by two reads as removed, then added
    When the agent runs a turn that writes "notes.txt" as "one\nTWO\nTOO\nthree\n"
    And I open the diff for "notes.txt"
    Then the diff removes "two" before it adds "TWO" and "TOO"

  Scenario: Discarding one file puts it back at HEAD
    When the agent runs a turn that writes "notes.txt" as "one\nTWO\nthree\n"
    And the agent runs a turn that writes "extra.txt" as "fresh\n"
    And I discard the changes of "notes.txt"
    Then "notes.txt" contains "one\ntwo\nthree\n"
    When I ask what the working copy changed
    Then 1 file is reported as changed with 1 addition and 0 deletions
    And "extra.txt" is reported as "added"

  Scenario: Discarding everything returns the working copy to HEAD
    Given a client listening for server events
    When the agent runs a turn that writes "notes.txt" as "one\nTWO\nthree\n"
    And the agent runs a turn that writes "extra.txt" as "fresh\n"
    And I discard every change
    Then the client hears that the session's working copy changed
    And "notes.txt" contains "one\ntwo\nthree\n"
    And "extra.txt" no longer exists
    When I ask what the working copy changed
    Then no files are reported as changed

  Scenario: Deleting a new file makes it go away
    Given a client listening for server events
    When the agent runs a turn that writes "extra.txt" as "fresh\n"
    And I delete the changes of "extra.txt"
    Then the client hears that the session's working copy changed
    And "extra.txt" no longer exists
    When I ask what the working copy changed
    Then no files are reported as changed

  Scenario: Deleting an edited tracked file leaves a deletion
    When the agent runs a turn that writes "notes.txt" as "one\nTWO\nthree\n"
    And I delete the changes of "notes.txt"
    Then "notes.txt" no longer exists
    When I ask what the working copy changed
    Then "notes.txt" is reported as "deleted"
