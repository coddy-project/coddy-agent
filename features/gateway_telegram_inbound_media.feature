Feature: A Telegram chat sends the agent pictures and files
  A person can send the bot a photo or a document (issue #509). When the
  chat's model reads images, the bot downloads the file from Telegram and
  hands it to the session the way the web UI hands an attachment: a picture
  reaches the model with the turn, every file is kept with the session's
  assets and the model is told where, and the caption is what the person
  asks. An album is one turn with every picture of it.

  Scenario: A captioned photo reaches the model as a picture
    Given a chat whose model reads images
    When the person sends a photo with the caption "What is on this picture?"
    Then the model is asked "What is on this picture?" with 1 picture
    And the session keeps the photo with its files
    And the answer "It is a red square." comes to the chat

  Scenario: A document is kept with the session and the model is told where
    Given a chat whose model reads images
    When the person sends the document "report.pdf" with the caption "Summarize the report"
    Then the model is asked "Summarize the report" and told where "report.pdf" is kept
    And the model is not sent the document's bytes as text
    And the answer "It is a short report." comes to the chat

  Scenario: An album is one turn with every picture of it
    Given a chat whose model reads images
    When the person sends an album of 3 photos with the caption "Which one is the brightest?"
    Then the model is asked "Which one is the brightest?" with 3 pictures in one turn
    And the answer "The second one." comes to the chat
