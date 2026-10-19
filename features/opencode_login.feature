Feature: Sign in to OpenCode from the provider settings
  The browser shows a device code while the server signs the selected provider
  into OpenCode Console, using the current proxy from the unsaved form.

  Scenario: Sign in an unsaved provider and sign out
    Given an OpenCode account issuer and a provider settings server
    When the form starts OpenCode sign-in with its current proxy
    Then the browser receives a verification page and a user code
    And the login completes with the selected organization
    When the form signs out of OpenCode
    Then the provider has no stored login

  @ui
  Scenario: The OpenCode provider row offers account sign-in
    Then the provider form shows Sign In with OpenCode beside its gateway picker
    And signing in sends the unsaved gateway and proxy, opens the page and shows the code
    And confirmation shows the organization and a Sign Out button
