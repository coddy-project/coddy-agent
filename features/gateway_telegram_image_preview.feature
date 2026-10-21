Feature: A Telegram chat sees the pictures the agent looked at
  When the agent of a chat reads an image file and its model reads images, the
  bot sends that picture into the chat as a photo captioned with the file's
  name while the turn runs, so the person sees what the agent looked at before
  the answer comes. The photo is the copy Coddy kept with the session, the
  picture the model was shown. A screenshot an MCP tool returns reaches the
  chat the same way, captioned with the name the agent gave it.

  Scenario: A picture the agent read reaches the chat as a photo
    Given a chat whose agent reads images and a screenshot "shot.png" in the workspace
    When the person asks the agent to look at the screenshot
    Then the chat receives "shot.png" as a photo of the screenshot
    And the answer "It is a red square." follows in the chat

  Scenario: A screenshot an MCP tool returned reaches the chat as a photo
    Given a chat whose agent reads images and an MCP server "browser" that takes screenshots
    When the person asks the agent to take a screenshot of the page
    Then the chat receives "browser-take_screenshot-1.png" as a photo of the screenshot
    And the answer "The page is blank." follows in the chat
