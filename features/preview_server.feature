Feature: The agent serves project files on a local web server
  HTML, JS and CSS work often cannot be opened straight from disk: a page loaded as file://
  cannot import ES modules, fetch its own data or start a worker. When the operator says "run
  this on a web server, I want to try it myself", the agent starts a static file server over
  the project directory on a free localhost port, hands back the address and invites the
  operator to open it. The server is a background task of the session: it shows among the
  running tasks and stays up until it is stopped or the session is deleted, unless the call that
  started it named a timeout.

  Scenario: The project is served on a free localhost port
    Given a project directory with an "index.html" that says "hello from the preview"
    When I start the preview server
    Then the tool reports a localhost URL on a free port
    And opening that URL returns "hello from the preview"

  Scenario: The agent is told to invite the operator to open the address
    Given a project directory with an "index.html" that says "hello from the preview"
    When I start the preview server
    Then the tool result invites the user to open that URL in a browser

  Scenario: The server is one of the session's running tasks
    Given a project directory with an "index.html" that says "hello from the preview"
    When I start the preview server
    And I list the background tasks
    Then the listing shows a running server task with that URL

  Scenario: An edited file is served fresh
    Given a project directory with an "index.html" that says "first version"
    When I start the preview server
    And "index.html" is rewritten to say "second version"
    Then opening that URL returns "second version"

  Scenario: Asking again for the same directory returns the same address
    Given a project directory with an "index.html" that says "hello from the preview"
    When I start the preview server
    And I start the preview server again
    Then the tool reports the same URL as before
    And the session has one running server task

  Scenario: Stopping the task shuts the server down
    Given a project directory with an "index.html" that says "hello from the preview"
    When I start the preview server
    And I stop that background task
    Then the task is stopped
    And that URL no longer answers

  Scenario: A server started with a timeout ends when it elapses
    Given a project directory with an "index.html" that says "hello from the preview"
    When I start the preview server with a timeout of 1 second
    And I wait for that background task to finish
    Then the task is timed out
    And that URL no longer answers

  Scenario: Deleting the session shuts its server down
    Given a project directory with an "index.html" that says "hello from the preview"
    When I start the preview server
    And the session is deleted
    Then that URL no longer answers
