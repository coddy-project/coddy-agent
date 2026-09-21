Feature: Subscription quotas use the shared account usage panel
  Codex and Devin subscriptions expose quota windows rather than a number of
  remaining prompts. Every surface reads the provider's reported percentages
  and reset times through the same cached provider usage endpoint.

  @http
  Scenario: A Codex subscription reports its plan and quota windows
    Given a subscription usage server for "codex"
    When I read the subscription usage over REST
    Then the subscription usage names the plan "plus"
    And the subscription has a "session" window labelled "5h" at 84 percent
    And the subscription has a "week" window labelled "week" at 37 percent
    And the subscription windows carry reset times without invented request counts
    And subscription credentials were used upstream but never returned

  @http
  Scenario: Finishing a Codex turn publishes fresh subscription usage
    Given a subscription usage server for "codex"
    When a subscription prompt turn finishes
    Then the subscription usage update names the plan "plus"
    When I read the subscription usage over REST
    Then the subscription has a "session" window labelled "5h" at 84 percent

  @http
  Scenario: A Devin subscription reports its plan and quota windows
    Given a subscription usage server for "devin"
    When I read the subscription usage over REST
    Then the subscription usage names the plan "Pro"
    And the subscription has a "day" window labelled "day" at 77 percent
    And the subscription has a "week" window labelled "week" at 13 percent
    And the subscription windows carry reset times without invented request counts
    And subscription credentials were used upstream but never returned

  @http
  Scenario: Finishing a Devin turn publishes fresh subscription usage
    Given a subscription usage server for "devin"
    When a subscription prompt turn finishes
    Then the subscription usage update names the plan "Pro"
    When I read the subscription usage over REST
    Then the subscription has a "day" window labelled "day" at 77 percent
