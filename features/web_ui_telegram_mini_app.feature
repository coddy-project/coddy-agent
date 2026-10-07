Feature: The web UI inside Telegram as a Mini App
  Opened by a Telegram bot as its Mini App, the web UI reads Telegram's launch
  parameters once, keeps them for the tab and takes them out of its address,
  and talks to the Telegram client itself, with no script from telegram.org.
  It expands the window, turns vertical swipes off, gives Telegram's header the
  theme's colours, follows Telegram's theme until the user picks one, and makes
  Telegram's back button its Escape. What the stacked shell anchors to the
  bottom is lifted out of the part of a half-open app Telegram does not show,
  and menus stay bottom sheets. An ordinary browser runs none of it.

  Scenario: Telegram's launch is read once and kept out of the address
    Then a launch in the fragment or the query makes the tab a Mini App, and a start parameter alone does not
    And the address keeps its own route and loses Telegram's parameters
    And a reload inside the Mini App is still a Mini App
    And the bot's link opens the chat's conversation, in a Mini App or a browser

  Scenario: The web UI talks to the Telegram client itself
    Then events go out through the client's proxy and come back through receiveEvent
    And in a web client's frame events go to the parent and only the parent is heard
    And the window is expanded, swipes are off, the header takes the theme's colours and ready goes last

  Scenario: The layout follows what Telegram shows
    Then the visible, stable and hidden heights follow the client and the safe areas add up
    And the docked composer and the sheets are lifted by the keyboard or the hidden part, whichever is more
    And the page never clips its own overflow and the top inset is set where the stacked shell sets it

  Scenario: The theme and the back button
    Then the theme follows Telegram until the user picks one
    And the back button is an Escape for whatever answers it
    And an Escape nothing claimed leaves the conversation and never skips a question

  Scenario: An ordinary browser is untouched
    Then outside Telegram nothing is installed or sent
    And the page loads nothing from another origin

  Scenario: A sign-in the browser does not keep is explained
    Then a sign-in the server accepted but the frame did not keep says so and offers a tab of its own
