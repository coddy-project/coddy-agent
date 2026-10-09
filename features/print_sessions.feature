Feature: One-shot print runs stay out of the session pickers
  `coddy -p` stores its run like any other session and marks it as a print run
  when it creates it. The lists a person picks a conversation from - History,
  the console's /resume and the interactive `coddy -c` - leave print runs out,
  `coddy -c -p` keeps continuing them, and `--ephemeral` leaves nothing behind.

  Scenario: A print run is stored, marked and kept out of the picker
    When the operator runs coddy -p "summarize the log"
    Then the run printed the answer
    And the store holds 1 session marked as a print run
    And the session picker of the folder lists no session

  Scenario: A print run continues the previous print run
    When the operator runs coddy -p "first question"
    And the operator runs coddy -c -p "second question"
    Then the store holds 1 session marked as a print run
    And that session holds the prompts "first question" and "second question"

  Scenario: The interactive continue skips print runs
    Given a conversation the operator started in the console of the folder
    When the operator runs coddy -p "a script's question"
    Then continuing interactively in the folder opens the console conversation

  Scenario: An ephemeral run leaves nothing behind
    When the operator runs coddy --ephemeral -p "throwaway question"
    Then the run printed the answer
    And the store holds no session
