Feature: Pasting a picture into the console prompt
  ctrl+v in the console (alt+v where the terminal keeps ctrl+v for its own
  paste) takes the picture the clipboard holds. On a desktop with an X display
  the picture opens in the markup window first, and Done attaches it as marked
  up. The editor shows an attached picture as an [image #N] marker, and the
  prompt that holds the marker carries the picture to the model.

  Background:
    Given a coddy console app over a stub agent runner
    And the configured models take pictures
    And the clipboard holds a 40x30 picture

  Scenario: A copied picture is marked up and sent with the prompt
    Given the markup window marks the picture up and clicks Done
    When the console app starts
    And the operator presses ctrl+v
    Then the editor shows "[image #1]"
    And the markup window was shown the 40x30 picture
    When the operator submits the prompt "what is circled here?"
    Then the stub turn received 1 picture
    And the picture the stub turn received is the marked-up one
    And the screen shows "[image #1] what is circled here?"

  Scenario: Without a display the picture is attached as copied
    Given there is no display for the markup window
    When the console app starts
    And the operator presses alt+v
    Then the editor shows "[image #1]"
    When the operator submits the prompt "describe it"
    Then the stub turn received 1 picture
    And the picture the stub turn received is the copied one

  Scenario: Cancel in the markup window attaches nothing
    Given the markup window is cancelled
    When the console app starts
    And the operator presses ctrl+v
    Then the screen shows "The picture was not attached"
    And the screen does not show "[image #1]"

  Scenario: A picture pasted while a turn runs rides on the queued message
    Given there is no display for the markup window
    When the console app starts
    And the operator submits the prompt "start working"
    And the stub turn blocks until cancelled
    And the operator presses ctrl+v
    Then the editor shows "[image #1]"
    When the operator submits the prompt "and look at this"
    Then the queued message "[image #1] and look at this" carries 1 picture
