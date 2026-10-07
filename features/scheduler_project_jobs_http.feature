Feature: The HTTP surface lists, creates and approves project scheduler jobs
  The web UI asks for the jobs of the session's workspace with the session header: the
  user jobs, then the project jobs, each with its scope, its workspace and its trust. A
  project job created from a session is approved at once; one that came with the
  checkout is shown with its raw file and digest, refused on a run, and approved by
  posting the digest the operator read.

  Scenario: A project job created from a session is trusted and listed for that session only
    Given a running coddy serve server with the scheduler and a session in a workspace
    When I POST the project job "lint" with the session header
    Then the API answers 201
    And the jobs listed for the session show the project job "lint" as "trusted" in the session's workspace
    And the jobs listed without a session do not show "lint"

  Scenario: A project job that came with the checkout is approved with the digest it was shown
    Given a running coddy serve server with the scheduler and a session in a workspace
    And a project job "lint" committed to the session's workspace
    When I GET the project job "lint" with the session header
    Then it answers its raw file, its digest and the trust "needs_approval"
    When I POST a run of the project job "lint" with the session header
    Then the API answers 409
    When I approve the project job "lint" with the digest it showed
    Then the API answers 200
    When I POST a run of the project job "lint" with the session header
    Then the API answers 202

  Scenario: A project job may not take the id of a user job
    Given a running coddy serve server with the scheduler and a job "nightly"
    And a session in a workspace
    When I POST the project job "nightly" with the session header
    Then the API answers 409
