Feature: The message queue over HTTP
  A browser watching a turn it started needs somewhere to put the follow-up the operator is
  typing, and somewhere to take it back from. The queue is per session and lives exactly as
  long as the turn it belongs to: it opens when a turn is admitted and is gone when that turn
  releases, so nothing an operator wrote is answered hours later by a session that moved on.

  A session is shared, so the queue is too: what one person queues has to appear for everyone
  looking at that session - the other browser tabs, and a console attached over --remote -
  whether or not they are reading the stream of the turn that is running.

  Background:
    Given a running coddy composer server
    And an agent session

  Scenario: A follow-up written during a turn is queued and listed
    Given a turn is running for that session
    When the operator posts "check the Windows path too" to the session queue
    Then the queue answers with that message and an identifier
    And reading the queue lists that one message

  Scenario: A queued message is removed by its identifier
    Given a turn is running for that session
    And the operator posted "check the Windows path too" to the session queue
    When the operator deletes that queued message
    Then the queue is empty

  Scenario: Nothing can be queued on a session with no turn running
    When the operator posts "check the Windows path too" to the session queue
    Then the queue refuses it because no turn is running
    And reading the queue lists no messages

  Scenario: What one client queues is seen by every other client of the session
    Given a turn is running for that session
    And a second client is watching that turn
    And a third client is subscribed to the server event stream
    When the operator posts "check the Windows path too" to the session queue
    Then the watching client is told what the queue now holds
    And the subscribed client is told what the queue holds
    When the operator deletes that queued message
    Then the subscribed client is told the queue is empty

  Scenario: A follow-up with an image waits for after the turn and can be switched to steer
    Given a turn is running for that session
    And a third client is subscribed to the server event stream
    When the operator posts "compare with this screenshot" with an image to the session queue for after the turn
    Then the queue answers with that message waiting for after the turn with one image
    And the subscribed client is told about the image without its bytes
    When the operator switches that queued message to steer
    Then reading the queue lists that message as steer

  Scenario: Taking a queued message back hands back its image
    Given a turn is running for that session
    And the operator posted "compare with this screenshot" with an image to the session queue for after the turn
    When the operator deletes that queued message
    Then the answer hands back the message with its image

  Scenario: A second client stops the turn and its waiting follow-ups
    Given a turn is running for that session
    And a third client is subscribed to the server event stream
    And the operator posted "check the Windows path too" to the session queue
    And the queue answers with that message and an identifier
    When a second client stops the session turn
    Then the subscribed client is told the queue is empty
    And the subscribed client is told the turn ended
    And reading the queue lists no messages
    And the session accepts the next ordinary prompt
