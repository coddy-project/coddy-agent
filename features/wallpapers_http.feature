Feature: Local wallpaper gallery
  The gallery reads pictures installed in the agent home's wallpapers folder.
  It never needs an external image service or a session workspace.

  Scenario: Default pictures are installed once
    Given a fresh wallpaper installation
    When the wallpaper server starts
    Then the gallery contains the four bundled pictures
    When I delete the bundled picture "dunes.png" and restart the server
    Then the deleted bundled picture remains absent

  Scenario: Browse and select a local wallpaper
    Given an authenticated wallpaper server with two local pictures
    When I request the local wallpaper gallery
    Then the gallery lists the two picture names in order
    When I request a listed wallpaper
    Then I receive that picture's original raster bytes

  Scenario Outline: Select pictures of any geometry
    Given an authenticated wallpaper server with two local pictures of <width> by <height> pixels
    When I request the local wallpaper gallery
    Then the gallery lists the two picture names in order
    When I request a listed wallpaper
    Then I receive that picture's original raster bytes

    Examples:
      | width | height |
      | 1     | 1      |
      | 120   | 2000   |
      | 5000  | 20     |

  Scenario: Upload a picture, reuse identical bytes and delete it
    Given an authenticated wallpaper server and a picture on my computer
    When I upload that picture as "Uploaded sky.png"
    Then the upload creates "Uploaded sky.png"
    When I upload that picture as "Different name.jpg"
    Then the upload reuses "Uploaded sky.png"
    When I request the local wallpaper gallery
    Then the gallery contains only "Uploaded sky.png"
    When I request the uploaded wallpaper
    Then I receive that picture's original raster bytes
    When I delete the uploaded wallpaper
    Then the gallery is empty
