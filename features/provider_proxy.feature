Feature: Each provider reaches its server the way its own proxy setting says
  A provider row follows the proxy the environment of the Coddy process names
  (HTTPS_PROXY, HTTP_PROXY, NO_PROXY) unless providers[].proxy says otherwise:
  "none" connects directly and ignores that proxy, and a proxy URL sends the
  row's requests through that proxy instead. Leaving the key out, or writing
  "inherit", keeps the behaviour Coddy always had. The setting covers every
  request the row makes - the completions, the model list, the account usage
  and the sign-in - and belongs to its row alone, so a provider that needs a
  proxy and one that must go direct live side by side in one config.

  Scenario Outline: Every request of a provider follows the provider's setting
    Given the environment names a proxy for every request
    And a "neuraldeep" provider "hub" <setting>
    When "hub" is asked for a completion, its model list, its account usage and a sign-in code
    Then every answer comes back
    And the environment's proxy carried <through the environment> requests
    And the own proxy of "hub" carried <through its own proxy> requests
    And the model server was reached directly <directly> times

    Examples:
      | setting                 | through the environment | through its own proxy | directly |
      | without a proxy setting | 4                       | 0                     | 0        |
      | with proxy "inherit"    | 4                       | 0                     | 0        |
      | with proxy "none"       | 0                       | 0                     | 4        |
      | with a proxy of its own | 0                       | 4                     | 0        |

  Scenario: A proxy URL on one provider leaves the direct provider next to it alone
    Given the environment names a proxy for every request
    And an "openai" provider "remote" with a proxy of its own
    And an "openai" provider "local" with proxy "none"
    When "remote" is asked for a completion
    And "local" is asked for a completion
    Then every answer comes back
    And the own proxy of "remote" carried 1 request
    And the own proxy of "local" carried 0 requests
    And the environment's proxy carried 0 requests
    And the model server was reached directly 1 time
