Feature: A turn's error stays in the web UI transcript
  When a turn ends on an error - a provider answering HTTP 400, for instance -
  the server keeps the message in the session's UI log, stamped with the turn
  it belongs to, and the web UI shows it at the end of that turn when it reads
  the transcript back. The server counts every user-role message of the
  history, the summaries of a compacted context included, so the transcript
  must count them too, or an error of a session compacted before would vanish
  as soon as the live turn is replaced by the saved transcript.

  Scenario: An error after a compaction is shown at the end of its turn
    Then the error of a turn that follows two compaction summaries is shown at the end of that turn
    And an error stamped past the end of the history is still shown
