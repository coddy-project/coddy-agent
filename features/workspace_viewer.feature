Feature: Preview a session workspace through the HTTP surface
  The Files dock reads the session's own workspace, including files larger than
  the old text preview limit. Native media uses a scoped URL without API headers.

  Scenario: Browse one directory without walking the whole workspace
    Given a file viewer server with a workspace
    And a workspace file "notes/readme.md" containing "# Notes\n"
    When I browse the directory "notes"
    Then the directory lists the file "readme.md"

  Scenario: Read a line window beyond 512 KiB
    Given a file viewer server with a workspace
    And a large workspace text file
    When I read the large file from line offset 99998
    Then the next line offset is 99999 and more lines remain

  Scenario: Stream a range using a scoped URL
    Given a file viewer server with a workspace
    And a workspace file "clip.txt" containing "hello world"
    When I mint a media URL for "clip.txt" using API authentication
    And I read the first five bytes using only that URL
    Then the response is a partial range containing "hello"
