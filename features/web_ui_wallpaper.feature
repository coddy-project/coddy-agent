Feature: A wallpaper of one's own under the web UI
  People asked to change the background of the web UI. Settings → Appearance takes a
  picture from the computer and shows it under the app instead of the glows, dimmed toward
  the theme's canvas colour so text on the glass stays readable. The picture is kept in the
  browser and never sent to the server. The existing gradients, grain and glass panels
  remain unchanged when no wallpaper is selected.

  Scenario: A picture becomes the wallpaper
    Then a chosen picture shows its preview, the dimming and Remove
    And the picture becomes the wallpaper and Remove takes it away
    And the page carries the wallpaper and its dimming while one is set
    And the wallpaper covers the viewport, dimmed toward the theme's canvas colour

  Scenario: Readable pictures have no numeric size or geometry restrictions
    Then a small picture shows its preview without a size refusal
    And readable pictures of any geometry can become the wallpaper
    And a readable picture larger than 20 MiB becomes the wallpaper
    And a gallery thumbnail larger than 20 MiB is displayed

  Scenario: Simplified mode keeps the chosen wallpaper
    Then a wallpaper remains visible in simplified mode

  Scenario: Dimming is remembered in the browser
    Then dimming is kept in a cookie and bounded to 0..90

  Scenario: Wallpaper choices leave the existing backdrop system intact
    Then Appearance offers wallpaper without a gradient control
    And the backdrop store holds only wallpaper choices
    And wallpaper leaves the native gradients, grain and panel glass in place without a backdrop canvas
