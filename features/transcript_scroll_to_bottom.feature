Feature: Scroll the transcript back to the bottom
  Scrolling up leaves the newest messages off screen. A control above the
  composer takes the reader back to them without dragging the scrollbar.

  Scenario: Return to the newest message after scrolling up
    Then the transcript scroll-to-bottom button appears when the reader scrolls up and returns them to the newest message
