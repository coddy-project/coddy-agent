Feature: The agent and the person work on a page on the canvas
  The canvas is a board of live frames beside a chat of the web UI. The agent writes a page
  of the workspace with the file tools and puts it on the canvas with canvas_show; the web
  UI runs it in a sandboxed frame whose address carries its own capability, so it loads
  locally, from a remote server and through a swarm relay alike. The browser reports what
  the page did, and the agent reads it with canvas_inspect instead of asking the person to
  open a console.

  Scenario: The agent puts a page on the canvas
    Given a chat whose agent is a scripted model, in a workspace with a shop page in "site"
    And a browser following the server's events
    When the person asks "show the shop on the canvas"
    Then the canvas of the chat holds one frame for "site/index.html" at 1280x800
    And the events stream announced the frame as added
    And the frame's address serves the page with the bridge under a sandbox policy
    And the frame's module script loads from the frame's opaque origin

  Scenario: The agent reads what the browser saw
    Given a chat whose agent is a scripted model, in a workspace with a shop page in "site"
    And the person asked "show the shop on the canvas"
    When the browser reports the console error "TypeError: cart is undefined" for the frame's current files
    And the person asks "check the canvas"
    Then the agent read "TypeError: cart is undefined" as untrusted output of the page

  Scenario: A frame taken off the canvas takes its address with it
    Given a chat whose agent is a scripted model, in a workspace with a shop page in "site"
    And the person asked "show the shop on the canvas"
    When the person takes the frame off the canvas
    Then the frame's address is refused
    And "site/index.html" is still in the workspace

  Scenario: A board drawn in Obsidian opens on the canvas and stays an Obsidian file
    Given a chat whose agent is a scripted model, in a workspace with a shop page in "site"
    And the workspace has an Obsidian canvas "design/shop.canvas" with a note linked to the shop page
    When the person asks "open the design board"
    Then the canvas of the chat is "design/shop.canvas" with the shop page as a live frame, the note and the edge
    And the frame's address serves the page with the bridge under a sandbox policy
    When the person moves the note on the canvas
    Then "design/shop.canvas" holds the note at its new place and every key Coddy does not know
