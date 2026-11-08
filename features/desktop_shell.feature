Feature: The tab shell of the desktop window
  The desktop window opens on a page the server answers at /desktop/: a tab
  strip over one iframe per chat, each iframe the web UI of that same server.
  The page is served where the web UI itself is served, under the catch-all
  that the authentication gate leaves open, so a browser that has not signed in
  yet reaches the shell and the sign-in form inside it rather than a bare 401.
  The window itself asks for no credential: it binds loopback for the person
  who started the application, and the configuration on disk is left as it is,
  so the same file still signs in a `coddy serve` that is reachable. Only the
  process that opened a window answers the page at all: the shell's own routes
  sit outside the gate with it, and a server nobody opened a window on has no
  reason to publish them.

  Scenario: A server that opened no window does not answer the shell
    Given a Coddy whose web UI asks for a sign-in
    But no window was opened on it
    When a browser with no credential opens the tab shell
    Then no tab shell is served there
    And the window actions of the shell answer nothing

  Scenario: The shell is reachable before anyone has signed in
    Given a Coddy whose web UI asks for a sign-in
    And a window of this machine was opened on it
    When a browser with no credential opens the tab shell
    Then the shell answers with its tab strip
    And it tells a launcher that a Coddy desktop is serving here

  Scenario: A tab holds the web UI of the same server
    Given a Coddy whose web UI asks for a sign-in
    And a window of this machine was opened on it
    When a browser with no credential opens the tab shell
    Then the shell frames the web UI of this server
    And the session list it draws its titles from is still behind the gate

  Scenario: The window opens on the chat, not on a sign-in form
    Given a Coddy whose web UI asks for a sign-in
    And a window of this machine was opened on it
    And the desktop serves it for a window of its own
    When a browser with no credential opens the tab shell
    Then the shell answers with its tab strip
    And the session list behind it answers too
    And the sign-in account in the configuration is still there
