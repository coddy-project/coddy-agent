Feature: The settings form fetches the model list a provider advertises
  The logical-models editor offers the models a provider serves instead of
  making the operator type the ids by hand. The provider row being edited has
  not necessarily been saved yet - the sign-in fields accept such rows, and the
  models fetch does too: the row travels in the request body of
  POST /coddy/providers/models (issue #335), so a provider that exists only in
  the form is fetched the same way as a stored one. Fields the body leaves
  empty are inherited from a saved provider of the same name, so a sparse
  {"name": "..."} post resolves the stored credentials without secrets
  travelling over the wire.

  Scenario: A provider row that is not saved yet lists its models
    Given an upstream model endpoint serving "m1,m2"
    And a coddy server holding only a provider named "other"
    When the settings form posts the provider row "fresh" of type "openai" at that upstream with key "sk-fresh"
    Then the gateway answers with the models "m1,m2"
    And the upstream saw the key "sk-fresh"

  Scenario: The listing carries the context window the provider reports
    Given an upstream model endpoint serving "m1:131072,m2:8192"
    And a coddy server holding only a provider named "other"
    When the settings form posts the provider row "fresh" of type "openai" at that upstream with key "sk-fresh"
    Then the gateway answers with the models "m1,m2"
    And the gateway answers with context window 131072 for "m1"
    And the gateway answers with context window 8192 for "m2"
