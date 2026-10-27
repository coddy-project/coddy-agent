Feature: The standard skill delivery
  Coddy carries a set of skills inside its binary and hands them to the
  operator's home directory the first time it sees that they are not there, so
  a fresh install has them without a network round trip. A release that carries
  a newer skill replaces the copy on disk. The catalogue those skills are
  published from, rpa-skills, is built into Coddy rather than written into
  anybody's config file - which is also why nothing can remove it - and it
  installs nothing on its own: reading it reports the newer versions of the
  skills that are on disk, `plugin update` installs them, and a skill the
  operator deleted is never brought back.

  Background:
    Given an empty coddy home

  Scenario: A fresh home receives the delivery
    When coddy hands over the standard delivery
    Then the home skills directory carries "rpa-feat"
    And the skill "rpa-gen-rules" carries its references on disk
    And the skill "crossreview" carries its runnable scripts on disk
    And the skill catalogue offers "rpa-feat"
    And the built-in marketplace "rpa-skills" reads from "EvilFreelancer/rpa-skills"

  Scenario: A newer skill in the release replaces the copy on disk
    Given the home already carries skill "rpa-feat" at version "0.1.0"
    When coddy hands over the standard delivery
    Then the home skill "rpa-feat" is at the delivered version

  Scenario: A skill the operator deleted stays deleted
    Given coddy has handed over the standard delivery
    And the operator deletes the skill "rpa-feat"
    When coddy hands over the standard delivery
    Then the home skills directory does not carry "rpa-feat"
    And the skill catalogue does not offer "rpa-feat"

  Scenario: The catalogue of the delivery is built in and cannot be removed
    When coddy hands over the standard delivery
    Then the built-in marketplace "rpa-skills" reads from "EvilFreelancer/rpa-skills"
    And removing the marketplace "rpa-skills" is refused
    And removing the marketplace "EvilFreelancer/rpa-skills" is refused
    And the built-in marketplace "rpa-skills" reads from "EvilFreelancer/rpa-skills"
    And the config file was not touched

  Scenario: A newer version in the built-in catalogue is reported, then installed by plugin update
    Given coddy has handed over the standard delivery
    And the built-in catalogue publishes "rpa-feat" at version "9.0.0"
    When I run the plugin command "marketplace update"
    Then the plugin command answers "1 update available"
    And the plugin command answers "rpa-feat"
    And the plugin command answers "-> 9.0.0"
    And the home skill "rpa-feat" is at the delivered version
    When I run the plugin command "update"
    Then the plugin command answers "0 added, 1 updated, 0 failed."
    And the home skill "rpa-feat" is at version "9.0.0"

  Scenario: A skill of the delivery the operator deleted is not installed back
    Given coddy has handed over the standard delivery
    And the operator deletes the skill "rpa-feat"
    And the built-in catalogue publishes "rpa-feat" at version "9.0.0"
    When I run the plugin command "marketplace update"
    And I run the plugin command "update"
    Then the home skills directory does not carry "rpa-feat"
    And the skill catalogue does not offer "rpa-feat"

  Scenario: A skill of the built-in catalogue is installed when the operator asks for it
    Given the built-in catalogue publishes "logika" at version "2.0.1"
    When I run the plugin command "install logika@rpa-skills"
    Then the plugin command answers "Installed logika@rpa-skills."
    And the home skills directory carries "logika"
