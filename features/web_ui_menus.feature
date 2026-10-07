Feature: The menus of the web UI stay with what they serve
  A menu belongs to the control that opened it and to the draft it completes.
  Typing a slash command opens the completion menu over the composer; sending
  that draft with the Send button, or queueing it while a turn runs, empties the
  composer from outside, and the textarea fires no change event for a value its
  parent set, so the menu stayed over the empty composer until the next
  keystroke. The environment is an item at the foot of the nav rail: its menu
  opens beside the rail, its foot level with the item's, and stays inside the
  window, so the entries a token is added with are never cut off. On a phone
  the History drawer is the width of the screen and its filter control sits at
  the right edge, so the choices of a filter opened on the left of the menu and
  ran off the screen: only the column of check marks was left to see.

  Scenario: Sending or queueing a command closes its menu
    Then Send clicked while the slash menu is open closes the menu
    And a draft queued while the slash menu is open closes the menu

  Scenario: The environment menu opens beside the rail and stays inside the window
    Then the environment menu opens beside the rail from the foot of it
    And the environment menu stays inside the window

  Scenario: The History filter menu fits a phone
    Then on a phone the choices of a History filter fold out under their row
    And the History filter menu is never wider than the room left of its control

  Scenario: A History filter row opened by the pointer stays open when clicked
    Then clicking a History filter row the pointer just opened keeps it open
