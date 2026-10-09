Feature: The composer's mode, model and level belong to the session on screen
  The web UI keeps one set of selectors for the whole tab, and a prompt carries
  from them the mode (the top-level model), metadata.model and
  metadata.reasoning, which the server applies to the session and keeps. A new
  chat used to be created in the mode of the chat just left, and a prompt sent
  while the opened session's settings were still being read rewrote that
  session's mode, model and level with the previous session's. A new chat now
  starts on a new chat's settings, Send waits for the opened session's own
  snapshot, and only a pick made in a session is ever sent to it.

  Scenario: The settings follow the session, not the tab
    Then a new chat starts in Agent mode, not in the mode of the chat just left
    And a mode picked on the start page creates the chat in that mode
    And a prompt sent while the opened session's settings are read carries nothing of the previous session
    And a mode picked in a session is sent with its next prompt, and stays in that session
    And coming back to a chat whose turn this tab runs shows that chat's own settings
