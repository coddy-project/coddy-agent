Feature: A pasted picture on every surface
  The console has no pictures: it names a picture as text, [image #N]. The
  web UI shows the same picture, because the session keeps every picture a
  prompt carried with its assets, next to a thumbnail. A picture attached in
  the web UI is named the same way when the console shows that prompt.

  Scenario: A picture pasted in the console reaches the model and stays in the session
    Given a coddy console app over the agent and a scripted model that takes pictures
    And the clipboard holds a 40x30 picture
    And there is no display for the markup window
    When the console app starts
    And the operator presses ctrl+v
    Then the editor shows "[image #1]"
    When the operator submits the prompt "what is on it?"
    Then the model answers
    And the model was sent the picture with the prompt "[image #1] what is on it?"
    And the session keeps the picture with a thumbnail the web UI shows

  Scenario: A picture attached in the web UI is text in the console
    Given a coddy console app over a stub agent runner
    When the console app starts
    And the operator submits the prompt "hello"
    And the stub turn streams the text "Hi!"
    And the session holds a prompt "describe it" sent from the web UI with a picture
    And the operator starts a new session
    And the operator resumes the session before
    Then the screen shows "[image #1] describe it"
