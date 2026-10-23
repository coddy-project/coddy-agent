Feature: Signing in to a model provider from the console
  A fresh install has no model provider Coddy can talk to: config.yaml is
  missing, or the example config names a provider whose key is not set. The
  console says so in its header and offers /login, which signs this machine in
  without leaving the console - the same sign-in `coddy providers login` runs
  in a shell. A bare /login signs in to NeuralDeep; /login codex, /login devin
  and /login opencode sign in to the others, and /login <name> to a provider
  config.yaml already lists. The sign-in adds the provider and every model it
  serves to config.yaml, and the console runs on them at once, without a
  restart. (Settings in the web UI keep adding models one by one with the plus
  button next to each.)

  Scenario: A fresh install points at /login
    Given a coddy console app with no model provider configured
    When the console app starts
    Then the screen shows "Type /login to sign in to NeuralDeep"
    And the screen does not show "same agent as ACP and HTTP"

  Scenario: A bare /login signs in to NeuralDeep and adds every model it serves
    Given a stand-in NeuralDeep hub whose tier serves the models "qwen3.8-27b" and "gpt-oss-120b"
    And a coddy console app with no model provider configured
    When the console app starts
    And the operator submits the command "/login"
    Then the screen shows the hub's verification page and the code "WXYZ-2345"
    And the screen shows "Signed in to NeuralDeep as dev"
    And config.yaml lists the provider "neuraldeep" with the models "neuraldeep/qwen3.8-27b" and "neuraldeep/gpt-oss-120b"
    And the footer shows "neuraldeep/qwen3.8-27b"
    And the screen no longer shows "Type /login to sign in to NeuralDeep"
    And the model selector offers "neuraldeep/gpt-oss-120b"

  Scenario Outline: The slash menu offers /<command>
    Given a coddy console app with no model provider configured
    When the console app starts
    And the operator types "/<prefix>" without sending it
    Then the slash menu offers "<command>"

    Examples:
      | prefix | command |
      | lo     | login   |
      | ex     | exit    |
