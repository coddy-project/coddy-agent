Feature: Marking up a picture before it is sent from the web UI
  A picture pasted into the composer opens in the markup editor first, the
  same editor the console opens on a desktop: pencil, ellipse, rectangle,
  arrow and text in one of seven colours, crop, a quarter turn and undo. Done
  attaches the picture as marked up, as a PNG; Cancel attaches nothing. A
  picture already attached opens in the editor from the pencil on its card,
  and Done replaces it. With a pen the pencil draws thicker where it is
  pressed harder, keeps every point the pen reports, and ignores a hand
  resting on the screen while the pen is in use.

  Scenario: A pasted picture is marked up before it is attached
    Then a pasted picture opens in the markup editor and Done attaches it
    And Cancel in the markup editor attaches nothing

  Scenario: An attached picture is marked up in place
    Then the pencil on an attached picture marks it up in place

  Scenario: The editor draws, types, crops and turns the picture
    Then the editor draws with the chosen tool and colour and hands back a PNG on Done
    And the editor types a label in any script and commits it with Enter
    And the editor crops and turns the picture

  Scenario: The editor draws a pen stroke with pressure
    Then the editor draws a pen stroke wider where it was pressed harder
    And the editor keeps every point a fast pen stroke reports
    And the editor ignores a hand resting on the screen while the pen is in use
    And the editor keeps a second pointer out of a stroke in progress
