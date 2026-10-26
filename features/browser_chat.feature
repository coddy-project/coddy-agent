Feature: A chat with Coddy in the side panel of Coddy for Browser
  The person writes to Coddy next to the page they look at. The turn runs on
  the server like any chat's, the tab they write from is the chat's to work
  in, and what needs the person - a permission, the answer - comes back to
  the panel over the same socket.

  Scenario: The side panel asks Coddy about the page the user looks at
    Given Coddy for Browser connected to a Coddy with a scripted model
    And the user looks at the tab "https://example.com/" titled "Example Domain" in the chat's tab group
    When the user asks in the side panel "read this page"
    Then the side panel is asked to allow the browser tool and allows it
    And the extension receives a "snapshot" command for that tab from the panel's chat
    And the side panel shows the answer "The page is Example Domain."
    And the panel lists the chat with the question and the answer
