Feature: The web UI installs as an app and notifies from the background
  Issue #508: people asked for Coddy as a PWA, mostly for its notifications. The web UI
  carries a web app manifest, its icons and a service worker, so a browser offers to install
  it as an app with a window of its own. A switch in Settings → Appearance turns on system
  notifications: while Coddy is in the background, the browser says when the agent finished
  a turn, asks for permission or asks a question in a chat the tab follows, and a click on
  the notification opens that chat. The service worker caches nothing, so every request
  still reaches the server; notifications with no tab open are not part of it.

  Scenario: The server serves what makes the web UI installable
    Given the embedded web UI build
    Then the page links the manifest
    And the manifest names the app, its window and its icons
    And every icon the manifest names is served as a PNG of its size
    And the service worker is served from the root and revalidated on every load

  Scenario: Turning notifications on asks the browser
    Then turning the switch on asks the browser and stays on when allowed
    And a refusal leaves the switch off and says where to allow notifications

  Scenario: A hidden tab says when the agent finished, and a click opens the chat
    Then a hidden tab says the turn of its chat ended, and a click opens the chat
    And a turn of a chat the tab has nothing to do with stays quiet
    And nothing is said while the person looks at the page

  Scenario: What waits for an answer is announced
    Then a permission request on the turn the tab shows is announced

  Scenario: The tabs of one browser show one notification
    Then the service worker shows the same tag once when several tabs heard the event
    And it shows nothing while another window of the app has the focus
