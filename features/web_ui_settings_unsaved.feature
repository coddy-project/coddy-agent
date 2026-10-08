Feature: Settings say when a change is not saved yet
  Issue #485: a change in the web UI's Settings takes effect only after Save, and nothing on
  screen said so. A provider signed in through ChatGPT looked ready, but it was not usable
  until Save was pressed as well, because the sign-in felt like the step that applied it.

  The form still saves only on Save. While it holds an edit the server does not have, Save
  stands out - a solid accent with a ring - and the line before the buttons says the changes
  are not saved; an edit put back as it was is no edit. Closing the drawer over such edits
  asks to save them or keep editing, and so does Escape; leaving the page asks the browser.

  Scenario: An edit makes Save stand out until it is saved
    Then an edit highlights Save and says it is not saved until Save is pressed

  Scenario: An edit put back is no edit
    Then an edit put back as it was leaves Save as it was

  Scenario: Closing over unsaved edits asks first
    Then closing the drawer over unsaved edits asks to save them or keep editing
    And Escape over unsaved edits asks first

  Scenario: Leaving the page over unsaved edits asks the browser
    Then leaving the page over unsaved edits asks the browser, and nothing is saved into another server
