Feature: Supervisor model controls
  Scenario: A goal check uses its model's protocol and service tier
    When the supervisor checks a goal through a Responses-only model with Flex service
    Then the goal check succeeds with the configured protocol and service tier
