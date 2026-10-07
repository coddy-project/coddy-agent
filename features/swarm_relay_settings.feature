Feature: The settings page of a relay
  A relay is a deployment of its own: a name, an address, the tokens of its
  clients and of the nodes that join it, CORS for pages served elsewhere. The
  Settings drawer of the web UI reads and saves them on the relay itself, with
  the relay's client token, the way it reads and saves an agent's: the form
  shows the relay's deployment and its log, the credentials in it are
  write-only, and a save is written over the relay's config.yaml and put in
  place, where the relay is rebuilt on it (issue #401).

  Background:
    Given a relay named "office" whose settings live in a file, with client token "client-secret"

  Scenario: The settings form of a relay shows its deployment and its log
    When I read the relay's settings schema with the client token
    Then the schema is a relay's, with the sections "swarm, logger"

  Scenario: A relay's settings are read without its credentials
    When I read the relay's settings with the client token
    Then the settings name the relay "office"
    And the settings say a client token is set without carrying it

  Scenario: A relay's settings carry the relay and nothing else of its host
    When I read the relay's settings with the client token
    Then the settings carry only the sections "swarm, logger"

  Scenario: A page's origin allowed from the settings reaches the file and the relay
    When I save the relay's settings allowing the origin "http://laptop.lan:12345" with the client token
    Then the save succeeds
    And the relay's file allows the origin "http://laptop.lan:12345"
    And the relay's file keeps its comments and its client token
    And the relay's file keeps the provider the settings do not show
    And the relay was handed the new settings

  Scenario: Loopback origins allowed from the settings reach the file and the relay
    When I save the relay's settings allowing loopback origins with the client token
    Then the save succeeds
    And the relay's file allows loopback origins
    And the relay was handed the new settings allowing loopback origins

  Scenario: The settings need the client token
    When I read the relay's settings without a credential
    Then the settings request is rejected as unauthorized
