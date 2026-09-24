Feature: Several profiles of one provider type
  An operator keeps several provider rows of one type - three ChatGPT accounts
  as codex rows, two NeuralDeep accounts - and each row is a profile of its
  own: its own sign-in, its own models, its own usage. The machine-wide Codex
  CLI login is one account, so it stands in for one row only: the only row of
  its type, or the row named "codex" when there are several. Every other row
  signs in itself instead of quietly running on that account.

  @http
  Scenario: Codex profiles sign in separately and do not share the Codex CLI login
    Given a coddy HTTP server with codex rows "codex", "codex-work" and "codex-home" and a Codex CLI login
    Then the row "codex" is signed in through the Codex CLI login
    And the row "codex-home" is not signed in and names "codex" as the row the Codex CLI login serves
    And the row "codex-home" lists no models without reaching the Codex backend
    When I sign in the row "codex-work" through the device flow over REST
    Then the row "codex-work" is signed in with an account of its own
    And the row "codex-home" is still not signed in

  @http
  Scenario: NeuralDeep profiles hold keys of their own, labelled by row
    Given a coddy HTTP server with neuraldeep rows "neuraldeep" and "nd-tech" and a stand-in hub
    When I sign in the row "neuraldeep" through the NeuralDeep device flow over REST
    And I sign in the row "nd-tech" through the NeuralDeep device flow over REST
    Then each neuraldeep row lists its models with the key the hub minted for it
    And the hub labelled the key of "nd-tech" with the row name
