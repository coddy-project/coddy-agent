Feature: The settings form takes the models and their windows from the provider
  A logical model is a provider name plus the id the provider's API expects,
  and the operator should not have to type that id or its context window from
  memory. The provider's edit form closes with the list the provider
  advertises, fetched with the row as the form holds it (issue #335: an unsaved
  row, a sign-in that exists only on disk), and files an id under Logical
  models with one click, together with the context window the provider reports
  for it, and takes it out again; an id the document lists that the provider
  no longer advertises is marked there. The logical-model form keeps the id a plain field and asks the
  provider for the model's context window.

  Scenario: The provider form lists the advertised models, adds and removes them
    Then opening a provider row fetches the list with the row as the form holds it
    And a model is added to Logical models from its row
    And a model added from the provider list carries the context window the provider reports
    And a model already in Logical models is checked and unchecking removes it

  Scenario: A model the provider no longer advertises is flagged
    Then a listed model the provider does not advertise is flagged and can be removed

  Scenario: The logical-model form asks the provider for the context window
    Then picking a provider and typing the id composes provider/id
    And an unset window shows the one the provider reports for the model
    And fetching the context window writes the provider's number into the model

  Scenario: The address names the provider that is open
    Then opening a row puts its name in the address and going back takes it out
    And the address opens the row it names
    And an address naming no row goes back to the list and clears the name
    And renaming the open row renames it in the address
