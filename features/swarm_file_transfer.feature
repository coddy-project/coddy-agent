Feature: Transfer session files through a relay-mounted node
  A client connected to a node through a swarm relay can upload an image and
  retrieve session-owned files without any direct route to that node.

  Scenario: An uploaded image and a shared artifact stay available through the relay
    Given a swarm relay mounts an authenticated file-capable node
    When I upload a PNG image to the mounted node
    Then the node receives its own credential for the upload
    And the node shares a report file
    Then the uploaded image thumbnail is available through the relay
    And the shared report is downloadable through the relay
    And the node receives its own credential for every file request

  Scenario: A workspace video plays through the relay from its signed address
    Given a swarm relay mounts an authenticated file-capable node
    When I ask the mounted node for a media address of "report.txt"
    Then the first six bytes of "report.txt" stream through the relay from that address alone
    And the node checks the media address itself
