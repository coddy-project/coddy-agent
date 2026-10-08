Feature: A model shared by a remote Coddy is reachable through a swarm relay
  The shared-model routes live under /coddy/, a prefix a relay mount already
  carries, so a local Coddy reaches a node's shared models at
  https://relay/swarm/nodes/<node> as it would at the node's own address:
  the relay authenticates the client with its client token, replaces it with
  the token the node joined with, and streams the answer back whether it
  dials the node or the node dialled it. A node that joined with a token made
  for shared models serves them through the relay and nothing else. The node
  sees one caller, so the stream limit of the node counts the relay's clients
  together.

  Scenario: A shared model answers through a relay that dials the node
    Given a swarm relay with a client token mounting a remote coddy node that requires its own token and shares "stub/qwen3-secret" as "coder" whose model answers "Hello through the relay."
    And a local coddy with a provider "remote" of type coddy pointing at the relay mount of that node with the relay's client token
    When the local coddy streams "remote/coder" with the message "Hi"
    Then the local coddy assembles the answer "Hello through the relay."
    And the node received its own credential and not the relay's client token
    And the node holds no session

  Scenario: A shared model answers through the reverse tunnel of a node that dialled out
    Given a swarm relay with a client token mounting a remote coddy node over its tunnel that requires its own token and shares "stub/qwen3-secret" as "coder" whose model answers "Hello through the tunnel."
    And a local coddy with a provider "remote" of type coddy pointing at the relay mount of that node with the relay's client token
    When the local coddy streams "remote/coder" with the message "Hi"
    Then the local coddy assembles the answer "Hello through the tunnel."

  Scenario: The shared models are listed through the mount under their aliases
    Given a swarm relay with a client token mounting a remote coddy node that requires its own token and shares "stub/qwen3-secret" as "coder" whose model answers "Hello through the relay."
    And a local coddy with a provider "remote" of type coddy pointing at the relay mount of that node with the relay's client token
    When the local coddy fetches the models of provider "remote"
    Then it offers exactly "coder"

  Scenario: A node that joined with a token made for shared models serves them through the relay and nothing else
    Given a swarm relay with a client token mounting a remote coddy node that joined with a shared-model token and shares "stub/qwen3-secret" as "coder" whose model answers "Hello through the relay."
    And a local coddy with a provider "remote" of type coddy pointing at the relay mount of that node with the relay's client token
    When the local coddy streams "remote/coder" with the message "Hi"
    Then the local coddy assembles the answer "Hello through the relay."
    When a client of the relay asks the mount of that node for its sessions
    Then the node refuses it as unauthorized
    And the relay's aggregated session list carries a warning for that node

  Scenario: A node that dialled out with a token made for shared models serves them through its tunnel
    Given a swarm relay with a client token mounting a remote coddy node over its tunnel that joined with a shared-model token and shares "stub/qwen3-secret" as "coder" whose model answers "Hello through the tunnel."
    And a local coddy with a provider "remote" of type coddy pointing at the relay mount of that node with the relay's client token
    When the local coddy streams "remote/coder" with the message "Hi"
    Then the local coddy assembles the answer "Hello through the tunnel."
    When a client of the relay asks the mount of that node for its sessions
    Then the node refuses it as unauthorized

  Scenario: The relay's clients share the node's stream limit
    Given a swarm relay with two client tokens mounting a remote coddy node that requires its own token and shares "stub/qwen3-secret" as "coder" whose model holds every stream open until released
    And the node allows 2 shared-model streams at once
    When each client of the relay holds a stream from "coder" open through the mount
    And the first client requests a third stream
    Then the node answers 429 with the kind "busy"

  Scenario: The account usage of a shared model is read through a relay that dials the node
    Given a swarm relay with a client token mounting a remote coddy node that requires its own token and shares "stub/qwen3-secret" as "coder" whose provider reports 62 percent of its quota used
    And a local coddy with a provider "remote" of type coddy pointing at the relay mount of that node with the relay's client token
    When the local coddy reads the usage of the model "remote/coder"
    Then it reports 62 percent of the quota used
    And the remote marks the reading as account-wide
    And the document never mentions the upstream model id, the provider name, the key name or any amount of money

  Scenario: The account usage of a shared model is read through the reverse tunnel of a node that dialled out
    Given a swarm relay with a client token mounting a remote coddy node over its tunnel that requires its own token and shares "stub/qwen3-secret" as "coder" whose provider reports 62 percent of its quota used
    And a local coddy with a provider "remote" of type coddy pointing at the relay mount of that node with the relay's client token
    When the local coddy reads the usage of the model "remote/coder"
    Then it reports 62 percent of the quota used
    And the remote marks the reading as account-wide
    And the document never mentions the upstream model id, the provider name, the key name or any amount of money
