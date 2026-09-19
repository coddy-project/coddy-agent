Feature: Choosing the summarizer in the web UI
  The summarizer of a compaction is one of the configured models, so the web UI
  offers that list instead of a field to type a model id into from memory:
  Settings offers it for compaction.model, and the composer completes the
  --model option of /compact from it.

  Scenario: Settings offers the configured models for the summarizer
    Then picking a configured model as the summarizer in Settings sets compaction.model and keeps the other compaction settings

  Scenario: The composer completes the model of /compact --model
    Then after "/compact --model" the composer lists the configured models and narrows them as the id is typed
    And Enter puts the highlighted model into the draft instead of sending the command
    And two dashes after /compact offer the --model option, which opens the models
