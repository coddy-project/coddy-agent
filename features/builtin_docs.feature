Feature: The documentation built into the binary
  Coddy carries its own documentation: every page of docs/nav.yaml is
  embedded when the binary is built, so what the user reads and what the
  agent looks up is the documentation of the very binary that runs, with
  no request to a site. The agent searches and reads it with two tools, the
  web UI has a reader with a search box, the console opens a help screen on
  F1, and coddy docs prints it on the command line.

  @tools
  Scenario: The agent looks up how a feature works
    When the agent searches its documentation for "Telegram gateway proxy"
    Then a result points at the section "surfaces/gateway#proxy"
    When the agent reads "surfaces/gateway#proxy"
    Then it gets the section "Proxy" of the page "Telegram gateway"

  @tools
  Scenario: The agent reads a long page one part at a time
    When the agent reads "surfaces/web-ui"
    Then it gets the beginning of the page with the list of its sections and the offset to continue at
    When the agent continues reading "surfaces/web-ui" from that offset
    Then it gets the next part of the page

  @tools
  Scenario: The agent asks for the contents first
    When the agent reads its documentation without naming a page
    Then it gets the contents with the page "features/mentions" and its summary

  @http
  Scenario: The web reader lists the documentation, opens a page and searches it
    Given a running coddy serve
    When the browser asks for the documentation contents
    Then the contents list the group "Features" with the page "features/mentions" titled "Mentions"
    When the browser opens the page "features/mentions"
    Then it gets the Markdown of "Mentions" with its sections, the page before it and the page after it
    When the browser searches the documentation for "homebrew"
    Then the results include the page "getting-started/homebrew"

  @cli
  Scenario: coddy docs prints a page and a search from the shell
    When the operator runs "coddy docs show features/mentions#what-the-model-receives"
    Then the output starts with "## What the model receives"
    When the operator runs "coddy docs search homebrew cask"
    Then the output lists "getting-started/homebrew"

  @web
  Scenario: The web UI reads the documentation like a book and asks the agent about a page
    Then the reader shows a page with its contents, its sections and the page before it
    And the reader's search opens a hit at its section
    And asking the agent opens a chat with the page mentioned
    And a coddy: link in any message opens the reader
    And a page mentioned in a sent message opens the reader
    And /docs in the composer opens the reader on a search instead of reaching the agent
    And the chat names a documentation lookup by what it does
