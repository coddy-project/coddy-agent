Feature: UI extension theme colours
  Scenario: Read the theme colours and follow a theme switch
    Given an extension that paints with the theme colours
    When the user switches the theme
    Then the extension reads the new colours and receives the change
