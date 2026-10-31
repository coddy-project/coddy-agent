Feature: Manage the selected server's wallpaper gallery
  Appearance shows compact thumbnails from CODDY_HOME/wallpapers on the selected server.
  Upload stores the file there and reuses an existing name for duplicate bytes.
  Each thumbnail has a separate trash control. Deletion asks for confirmation.

  Scenario: Browse and choose a background
    Then Appearance offers a compact always-visible gallery
    And a selected thumbnail is marked without a duplicate preview

  Scenario: Upload while keeping a background
    Then upload preserves an existing private background exactly and never uploads it automatically

  Scenario: Reuse duplicate bytes
    Then a duplicate upload selects the existing server filename and the grid is sorted

  Scenario: Delete the selected background from the server
    Then confirmed deletion of a.png clears only a matching background

  Scenario: Delete another picture
    Then confirmed deletion of z.png clears only a matching background

  Scenario: Use the gallery through a remote environment
    Then remote uploads, thumbnails, selections and deletes use header authentication
