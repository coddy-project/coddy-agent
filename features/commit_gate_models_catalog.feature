Feature: A commit that touches code keeps the models catalog current
  The settings screen fills a model's window from models.json, a copy of
  models.dev embedded into the binary, and nothing refreshes that copy on its
  own. The commit gate compares the staged copy with models.dev, so whoever
  commits, a person or a coding agent, learns about a new model at that commit
  and refreshes the file on the spot.

  Scenario: The gate stops a commit while the catalog is behind and says what to do
    Given a checkout whose models catalog lacks the model "gpt-new" of models.dev
    When a code change is committed
    Then the commit is refused
    And the gate names the model "gpt-new"
    And the gate says to run "make models-catalog"

  Scenario: The same commit goes through with the refreshed catalog
    Given a checkout whose models catalog lacks the model "gpt-new" of models.dev
    And the catalog has been refreshed and staged
    When a code change is committed
    Then the commit lands
    And the commit carries the model "gpt-new" in the catalog
