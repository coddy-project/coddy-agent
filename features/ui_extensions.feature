Feature: Operator-installed JavaScript extensions
  Scenario: Install and switch a UI extension
    Given a JavaScript extension archive
    When I install the extension
    Then the extension is disabled
    When I approve and enable the installed digest
    Then its JavaScript can be loaded
    When I disable the extension
    Then its JavaScript is unavailable
