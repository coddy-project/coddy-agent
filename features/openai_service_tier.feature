Feature: OpenAI service tier
  A session keeps its selected tier.
  A direct request has a separate tier and reports the provider's actual tier.

  Scenario: A direct response does not replace the session preference
    Given an OpenAI session with the Fast tier
    When a direct Responses request asks for Ultrafast in that session
    Then the provider receives "ultrafast"
    And the response reports "default" while the session keeps "priority"
