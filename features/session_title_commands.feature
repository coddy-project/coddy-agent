Feature: A chat started with a slash command is named by what the command does
  A first message such as "/warm-up" carries no words to name a conversation by:
  the command token is all the model would see, and it either repeats the token
  or guesses at it. The call that names a new chat, POST /coddy/describe, tells
  the model what each command the message invokes does - the description of the
  skill or the built-in action in the chat's own workspace - so the History row
  says what the work is about. A plain first message is described as before.

  Background:
    Given a running coddy HTTP server
    And the title model answers "Repository onboarding and test run" with tags "onboarding" once it is told what the commands do, and repeats the text otherwise

  Scenario: A skill typed alone names the chat by its purpose
    Given the workspace has a skill "warm-up" described "Onboard onto a repository: read the docs and the tests, set up the environment and run the suite"
    When I ask for a description of "/warm-up"
    Then the title model was told that "/warm-up" means "Onboard onto a repository: read the docs and the tests, set up the environment and run the suite"
    And the description is "Repository onboarding and test run"
    And the description proposes tags "onboarding"

  Scenario: A built-in action is described to the model too
    When I ask for a description of "/goal the release builds on every platform"
    Then the title model was told that "/goal" means "Work on a goal until a second model confirms it"

  Scenario: The skill of the folder a new chat picked names that chat
    Given the folder "picked" has a skill "triage" described "Sort the open issues by severity and label them"
    When a new chat in the folder "picked" asks for a description of "/triage"
    Then the title model was told that "/triage" means "Sort the open issues by severity and label them"

  Scenario: A plain first message is described as before
    When I ask for a description of "Please refactor the memory tree endpoint"
    Then the title model was told about no commands
    And the description is "Please refactor the memory tree endpoint"
