Feature: Share a file as a session artifact
  A deterministic session-owned artifact is downloadable only from the session
  that published it.

  Scenario: A shared report is downloadable
    Given a deterministic session has shared "report.txt"
    When the client downloads the shared artifact
    Then the artifact download contains "report bytes"

  Scenario: A shared report can be revealed only through its artifact id
    Given a deterministic session has shared "report.txt"
    When the client asks the server to reveal the shared artifact
    Then the artifact reveal is accepted or reports that this server cannot reveal files
