Feature: The agent publishes a finished preview behind the deployment's own edge
  preview_server is local work: a free port on loopback, the files read off the project on every
  request. An installation that runs Coddy behind an authenticated HTTPS edge on another host has
  nowhere to point a reverse proxy at a port that moves every time. The published-preview channel is
  the other half: one static origin on a fixed address, serving copies of finished work below one
  artifact root, and a preview_publish tool that materialises a directory there and hands back the
  address the edge serves it under. The agent's project files are never the origin's root, and
  preview_server keeps its random local port.

  Scenario: A directory is published and answered on the fixed origin
    Given a published-preview origin over an artifact root
    And a project directory with an "index.html" that says "published build"
    When I publish the project directory
    Then the tool reports an address under the configured public URL
    And opening that address returns "published build"

  Scenario: The agent is told to invite the operator to open the address
    Given a published-preview origin over an artifact root
    And a project directory with an "index.html" that says "published build"
    When I publish the project directory
    Then the tool result invites the user to open that address in a browser

  Scenario: The published copy keeps answering after the project changes
    Given a published-preview origin over an artifact root
    And a project directory with an "index.html" that says "first build"
    When I publish the project directory
    And "index.html" is rewritten to say "second build"
    Then opening that address returns "first build"

  Scenario: Publishing under the same name replaces the artifact at the same address
    Given a published-preview origin over an artifact root
    And a project directory with an "index.html" that says "first build"
    When I publish the project directory as "site"
    And "index.html" is rewritten to say "second build"
    And I publish the project directory as "site"
    Then the tool reports the same address as before
    And opening that address returns "second build"

  Scenario: Two sessions publishing the same name get addresses of their own
    Given a published-preview origin over an artifact root
    And a project directory with an "index.html" that says "published build"
    When I publish the project directory as "site"
    And another session publishes its own directory as "site"
    Then the two addresses differ
    And each address returns the page of the session that published it

  Scenario: Dot-files of the project are never materialised
    Given a published-preview origin over an artifact root
    And a project directory with an "index.html" that says "published build"
    And the project directory holds a ".env" with "SECRET=1"
    When I publish the project directory
    Then the artifact holds no ".env"
    And asking that address for ".env" answers 404

  Scenario: Unpublishing takes the artifact down
    Given a published-preview origin over an artifact root
    And a project directory with an "index.html" that says "published build"
    When I publish the project directory as "site"
    And I unpublish "site"
    Then that address answers 404

  Scenario: An expired artifact stops answering
    Given a published-preview origin over an artifact root
    And a project directory with an "index.html" that says "published build"
    When I publish the project directory for 1 second
    And that artifact's lifetime elapses
    Then that address answers 404

  Scenario: The origin never lists what it holds
    Given a published-preview origin over an artifact root
    And a project directory with an "index.html" that says "published build"
    When I publish the project directory
    Then asking the origin for its root answers 404
