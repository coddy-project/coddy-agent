Feature: External web authentication
  Scenario: A direct visit to the logout continuation stays on Coddy
    When a browser visits the logout continuation without a ticket
    Then it is redirected to the Coddy root

  Scenario: External sign-in grants full operator access without a local factor
    Given the server uses an external issuer and has an enrolled local account
    When two subjects with the configured role sign in at the external issuer
    Then both new sessions can use both chats, settings and tools without contacting the issuer
    And the local password and one-time code work again only after disabling external sign-in

  Scenario: A browser continues logout once without the server contacting the destination
    Given the server uses an external issuer and has an enrolled local account
    When the external browser signs out
    Then the eligible browser can continue logout exactly once
