Feature: The canvas of a node through a swarm relay
  A web UI that works on a node through a relay reads the node's canvas through the relay's
  mount like any other session route, and loads each frame from the frame's own address:
  the page and the files it loads carry the node's capability in their path, which the relay
  hands to the node without a credential of its own, so a page in a sandbox loads its
  scripts and nothing else of the swarm answers it.

  Scenario: A frame of a node loads through the relay on its own address
    Given a node that asks for a token, behind a relay that asks for a client token, with a shop page in its workspace
    And the person put "site/index.html" on the node's canvas through the relay
    When the browser loads the frame's address through the relay without a token
    Then the page arrives with the bridge under the node's sandbox policy
    And the frame's module script arrives for an opaque origin
    And the node saw no credential of the relay on the frame's requests
    And the page's request to the node's API through the relay is refused
