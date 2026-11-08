Feature: Discover tools before loading their complete native definitions
  Every permitted tool remains in a compact catalog. Activating tools loads
  their complete native definitions for later requests, without executing them
  or granting permission. Already loaded tools can be reused during the run.

  Scenario: A greeting needs no tool schemas
    Given a virtual fixture with permitted read, grep and write tools
    And a model that only greets
    When the fixture turn runs
    Then every permitted name is discoverable
    And the first native offer contains only activation
    And the fixture performed 0 tool actions in 1 model requests

  Scenario: Search and repeated reads share one batch activation
    Given a virtual fixture with permitted read, grep and write tools
    And a model that activates search and read, searches, then reads twice
    When the fixture turn runs
    Then every permitted name is discoverable
    And the first native offer contains only activation
    And the fixture performed 3 tool actions in 5 model requests
    And the discovery catalog stayed unchanged while native schemas grew
