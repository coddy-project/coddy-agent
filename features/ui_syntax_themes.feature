Feature: Code highlighting follows the UI theme
  Assistant code blocks use the declared language and the active theme palette.

  Scenario Outline: Each appearance provides a complete syntax palette
    Given the UI theme is "<theme>"
    Then code syntax colors are supplied by that theme
    And code token styles use the theme palette

    Examples:
      | theme          |
      | dark           |
      | light          |
      | midnight       |
      | solarized-dark |
      | monokai        |
      | nord           |
      | rose-pine      |
