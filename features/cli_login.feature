Feature: Setting up a model provider from the console
  A fresh install has no model provider Coddy can talk to: config.yaml is
  missing, or the example config names a provider whose key is not set. The
  console opens the same provider picker the web UI opens on a first run, with
  the same providers in the same order and NeuralDeep as the row it opens on;
  escape puts it away and the header keeps pointing at /login, which opens it
  again. A provider that signs in (NeuralDeep, OpenCode, Codex, Devin) runs the
  sign-in `coddy providers login` runs in a shell and adds every model the
  account serves, unless this machine has already signed in to it, which Codex
  and Devin do once for every program on the machine: then the picker asks the
  provider for its models instead of running the sign-in again. One that takes
  a key is asked for its endpoint and key, and the model is chosen from what
  the provider itself lists. /login <name> skips the picker. Either way
  config.yaml gains the provider and its models and the console runs on them at
  once, without a restart. (Settings in the web UI keep adding models one by
  one with the plus button next to each.)

  Scenario: A fresh install opens the provider picker
    Given a coddy console app with no model provider configured
    When the console app starts
    Then the screen shows "Set up a model provider"
    And the provider picker lists "NeuralDeep, OpenCode, OpenAI, Anthropic, Codex, Devin, Kimi, OpenAI-compatible, ACP agent" in that order
    When the operator presses escape
    Then the screen shows "Type /login to pick one"
    And the screen does not show "same agent as ACP and HTTP"

  Scenario: The picker opens on NeuralDeep and signs in, adding every model it serves
    Given a stand-in NeuralDeep hub whose tier serves the models "qwen3.8-27b" and "gpt-oss-120b"
    And a coddy console app with no model provider configured
    When the console app starts
    And the operator confirms the highlighted row
    Then the screen shows "NeuralDeep: which endpoint"
    When the operator confirms the highlighted row
    Then the screen shows the hub's verification page and the code "WXYZ-2345"
    And the screen shows "Signed in to NeuralDeep as dev"
    And config.yaml lists the provider "neuraldeep" with the models "neuraldeep/qwen3.8-27b" and "neuraldeep/gpt-oss-120b"
    And the footer shows "(neuraldeep) qwen3.8-27b"
    And the screen no longer shows "Type /login to pick one"
    And the model selector offers "neuraldeep/gpt-oss-120b"

  Scenario: The picker sets up a provider that takes a key from the models it lists
    Given a stand-in OpenAI-compatible server that lists the model "mini-1"
    And a coddy console app with no model provider configured
    When the console app starts
    And the operator picks "OpenAI-compatible" in the provider picker
    Then the screen shows "OpenAI-compatible: API base URL"
    When the operator answers with the stand-in server's address
    Then the screen shows "OpenAI-compatible: API key"
    When the operator answers "sk-stand-in"
    Then the screen shows "OpenAI-compatible: which model"
    When the operator confirms the highlighted row
    Then config.yaml lists the provider "custom" with the model "custom/mini-1"
    And config.yaml names "custom/mini-1" as agent.model
    And the footer shows "(custom) mini-1"

  Scenario: An existing Kimi CLI login reaches model discovery
    Given a synthetic Kimi CLI login and a stand-in discovery proxy
    And a coddy console app with a Kimi provider using the stand-in proxy
    When the console app starts
    And the operator submits the command "/login"
    And the operator picks "Kimi" in the provider picker
    And the operator confirms the highlighted row
    Then the screen shows "Kimi: model"
    And Kimi discovery reached the proxy without changing the CLI credential

  Scenario: The picker offers Kimi platform in China
    Given a coddy console app with no model provider configured
    When the console app starts
    And the operator picks "Kimi" in the provider picker
    Then the screen shows "Kimi: which service"
    When the operator picks "Kimi Platform API key - platform.kimi.com" from the list
    Then the screen shows "Kimi: API key"

  Scenario: The picker connects an agent of the operator's own over ACP
    Given a stand-in ACP agent that lists the model "own-1"
    And a coddy console app with no model provider configured
    When the console app starts
    And the operator picks "ACP agent" in the provider picker
    Then the screen shows "ACP agent: command"
    When the operator answers with the stand-in agent's command
    Then the screen shows "ACP agent: which model"
    When the operator picks "own-1" from the list
    Then config.yaml lists the provider "acp" with the model "acp/own-1"
    And config.yaml gives the provider "acp" no key and no API base
    And config.yaml runs the provider "acp" as a command

  Scenario: The picker goes straight to the models of a Codex this machine already signed in to
    A Codex sign-in is one ChatGPT account for every program on the machine,
    so a console that finds the Codex CLI's auth.json asks Codex what it
    serves instead of putting the person through the device flow again. The
    device flow would have shown a code; this scenario never sees one.

    Given a stand-in Codex backend that lists the model "gpt-5.6-sol" to a machine already signed in
    And a coddy console app with no model provider configured
    When the console app starts
    And the operator picks "Codex" in the provider picker
    Then the screen shows "Codex: which model"
    When the operator confirms the highlighted row
    Then config.yaml lists the provider "codex" with the model "codex/gpt-5.6-sol"
    And config.yaml names "codex/gpt-5.6-sol" as agent.model
    And config.yaml gives the provider "codex" no key and no API base

  Scenario: /login <name> skips the picker and moves the session to the signed-in provider
    install.sh writes config.yaml from the example, whose openai row reads an
    unset OPENAI_API_KEY and whose agent.model runs on that row. The sign-in
    moves agent.model and the session to the provider it signed in.

    Given a stand-in NeuralDeep hub whose tier serves the models "qwen3.8-27b" and "gpt-oss-120b"
    And a coddy console app on the example config with OPENAI_API_KEY unset
    When the console app starts
    And the operator presses escape
    Then the screen shows "Type /login to pick one"
    When the operator submits the command "/login neuraldeep"
    Then the screen shows "This session runs on neuraldeep/qwen3.8-27b now"
    And the footer shows "(neuraldeep) qwen3.8-27b"
    And config.yaml names "neuraldeep/qwen3.8-27b" as agent.model

  Scenario Outline: The slash menu offers /<command>
    Given a coddy console app with no model provider configured
    When the console app starts
    And the operator presses escape
    And the operator types "/<prefix>" without sending it
    Then the slash menu offers "<command>"

    Examples:
      | prefix | command |
      | lo     | login   |
      | ex     | exit    |
