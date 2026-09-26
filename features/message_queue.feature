Feature: A follow-up written while the agent is working
  Watching an agent work is the moment an operator knows most about what it should do next,
  and until now that was the one moment they could not say anything: the composer turn lock
  refuses a second prompt, so the correction had to wait for the answer or cost a Stop.

  A message written during a turn is therefore queued on the session instead of refused, and
  the running turn reads it at its next step - between the tool calls it just made and the
  request that follows them - so the correction lands while the work is still happening
  rather than after it is finished. Until the agent reads it, a queued message is still the
  operator's: it can be taken back.

  Scenario: A message queued mid-turn is read at the agent's next step
    Given an agent turn that calls a tool before it answers
    When the operator queues "check the Windows path too" while the tool is running
    Then the agent reads that message on its next step
    And the request that follows the tool result carries that message
    And the transcript records it as a message of the operator

  Scenario: Queued messages are read in the order they were written
    Given an agent turn that calls a tool before it answers
    When the operator queues "first correction" while the tool is running
    And the operator queues "second correction" while the tool is running
    Then the agent reads both messages on its next step
    And they are read in the order they were written

  Scenario: A queued message can be taken back before the agent reads it
    Given an agent turn that calls a tool before it answers
    When the operator queues "wrong file, ignore that" while the tool is running
    And the operator cancels that queued message before the step ends
    Then the agent never reads it
    And the transcript has no message of the operator besides the prompt

  Scenario: A message queued as the turn is ending is still answered
    Given an agent turn that answers without calling a tool
    When the operator queues "one more thing" before the turn releases
    Then the agent answers that message in the same turn

  Scenario: The queue is empty again once the turn is over
    Given an agent turn that calls a tool before it answers
    When the operator queues "check the Windows path too" while the tool is running
    And the turn finishes
    Then the session holds no queued messages

  Scenario: A queued image reaches the next model request and the transcript
    Given an agent turn that calls a tool before it answers
    When the operator queues an image with "inspect this image" while the tool is running
    Then the agent reads that image on its next step
    And the next model request carries the image
    And the transcript records the image on the operator's message

  Scenario: A message queued for after the turn is answered by a prompt of its own
    Given an agent turn that calls a tool, answers, and has an answer for one more prompt
    When the operator queues "then update the changelog" for after the turn while the tool is running
    Then the step after the tool result does not carry that message
    And that message is answered by a prompt of its own after the first answer
    And the clients see that message arrive between the two answers
