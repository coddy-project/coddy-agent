Feature: The simplified mode of the web UI
  The glass of the web UI costs a weak machine real work: a blur behind every panel,
  soft shadows, gradients and grain on the canvas, and animations that keep the
  compositor busy. A switch under the theme grid in Settings → Appearance turns all of
  it off in every theme: the panels are solid colours, nothing moves, and the marks that
  say something (the theme picked, a focused control, a dot of a session) stay. The
  browser keeps the choice in a cookie, so it applies before the first paint.

  Scenario: The switch stands under the theme grid and turns the mode on
    Then the simplified mode switch stands between the theme grid and the language
    And the name of the mode is the switch's label and what it turns off is in the tooltip
    And turning it on marks the page and the cookie, and turning it off clears both

  Scenario: The mode outlives a change of theme and a reload
    Then a theme picked while the mode is on leaves it on
    And a page loaded with the cookie starts in the mode

  Scenario: The stylesheet takes the glass away in every theme
    Then the simplified mode comes last in the cascade and clears shadows, blur, masks and motion
    And the panels of the simplified mode are solid colours of the theme
    And every gradient of the stylesheet is cleared by the simplified mode or draws a mark

  Scenario: Script-driven motion stops with it
    Then a jump down the transcript goes at once in the simplified mode
