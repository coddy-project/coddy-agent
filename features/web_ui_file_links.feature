Feature: Files named in the conversation open in the Files window
  A file the agent shared with share_file and a path an answer names both open
  in a tab of the Files window, the web UI's own file manager, whatever the
  server's desktop is: a server without one used to answer "Reveal on server"
  with nothing but a failure. The card of a shared file opens the file it was
  copied from; when that file was deleted from the workspace, the card says so
  and keeps the shared copy downloadable. A path an answer names, as inline
  code, as a link or in its prose, becomes a link once the server says it is a
  file of the chat's workspace, and stays the text it was otherwise.

  Scenario: A shared file opens in the Files window
    Then a click on a shared file's card opens its source in the Files window
    And a card checks its file before it opens it and opens it when it is there

  Scenario: A shared file whose source was deleted says so
    Then a shared file whose source was deleted says so and keeps its copy downloadable
    And a card whose file was deleted since the listing finds it out on the click

  Scenario: A path an answer names opens in a tab of the Files window
    Then a path an answer names opens in a tab of the Files window once the server says it is a file
    And what is not a file of the workspace keeps its look and its copy button
