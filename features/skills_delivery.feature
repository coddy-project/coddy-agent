Feature: The standard skill delivery
  Coddy carries a set of skills inside its binary and hands them to the
  operator's home directory the first time it sees that they are not there, so
  a fresh install has them without a network round trip. A release that carries
  a newer skill replaces the copy on disk, and the marketplace those skills are
  published from is built into Coddy rather than written into anybody's config
  file - which is also why nothing can remove it.

  Background:
    Given an empty coddy home

  Scenario: A fresh home receives the delivery
    When coddy hands over the standard delivery
    Then the home skills directory carries "rpa-feat"
    And the skill "rpa-gen-rules" carries its references on disk
    And the skill catalogue offers "rpa-feat"
    And the configured skill sources contain "EvilFreelancer/rpa-skills"

  Scenario: A newer skill in the release replaces the copy on disk
    Given the home already carries skill "rpa-feat" at version "0.1.0"
    When coddy hands over the standard delivery
    Then the home skill "rpa-feat" is at the delivered version

  Scenario: A skill the operator deleted stays deleted
    Given coddy has handed over the standard delivery
    And the operator deletes the skill "rpa-feat"
    When coddy hands over the standard delivery
    Then the home skills directory does not carry "rpa-feat"

  Scenario: The marketplace of the delivery is built in and cannot be removed
    When coddy hands over the standard delivery
    Then the configured skill sources contain "EvilFreelancer/rpa-skills"
    And removing the marketplace "EvilFreelancer/rpa-skills" is refused
    And the configured skill sources contain "EvilFreelancer/rpa-skills"
    And the config file was not touched
