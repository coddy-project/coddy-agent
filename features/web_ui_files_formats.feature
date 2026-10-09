Feature: Files of every kind in the Files window
  The Files window colours source over its whole text, with the grammars the
  chat's code blocks use, so a comment or a string that spans lines keeps its
  colour on every line. A PDF opens in the browser's own viewer where the
  browser has one. An SVG is a picture and an HTML file its source; the
  window's menu switches either to the other view, and the browser remembers
  the choice for every file of that kind. An HTML page shows in a sandboxed
  frame that runs nothing and loads nothing. A right click on a picture of
  the app - a file of the workspace, an image in the chat, the full-screen
  viewer - offers to copy it and to save it. Nothing new is bundled for any of
  it: the browser draws what it already can.

  Scenario: Source is coloured over its whole text
    Then a block comment is coloured on every line of an open file
    And a file in a language the chat's code blocks know is coloured in the window too

  Scenario: A PDF opens in the browser's own viewer
    Then a PDF opens in the browser's own viewer where the browser has one

  Scenario: An SVG is a picture, its source a choice of the menu
    Then an SVG opens as a picture, and Preview in the menu shows its source
    And the choice of Preview is remembered for every file of its kind

  Scenario: An HTML file is source, its page a choice of the menu
    Then an HTML file opens as its source, and Preview shows it in a sandbox where nothing runs

  Scenario: A picture offers to copy it and to save it
    Then a right click on a picture of the app offers to copy it and to save it
    And Copy image puts the picture on the clipboard as a PNG
    And Save image downloads the picture under its name
    And in the full-screen viewer Escape puts the menu away and leaves the picture open
