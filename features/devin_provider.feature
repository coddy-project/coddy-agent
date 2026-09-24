Feature: A Devin account serves Coddy's own agent
  A "devin" provider reaches the models of a Devin (Cognition) account the way
  the Devin CLI does: a browser sign-in with PKCE yields a session token, the
  session token is exchanged for a short-lived user JWT, and chat runs over the
  GetChatMessage stream of the Devin API server. Devin is only the backend:
  Coddy keeps its own system prompt, its own tools and its own ReAct loop. A
  stand-in API server plays Devin, so no account and no network are needed.

  @cli
  Scenario: Browser sign-in stores the session token and publishes the catalog
    Given a Devin stand serving the Claude Opus 5 and SWE-1.6 families
    And a fresh CODDY_HOME with an empty config and no Devin CLI login
    When I run "providers login devin" and the browser completes the sign-in
    Then the Devin session token is stored with owner-only permissions
    And config.yaml lists provider "devin" of type "devin"
    And config.yaml lists model "devin/claude-opus-5" with reasoning levels "low,medium,high" and default "medium"
    And config.yaml lists model "devin/swe-1.6" with no reasoning levels
    And agent.model is "devin/claude-opus-5"
    And "providers list" reports provider "devin" signed in as "dev@example.com"

  @cli
  Scenario: The Devin CLI login is used without a browser
    Given a Devin stand serving the Claude Opus 5 and SWE-1.6 families
    And a fresh CODDY_HOME with an empty config and no Devin CLI login
    And a Devin CLI login holding the stand's session token
    When I run "providers login devin --devin-cli"
    Then no Coddy-managed Devin credential is written
    And config.yaml lists provider "devin" of type "devin"
    And "providers list" reports provider "devin" using the Devin CLI login

  @acp
  Scenario: A turn on a Devin family runs Coddy's own tools through GetChatMessage
    Given a Devin stand whose model reads a workspace file with coddy's tool and then quotes it
    And an ACP session manager with a devin provider signed in through Coddy
    When I run an agent prompt on "devin/claude-opus-5" at reasoning level "high"
    Then the Devin stand received the model uid "claude-opus-5-high" from a Devin Desktop client
    And the chat request carried coddy's own tools and system prompt
    And the second chat request replayed the tool call, its result and the signed reasoning
    And the final assistant message quotes the workspace file

  @acp
  Scenario: A tool result that is not valid UTF-8 still reaches the model
    The Devin API server rejects a request whose protobuf strings are not
    valid UTF-8 with an opaque invalid_argument, so bytes a tool returns as
    they are - a grep match or a command's output from a file in a legacy
    encoding - must not end the turn.
    Given a Devin stand whose model reads a workspace file with coddy's tool and then quotes it
    And the workspace file is Latin-1 text
    And the model searches it with coddy's grep tool, which returns the matching line byte for byte
    And an ACP session manager with a devin provider signed in through Coddy
    When I run an agent prompt on "devin/claude-opus-5" at reasoning level "high"
    Then the second chat request carried the tool result as valid UTF-8
    And the final assistant message quotes the workspace file
