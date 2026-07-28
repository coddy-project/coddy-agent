Feature: Line-ending-safe file edits
  The edit tool treats LF, CRLF, and CR as equivalent when it matches oldString,
  and writes the result back using the file's own line-ending style. An agent can
  therefore edit a Windows (CRLF) file using ordinary LF tool arguments, and edit
  a Unix (LF) file with CRLF arguments, without rewriting the file's line endings
  or leaving a mix behind.

  Scenario: LF edit arguments modify a CRLF file and keep CRLF
    Given a file "config.js" with CRLF line endings and content:
      """
      const port = 3000;
      const host = "localhost";
      const debug = false;
      """
    When I replace in "config.js" using LF arguments the text:
      """
      const host = "localhost";
      const debug = false;
      """
    And with the text:
      """
      const host = "0.0.0.0";
      const debug = true;
      """
    Then the edit succeeds
    And "config.js" uses CRLF line endings
    And "config.js" contains "0.0.0.0"
    And "config.js" contains "const debug = true;"
    And "config.js" ends with a newline

  Scenario: CRLF edit arguments modify an LF file and keep LF
    Given a file "server.py" with LF line endings and content:
      """
      host = "localhost"
      port = 8000
      """
    When I replace in "server.py" using CRLF arguments the text:
      """
      host = "localhost"
      port = 8000
      """
    And with the text:
      """
      host = "0.0.0.0"
      port = 9000
      """
    Then the edit succeeds
    And "server.py" uses LF line endings
    And "server.py" contains "0.0.0.0"
    And "server.py" contains "port = 9000"

  Scenario: Replace-all across a CRLF file keeps CRLF
    Given a file "flags.ini" with CRLF line endings and content:
      """
      mode = draft
      mode = draft
      mode = draft
      """
    When I replace every occurrence in "flags.ini" using LF arguments the text:
      """
      mode = draft
      """
    And with the text:
      """
      mode = final
      """
    Then the edit succeeds
    And "flags.ini" uses CRLF line endings
    And "flags.ini" contains "mode = final"
    And "flags.ini" does not contain "mode = draft"
