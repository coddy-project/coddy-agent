Feature: Pick a wallpaper from the server gallery
  Settings offers pictures from CODDY_HOME/wallpapers on the active server.
  Opening the gallery reads the list and authenticated thumbnails. Picking a picture
  keeps its bytes in this browser, like choosing a file from this computer.

  Scenario: Browse the server gallery
    Then nothing is requested from the server gallery until it is opened

  Scenario: Keep a server picture in this browser
    Then a picked server picture becomes the browser wallpaper without credits

  Scenario: Pick a picture through a remote environment
    Then remote thumbnails and selections carry authentication only in headers
