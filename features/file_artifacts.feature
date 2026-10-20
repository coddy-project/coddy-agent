Feature: Share a file as a session artifact
  A deterministic session-owned artifact is downloadable only from the session
  that published it. Its card opens the file it was copied from in the Files
  window, so the listing says whether that file is still in the chat's
  workspace.

  Scenario: A shared report is downloadable
    Given a deterministic session has shared "report.txt"
    When the client downloads the shared artifact
    Then the artifact download contains "report bytes"

  Scenario: A shared report can be revealed only through its artifact id
    Given a deterministic session has shared "report.txt"
    When the client asks the server to reveal the shared artifact
    Then the server asks the desktop to reveal the shared report

  Scenario: The listing says the source of a shared report is in the workspace
    Given a deterministic session has shared "report.txt"
    When the client reads the session's messages
    Then the shared report's source is "present"

  Scenario: The listing says the source of a shared report was deleted
    Given a deterministic session has shared "report.txt"
    And the source of the shared report is deleted from the workspace
    When the client reads the session's messages
    Then the shared report's source is "deleted"
    And the client downloads the shared artifact
    And the artifact download contains "report bytes"
