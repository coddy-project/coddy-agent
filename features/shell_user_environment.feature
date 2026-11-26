Feature: Shell commands use the user's current environment
  Bash commands receive variables exported by the user's current ~/.bashrc,
  including PATH entries installed by version managers such as nvm. A running
  Coddy process does not need to be restarted after that file changes.

  Scenario: A Bash command sees a PATH exported by the current bashrc
    Given Bash with a temporary home directory
    And the Bash startup file adds "/tmp/coddy-nvm/bin" to PATH
    When I run a command that prints PATH
    Then the output starts with "/tmp/coddy-nvm/bin:"
