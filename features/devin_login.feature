Feature: Sign in to Devin from the provider settings
  The provider form signs the selected row in through Devin's browser PKCE flow.
  A browser on another machine can finish the same login by pasting the full
  callback address into the form.

  Scenario: Sign in an unsaved provider and sign out
    Given a Devin account server and a provider settings server
    When the form starts Devin sign-in
    Then the browser receives a verification page
    And the login completes from the pasted callback address
    When the form signs out of Devin
    Then the provider has no stored Devin login

  @ui
  Scenario: The Devin provider row offers account sign-in
    Then the provider form shows Sign In with Devin
    And signing in sends the unsaved proxy and opens the verification page
    And callback paste shows the account and a Sign Out button
