Feature: Smooth gradients for screens that show banding
  Issue #622: the dark canvas, its two glows and the glass panels over them span only a few
  dozen of a channel's 256 levels, so each level is a band tens of pixels wide. Browsers
  hide the bands with an ordered dither, and a high-contrast screen shows where one pattern
  meets the next as rings; the glass blurs that dither away, and a blurred shadow ends in a
  one-level edge. A switch in Settings → Appearance paints the canvas with random dither
  instead and gives the title bar, the rail and the composer that picture with their tint
  baked in. It is off by default and kept in the browser, so nothing changes for anyone who
  does not turn it on.

  Scenario: The switch is off until a person turns it on
    Then Settings → Appearance shows the smooth gradients switch under the notifications, off
    And turning it on is remembered in this browser

  Scenario: The canvas is painted where the stylesheet paints it, with random dither
    Then every theme spells the backdrop tokens in a form the painter reads
    And the painter places the glows where the html background rule does
    And each glow has its full alpha at its centre and none at its end stop
    And every pixel carries a dither of about one level

  Scenario: The glass panels show the painted canvas instead of a blur
    Then each panel shows its painted image over its plain tint
    And the panels drop their blur and soft shadows, and the composer its vignette
    And without a canvas nothing is marked and the panels keep their own glass
