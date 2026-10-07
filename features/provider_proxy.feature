Feature: Each provider reaches its server the way its own proxy setting says
  A provider row follows the proxy the environment of the Coddy process names
  (HTTPS_PROXY, HTTP_PROXY, NO_PROXY) unless providers[].proxy says otherwise:
  "none" connects directly and ignores that proxy, and a proxy URL sends the
  row's requests through that proxy instead. Leaving the key out, or writing
  "inherit", keeps the behaviour Coddy always had. The setting covers every
  request the row makes - completions, model lists, account usage, sign-ins,
  token refreshes and sign-outs - and belongs to its row alone, so a provider
  that needs a proxy and one that must go direct live side by side in one
  config.

  Scenario Outline: Every request of a neuraldeep provider follows the provider's setting
    Given the environment names a proxy for every request
    And a "neuraldeep" provider "hub" <setting>
    When "hub" is asked for a completion, its model list, its account usage and its auth flow
    Then every answer comes back
    And the environment's proxy carried <through the environment> requests
    And the own proxy of "hub" carried <through its own proxy> requests
    And the provider's servers were reached directly <directly> times

    Examples:
      | setting                 | through the environment | through its own proxy | directly |
      | without a proxy setting | 10                      | 0                     | 0        |
      | with proxy "inherit"    | 10                      | 0                     | 0        |
      | with proxy "none"       | 0                       | 0                     | 10       |
      | with a proxy of its own | 0                       | 10                     | 0        |

  Scenario Outline: Every request of a codex provider follows the provider's setting
    Given the environment names a proxy for every request
    And a "codex" provider "cx" <setting>
    When "cx" is asked for a completion, its model list, its account usage, a token refresh, a device sign-in and a config apply
    Then every answer comes back
    And the environment's proxy carried <through the environment> requests
    And the own proxy of "cx" carried <through its own proxy> requests
    And the provider's servers were reached directly <directly> times

    Examples:
      | setting                 | through the environment | through its own proxy | directly |
      | without a proxy setting | 8                       | 0                     | 0        |
      | with proxy "inherit"    | 8                       | 0                     | 0        |
      | with proxy "none"       | 0                       | 0                     | 8        |
      | with a proxy of its own | 0                       | 8                     | 0        |

  Scenario Outline: Every request of a devin provider follows the provider's setting
    Given the environment names a proxy for every request
    And a "devin" provider "dv" <setting>
    When "dv" is asked for a credential check, its model list, a chat answer, its account usage and a sign-in
    Then every answer comes back
    And the environment's proxy carried <through the environment> requests
    And the own proxy of "dv" carried <through its own proxy> requests
    And the provider's servers were reached directly <directly> times

    Examples:
      | setting                 | through the environment | through its own proxy | directly |
      | without a proxy setting | 7                       | 0                     | 0        |
      | with proxy "inherit"    | 7                       | 0                     | 0        |
      | with proxy "none"       | 0                       | 0                     | 7        |
      | with a proxy of its own | 0                       | 7                     | 0        |

  Scenario Outline: Every request of an openai provider follows the provider's setting
    Given the environment names a proxy for every request
    And a "openai" provider "oai" <setting>
    When "oai" is asked for a completion and its model list
    Then every answer comes back
    And the environment's proxy carried <through the environment> requests
    And the own proxy of "oai" carried <through its own proxy> requests
    And the provider's servers were reached directly <directly> times

    Examples:
      | setting                 | through the environment | through its own proxy | directly |
      | without a proxy setting | 2                       | 0                     | 0        |
      | with proxy "inherit"    | 2                       | 0                     | 0        |
      | with proxy "none"       | 0                       | 0                     | 2        |
      | with a proxy of its own | 0                       | 2                     | 0        |

  Scenario Outline: Every request of an anthropic provider follows the provider's setting
    Given the environment names a proxy for every request
    And a "anthropic" provider "ant" <setting>
    When "ant" is asked for a completion and its model list
    Then every answer comes back
    And the environment's proxy carried <through the environment> requests
    And the own proxy of "ant" carried <through its own proxy> requests
    And the provider's servers were reached directly <directly> times

    Examples:
      | setting                 | through the environment | through its own proxy | directly |
      | without a proxy setting | 2                       | 0                     | 0        |
      | with proxy "inherit"    | 2                       | 0                     | 0        |
      | with proxy "none"       | 0                       | 0                     | 2        |
      | with a proxy of its own | 0                       | 2                     | 0        |

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
    And the provider's servers were reached directly 1 time
