Feature: Long-term memory runs as a subagent the model calls
  Long-term memory is a bundled subagent definition named "memory". The model
  delegates to it through spawn_agent like to any other subagent: the run is an
  agent task in the background task pool with its own session bundle nested in
  the parent's, its own transcript and its own task log, and the report comes
  back as the spawn_agent tool result. The memory child runs its own tools -
  the six coddy_memory_* names, the recall three in an ask-mode turn - on the
  model memory.model names, with the fallback chain of memory.fallback_models,
  and its system prompt is the memory role with every memory root's guide
  quoted in the task ahead of the user message. Nothing runs in the background
  of its own accord, the parent's system prompt never mentions memory, and
  with memory.enable off the definition is not in the catalog and the spawn is
  refused. The main model never sees the memory tools.

  Scenario: A memory spawn runs the bundled memory child as an ordinary agent task
    Given long-term memory is enabled
    And a parent agent session in that workspace
    When the model delegates "what did we decide about the API?" to the memory subagent and it answers "Already on disk: the API returns JSON"
    Then the pool lists an agent task named "memory" for the parent session
    And the memory task is not marked as a system task
    And the memory child session bundle sits inside the parent's and records the parent session id and the name "memory"
    And the memory child was offered exactly the tools "coddy_memory_search, coddy_memory_list, coddy_memory_read, coddy_memory_mkdir, coddy_memory_save, coddy_memory_delete"
    And the memory child's system prompt carries the memory role and no project instructions
    And the parent's client received no message chunk containing "memory"

  Scenario: A memory spawn in a plan-mode turn offers the recall tools only
    Given long-term memory is enabled
    And a parent agent session in that workspace
    When the memory subagent is spawned in plan mode and it asks to save a note before answering "Already on disk: nothing"
    Then the memory child was offered exactly the tools "coddy_memory_search, coddy_memory_list, coddy_memory_read"
    And the memory child's save was refused
    And no note was written under the global memory root

  Scenario: A persist spawn writes the note and the transcript shows the tool calls
    Given long-term memory is enabled
    And a parent agent session in that workspace
    When the model delegates "remember that I prefer pytest" to the memory subagent and it saves the note "Prefers pytest" then answers "Saved the preference"
    Then a note under the global memory root contains "Prefers pytest"
    And the memory child transcript records a call of "coddy_memory_save"
    And the memory task log contains "coddy_memory_save"

  Scenario: A persist spawn keeps a Cyrillic note whole within the character limit
    Given long-term memory is enabled
    And a parent agent session in that workspace
    When the model delegates "запомни мои предпочтения" to the memory subagent and it saves a note of 600 Cyrillic characters then answers "Сохранено"
    Then a note under the global memory root holds those 600 characters whole
    And the memory child's save was answered without a truncation warning

  Scenario: The memory child's task carries the guides of both roots ahead of the user message
    Given long-term memory is enabled
    And a parent agent session in that workspace
    And the global memory root holds the note "AGENTS.md" saying "Global guide: notes live under topics/"
    And the project memory root holds the note "AGENTS.md" saying "Project guide: decisions live in decisions/"
    When the model delegates "what did we decide about the API?" to the memory subagent and it answers "Already on disk: nothing"
    Then the memory child's task carries "Global guide", then "Project guide", then "what did we decide about the API?"

  Scenario: A memory model that fails before answering falls back to the next one
    Given long-term memory is enabled
    And the memory model is "fake/broken" with the fallback "fake/model"
    And a parent agent session in that workspace
    When the model delegates "what did we decide about the API?" to the memory subagent and it answers "Already on disk: the API returns JSON"
    Then the memory child ran on the model "fake/model"

  Scenario: A memory model that fails after streaming text does not jump models
    Given long-term memory is enabled
    And the memory model is "fake/partial" with the fallback "fake/model"
    And a parent agent session in that workspace
    When the model delegates "what did we decide about the API?" to the memory subagent and the parent still answers the user
    Then the memory task finished as "failed"
    And the fallback model was never called for the memory child
    And the parent answered the user

  Scenario: A memory spawn on a broken chain fails the task, not the turn
    Given long-term memory is enabled
    And every model the memory child could run on answers "broken"
    And a parent agent session in that workspace
    When the model delegates "what did we decide about the API?" to the memory subagent and the parent still answers the user
    Then the memory task finished as "failed"
    And the memory task record names the error "broken"
    And the parent answered the user

  Scenario: A memory spawn while memory is disabled is refused
    Given long-term memory is disabled
    And a parent agent session in that workspace
    When the model delegates "what did we decide about the API?" to the memory subagent and the parent still answers the user
    Then the pool lists exactly 0 memory tasks for the parent session
    And the parent's spawn_agent call was refused naming "unknown subagent"
    And the parent answered the user

  Scenario: The memory child's task names the operator instructions the role asks for
    Given long-term memory is enabled
    And the memory additional prompt is "Prefer short answers." with no cap
    And a parent agent session in that workspace
    When the model delegates "what did we decide about the API?" to the memory subagent and it answers "Already on disk: nothing"
    Then the memory child's system prompt carries "Prefer short answers." under the operator instructions

  Scenario: The memory child's search ranks the notes the question is about above a generic one
    Given long-term memory is enabled
    And the global memory root holds the note "how-to-write-a-note.md" saying "How to write a note. Open the file, add a heading and save it to the folder."
    And the global memory root holds the note "relay-deployment.md" saying "Relay deployment: build the image and restart the relay service."
    And the project memory root holds the note "runbook.md" saying "Runbook of the relay: deploy it with the release script."
    And a parent agent session in that workspace
    When the model delegates "how do we deploy the relay?" to the memory subagent and it searches the notes for "how to deploy the relay" then answers "Already on disk: build the image, restart the service"
    Then the memory child's search ranks "global:relay-deployment.md" above "global:how-to-write-a-note.md"
    And the memory child's search ranks "project:runbook.md" above "global:how-to-write-a-note.md"
