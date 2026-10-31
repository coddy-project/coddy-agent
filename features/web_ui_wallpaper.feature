Feature: A wallpaper of one's own under the web UI
  Settings → Appearance shows the selected server's wallpaper gallery. Upload adds a
  picture to that server; the chosen background stays cached in this browser. Choosing
  a thumbnail fills the viewport from the centre and keeps the theme's glass and grain.
  Clearing the background keeps the server file. Simplified mode keeps the wallpaper.

  Scenario: A picture becomes the wallpaper
    Then upload selects the actual returned name when no background is set
    And clear background keeps the server file and clears only the browser selection
    And the page carries the wallpaper and its dimming while one is set
    And the wallpaper covers the viewport, dimmed toward the theme's canvas colour

  Scenario: Readable pictures have no numeric size or geometry restrictions
    Then readable pictures of any geometry can become the wallpaper
    And a readable picture larger than 20 MiB becomes the wallpaper
    And a gallery thumbnail larger than 20 MiB is displayed

  Scenario: Simplified mode keeps the chosen wallpaper
    Then a wallpaper remains visible in simplified mode

  Scenario: Dimming is remembered in the browser
    Then dimming is kept in a cookie and bounded to 0..90

  Scenario: Wallpaper choices leave the existing backdrop system intact
    Then Appearance offers a compact always-visible gallery
    And the backdrop store holds only wallpaper choices
    And wallpaper leaves the native gradients, grain and panel glass in place without a backdrop canvas
