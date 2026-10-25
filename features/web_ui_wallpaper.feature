Feature: A wallpaper of one's own under the web UI
  People asked to change the background of the web UI. Settings → Appearance takes a
  picture from the computer and shows it under the app instead of the glows, dimmed toward
  the theme's canvas colour so text on the glass stays readable. The picture is kept in the
  browser and never sent to the server. With smooth gradients on, the canvas paints the
  wallpaper too, and each glass panel gets the picture blurred the way its own glass is.

  Scenario: A picture becomes the wallpaper
    Then a chosen picture shows its preview, the dimming and Remove
    And the picture becomes the wallpaper and Remove takes it away
    And the page carries the wallpaper and its dimming while one is set
    And the wallpaper covers the viewport, dimmed toward the theme's canvas colour

  Scenario: What cannot be a wallpaper is refused with a reason
    Then a picture under 1280x720 is refused and the message names its size
    And a file that is not a picture or is too large is refused

  Scenario: Dimming is remembered in the browser
    Then dimming is kept in a cookie and bounded to 0..90

  Scenario: Smooth gradients cover the wallpaper too
    Then each panel's image over a wallpaper is blurred the way its own glass is
    And the blur keeps the tone and smooths a step
    And the panel's saturation leaves grey alone
