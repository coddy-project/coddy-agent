Feature: Long-term memory runs as a background subagent
  Every user turn of an ordinary session starts a memory subagent: a child agent run in the
  background task pool with its own session bundle, its own transcript and its own task log,
  exactly like a spawn_agent child. The turn waits a bounded time for the child's report. The
  report travels in the turn context block appended after the history - on the first request
  when it is in by then, on a later step otherwise - and never in the system message, so the
  prefix a provider caches the conversation by does not move with it. A report that lands
  after the turn is history in the Tasks drawer. The main model never sees the memory tools.

  Scenario: A user turn starts a memory run the pool and the bundle record
    Given long-term memory is enabled with a wait of 10 seconds
    And a parent agent session in that workspace
    When the user sends "what did we decide about the API?" and the memory child answers "Already on disk: the API returns JSON"
    Then the pool lists an agent task named "memory" for the parent session marked as a system task
    And the memory child session bundle sits inside the parent's and records the parent session id and the name "memory"
    And the memory child was offered exactly the tools "coddy_memory_search, coddy_memory_list, coddy_memory_read, coddy_memory_mkdir, coddy_memory_save, coddy_memory_delete"
    And the memory child's system prompt carries the memory role and no project instructions

  Scenario: A report that arrives within the wait rides in the turn context of the first request
    Given long-term memory is enabled with a wait of 10 seconds
    And a parent agent session in that workspace
    When the user sends "what did we decide about the API?" and the memory child answers "Already on disk: the API returns JSON"
    Then the parent's first request carries "Already on disk: the API returns JSON" in its turn context
    And the parent's first system prompt does not contain "Already on disk"
    And the parent's client received a memory_run update with status "started"
    And the parent's client received a memory_run update with status "finished" and delivered true
    And the memory task log says the report was delivered to the turn

  Scenario: A report that arrives after the wait travels in the turn context of a later step
    Given long-term memory is enabled with a wait of 0 seconds
    And a parent agent session in that workspace
    When the user sends "what did we decide about the API?" and the memory child answers "Already on disk: the API returns JSON" only after the parent's first request
    Then the parent's first system prompt does not contain "Already on disk: the API returns JSON"
    And a later parent request carries "Already on disk: the API returns JSON" in its turn context
    And the parent's client received a memory_run update with status "finished" and delivered true

  Scenario: An ask-mode turn offers the memory child the recall tools only
    Given long-term memory is enabled with a wait of 10 seconds
    And a parent agent session in that workspace in ask mode
    When the user sends "what did we decide about the API?" and the memory child asks to save a note before answering "Already on disk: nothing"
    Then the memory child was offered exactly the tools "coddy_memory_search, coddy_memory_list, coddy_memory_read"
    And the memory child's save was refused
    And no note was written under the global memory root

  Scenario: A persist run writes the note and the transcript shows the tool calls
    Given long-term memory is enabled with a wait of 10 seconds
    And a parent agent session in that workspace
    When the user sends "remember that I prefer pytest" and the memory child saves the note "Prefers pytest" then answers "Saved the preference"
    Then a note under the global memory root contains "Prefers pytest"
    And the memory child transcript records a call of "coddy_memory_save"
    And the memory task log contains "coddy_memory_save"

  Scenario: A persist run keeps a Cyrillic note whole within the character limit
    Given long-term memory is enabled with a wait of 10 seconds
    And a parent agent session in that workspace
    When the user sends "запомни мои предпочтения" and the memory child saves a note of 600 Cyrillic characters then answers "Сохранено"
    Then a note under the global memory root holds those 600 characters whole
    And the memory child's save was answered without a truncation warning

  Scenario: The memory child never writes to the parent's stream
    Given long-term memory is enabled with a wait of 10 seconds
    And a parent agent session in that workspace
    When the user sends "what did we decide about the API?" and the memory child answers "Already on disk: the API returns JSON"
    Then the parent's client received no message chunk containing "Already on disk"
    And the parent's client received memory_run updates and nothing else about memory

  Scenario: A memory model that fails before answering falls back to the next one
    Given long-term memory is enabled with a wait of 10 seconds
    And the memory model is "fake/broken" with the fallback "fake/model"
    And a parent agent session in that workspace
    When the user sends "what did we decide about the API?" and the memory child answers "Already on disk: the API returns JSON"
    Then the memory child ran on the model "fake/model"
    And the parent's first request carries "Already on disk: the API returns JSON" in its turn context

  Scenario: A memory model that fails after streaming text does not jump models
    Given long-term memory is enabled with a wait of 10 seconds
    And the memory model is "fake/partial" with the fallback "fake/model"
    And a parent agent session in that workspace
    When the user sends "what did we decide about the API?" and the memory child answers "Already on disk: the API returns JSON"
    Then the memory task finished as "failed"
    And the fallback model was never called for the memory child
    And the parent's first system prompt does not contain "Already on disk"

  Scenario: The parent model does not see the memory run among its background tasks
    Given long-term memory is enabled with a wait of 0 seconds
    And a parent agent session in that workspace
    When the user sends "what did we decide about the API?" and the memory child waits to be released while the parent lists and waits for its tasks
    Then the parent's background_list result names no memory task
    And the parent's background_wait on the memory task was refused as a system task
    When the memory child is released and answers "(no memory hits)"
    Then the memory task finished as "succeeded"

  Scenario: A Stop of the parent turn during the wait leaves the memory run running
    Given long-term memory is enabled with a wait of 30 seconds
    And a parent agent session in that workspace
    When the user sends "what did we decide about the API?" and the memory child waits to be released
    And the parent turn is cancelled during the memory wait
    Then the parent turn ended as cancelled
    And the memory task is still running
    When the memory child is released and answers "Already on disk: late"
    Then the memory task finished as "succeeded"
    And the memory task log says the turn ended before the report

  Scenario: Finished memory runs beyond keep_runs are removed, the newest kept
    Given long-term memory is enabled with a wait of 10 seconds
    And memory keeps 2 runs
    And a parent agent session in that workspace
    When the user sends "one" and the memory child answers "(no memory hits)"
    And the user sends "two" and the memory child answers "(no memory hits)"
    And the user sends "three" and the memory child answers "(no memory hits)"
    Then the pool lists exactly 2 memory tasks for the parent session
    And exactly 2 memory child bundles exist under the parent's

  Scenario: A memory run does not take a slot of the model's background tasks
    Given long-term memory is enabled with a wait of 10 seconds
    And the background task pool allows 1 task per session
    And a parent agent session in that workspace
    When the user sends "start the build" and the memory child waits to be released while the parent starts a background command
    Then the background command was accepted
    When the memory child is released and answers "(no memory hits)"
    Then the memory task finished as "succeeded"

  Scenario: A memory model whose account is exhausted fails the run inside the wait and the turn goes on
    Given long-term memory is enabled with a wait of 10 seconds
    And every model the memory child could run on answers "402 Payment Required"
    And a parent agent session in that workspace
    When the user sends "what did we decide about the API?" and the memory child answers "Already on disk: the API returns JSON"
    Then the memory task finished as "failed"
    And the parent's first system prompt does not contain "Already on disk"
    And the parent answered the user
    And the parent's client received a memory_run update with status "finished", task status "failed" and a reason naming "402 Payment Required"
    And the memory task record names the error "402 Payment Required"
    And the memory task log contains "402 Payment Required"

  Scenario: The operator's additional prompt reaches the memory child and stays out of the parent's prompt
    Given long-term memory is enabled with a wait of 10 seconds
    And the memory additional prompt is "Only deal with the notes; never answer the task itself." with no cap
    And a parent agent session in that workspace
    When the user sends "what did we decide about the API?" and the memory child answers "(no memory hits)"
    Then the memory child's system prompt carries "Only deal with the notes; never answer the task itself." under the operator instructions
    And the parent's first system prompt does not contain "Only deal with the notes"

  Scenario: The system message does not move between turns that recalled different things
    Given long-term memory is enabled with a wait of 10 seconds
    And a parent agent session in that workspace
    When the user sends "what did we decide about the API?" and the memory child answers "Already on disk: the API returns JSON"
    And the user sends "and what about the database?" and the memory child answers "Already on disk: the database is Postgres"
    Then the parent's system message is the same in both turns
    And no parent system message carries "Already on disk"
    And the first request of the second turn carries "Already on disk: the database is Postgres" in its turn context
    And the first request of the second turn carries "the API returns JSON" nowhere
    And the second turn repeats the first turn's conversation byte for byte before its own message

  Scenario: Every step of a turn carries the report after the history
    Given long-term memory is enabled with a wait of 10 seconds
    And a parent agent session in that workspace
    When the user sends "list the tasks" and the memory child answers "Already on disk: the API returns JSON" while the parent takes two steps
    Then every parent request of that turn carries "Already on disk: the API returns JSON" in its turn context
    And every parent request of that turn carries the same system message

  Scenario: A turn whose memory run delivers nothing does not inherit the previous turn's report
    Given long-term memory is enabled with a wait of 10 seconds
    And a parent agent session in that workspace
    When the user sends "what did we decide about the API?" and the memory child answers "Already on disk: the API returns JSON"
    And every model the memory child could run on answers "402 Payment Required"
    And the user sends "and what about the database?" and the memory child answers "never delivered"
    Then the first request of the second turn carries "the API returns JSON" nowhere

  Scenario: The memory child's search ranks the notes the question is about above a generic one
    Given long-term memory is enabled with a wait of 10 seconds
    And the global memory root holds the note "how-to-write-a-note.md" saying "How to write a note. Open the file, add a heading and save it to the folder."
    And the global memory root holds the note "relay-deployment.md" saying "Relay deployment: build the image and restart the relay service."
    And the project memory root holds the note "runbook.md" saying "Runbook of the relay: deploy it with the release script."
    And a parent agent session in that workspace
    When the user sends "how do we deploy the relay?" and the memory child searches the notes for "how to deploy the relay" then answers "Already on disk: build the image, restart the service"
    Then the memory child's search ranks "global:relay-deployment.md" above "global:how-to-write-a-note.md"
    And the memory child's search ranks "project:runbook.md" above "global:how-to-write-a-note.md"

  Scenario: The AGENTS.md of both memory roots open the memory child's task, the global one first
    Given long-term memory is enabled with a wait of 10 seconds
    And the global memory root holds the note "AGENTS.md" saying "Global guide: read topics/index.md first."
    And the project memory root holds the note "AGENTS.md" saying "Project guide: decisions live in decisions/."
    And a parent agent session in that workspace
    When the user sends "what did we decide about the API?" and the memory child answers "(no memory hits)"
    Then the memory child's task carries "Global guide: read topics/index.md first.", then "Project guide: decisions live in decisions/.", then "what did we decide about the API?"
    And the memory child's task carries "Memory guide (global:AGENTS.md):", then "Memory guide (project:AGENTS.md):", then "User message for this turn:"

  Scenario: A memory root without an AGENTS.md adds nothing to the task
    Given long-term memory is enabled with a wait of 10 seconds
    And the global memory root holds the note "AGENTS.md" saying "Global guide: read topics/index.md first."
    And a parent agent session in that workspace
    When the user sends "what did we decide about the API?" and the memory child answers "(no memory hits)"
    Then the memory child's task carries "Memory guide (global:AGENTS.md):", then "Global guide: read topics/index.md first.", then "what did we decide about the API?"
    And the memory child's task does not carry "Memory guide (project:AGENTS.md):"
