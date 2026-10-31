Feature: Local wallpaper gallery
  The gallery reads pictures installed in the agent home's wallpapers folder.
  It never needs an external image service or a session workspace.

  Scenario: Browse and select a local wallpaper
    Given an authenticated wallpaper server with two local pictures
    When I request the local wallpaper gallery
    Then the gallery lists the two picture names in order
    When I request a listed wallpaper
    Then I receive that picture's validated raster bytes
