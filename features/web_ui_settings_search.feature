Feature: Settings can be searched
  The Settings drawer has grown past what one can scan: eighteen tabs, two of them
  lists whose fields exist only inside an open row, and a folded block in the
  provider form. One field searches every tab by the names, config keys and
  descriptions the page draws, and a result takes the operator to the setting.

  Scenario: Slash puts the cursor in the search
    Then slash puts the cursor in the settings search unless a field has it or a dialog is open

  Scenario: A query shows matching settings from every tab
    Then typing a query shows the matching settings of every tab in place of the sections, with the words marked

  Scenario: A result opens its tab and the setting
    Then choosing a field in another tab opens that tab, rings the setting and puts the cursor in it
    And searching and choosing a result leave unsaved edits as they were

  Scenario: A field of a provider row opens that row
    Then choosing a field of a provider row opens that row and unfolds the block it stands in
    And choosing a field of another row of the open list reveals it in that row, not in the one that was open

  Scenario: A mouse gets the setting's control, a touch screen its box
    Then on a fine pointer the focus goes to the control named by the primary name (Proxy URL's input, a model's id input), never a switch beside it, a disabled input or the (i)
    And on a touch-only device the focus goes to the setting's named box, so no keyboard opens

  Scenario: The query stays for the next result
    Then the query stays after a choice, and the field brings the results back with the chosen one highlighted

  Scenario: Escape and Telegram's Back clear the search first
    Then Escape with results showing clears the query wherever the focus is
    And the Telegram back button clears shown results first, then goes back from a tab to the tiles

  Scenario: Russian names and config keys both find a setting
    Then a Russian page finds a setting by its Russian name and by its config key

  Scenario: A relay's own settings are searched
    Then a relay's settings are searched too

  Scenario: A tab the binary cannot run is not searched
    Then the Metrics tab is a tab and a result, and a binary without its tag offers neither

  Scenario: A link to a highlighted setting opens it highlighted
    Then choosing a result writes the setting into the address, next to its row, and a tab writes none
    And an address that names a setting reveals it as a choice does: the tab, the ring, the scroll and the control
    And an address that names a field of a row opens the row, unfolds its block and reveals the field

  Scenario: A group of settings is found by its name
    Then a folded group chosen from the results opens, is scrolled to whole and ringed, and takes the cursor as the named fieldset
    And a block of a tab is found by its name and takes the ring, and so does a block of a model's row
    And a nested object and a list are found by their legends, and the whole fieldset is scrolled to and ringed

  Scenario: The slash shortcut is advertised in the empty field
    Then the slash badge shows in an empty field that has no cursor, and goes while the field has the cursor or holds text
    And a touch-only device has no key to press, so no badge is drawn on any width
