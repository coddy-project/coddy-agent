Feature: File tools replace a file atomically
  The write, edit and apply_patch tools put the new content in a temporary file
  beside the target and rename it over the target. When a write fails half way,
  as it does on a full disk, the file keeps the content it had before the call,
  no temporary file is left behind, and the error the model reads says the file
  was left as it was.

  Scenario: A failed write leaves the existing file as it was
    Given a file "notes.txt" with the content:
      """
      first line
      second line
      third line
      """
    And the disk fails after 5 bytes
    When I write "notes.txt" with the content:
      """
      a replacement that is longer than the failure point
      """
    Then the tool fails and says the file was left as it was
    And "notes.txt" still holds the original content
    And no temporary file is left in the folder

  Scenario: A failed edit leaves the existing file as it was
    Given a file "notes.txt" with the content:
      """
      first line
      second line
      third line
      """
    And the disk fails after 5 bytes
    When I edit "notes.txt" replacing "second line" with "2nd line"
    Then the tool fails and says the file was left as it was
    And "notes.txt" still holds the original content
    And no temporary file is left in the folder

  Scenario: A failed patch leaves the existing file as it was
    Given a file "notes.txt" with the content:
      """
      first line
      second line
      third line
      """
    And the disk fails after 5 bytes
    When I patch "notes.txt" replacing "second line" with "2nd line"
    Then the tool fails and says the file was left as it was
    And "notes.txt" still holds the original content
    And no temporary file is left in the folder

  Scenario: A write that succeeds replaces the content
    Given a file "notes.txt" with the content:
      """
      first line
      second line
      third line
      """
    When I write "notes.txt" with the content:
      """
      only line
      """
    Then the tool succeeds
    And "notes.txt" holds the content:
      """
      only line
      """
    And no temporary file is left in the folder
