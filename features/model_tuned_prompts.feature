Feature: The system prompt is tuned to the session's model
  Models read the same instructions differently: Gemma 4 served by NeuralDeep
  loses a second tool call of the same message to plain text, gpt-oss follows its
  own channel rules, Claude batches independent reads. Coddy therefore resolves
  the model of every turn to a family and a model slug and assembles the system
  prompt from the shared sections plus the guidance written for that model. An
  operator can bring model-specific files of their own under prompts.dir, and
  switches the whole mechanism off with prompts.per_provider.enable.

  Scenario: A Gemma model receives the Gemma guidance
    Given a session on the model "neuraldeep/gemma-4-31b-noreason" served by a "neuraldeep" provider
    When the agent sends a turn to the model
    Then the system prompt of that request carries the "Gemma" model-family notes
    And the system prompt names Coddy exactly once

  Scenario: A gpt-oss model receives its family notes and its size profile
    Given a session on the model "neuraldeep/gpt-oss-120b" served by a "neuraldeep" provider
    When the agent sends a turn to the model
    Then the system prompt of that request carries the "Harmony-native gpt-oss guidance" model-family notes
    And the system prompt of that request contains "### gpt-oss-120b profile"

  Scenario: Model-tuned prompts switched off
    Given a session on the model "neuraldeep/gemma-4-31b-noreason" served by a "neuraldeep" provider
    And prompts.per_provider.enable is false
    When the agent sends a turn to the model
    Then the system prompt of that request carries no model-family notes

  Scenario: A model-specific file under prompts.dir wins over the family file
    Given a session on the model "neuraldeep/gemma-4-31b" served by a "neuraldeep" provider
    And prompts.dir holds "agent.md", "agent.gemma.md" and "agent.neuraldeep-gemma-4-31b.md"
    When the agent sends a turn to the model
    Then the system prompt of that request is built from "agent.neuraldeep-gemma-4-31b.md"
    And the system prompt names Coddy exactly once
