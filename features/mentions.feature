Feature: "@" mentions in a prompt
  A prompt points at things with "@": a file anywhere on disk, a folder, a
  line range, another session, a subagent, a web page. Each reference is
  resolved once, when the message is sent, into an attachment of that same
  message, and the same resolver runs whichever surface the prompt came from:
  the console, the web UI, an editor over ACP or a messenger. A reference that
  names nothing stays prose; one that names something that cannot be inlined
  is still attached, with the reason, and never fails the turn.

  Background:
    Given a workspace with the files:
      | path              | content               |
      | README.md         | README_TOKEN          |
      | src/app.go        | APP_TOKEN             |
      | src/util/help.go  | HELP_TOKEN            |
      | notes/my draft.md | DRAFT_TOKEN           |

  Scenario: A file outside the workspace, named by its absolute path
    Given a file outside the workspace holding "OUTSIDE_TOKEN"
    When the user sends "compare with @<outside file>"
    Then the prompt attaches the outside file holding "OUTSIDE_TOKEN"

  Scenario: A file in the home folder, named with "~"
    Given a file "notes.txt" in the home folder holding "HOME_TOKEN"
    When the user sends "read @~/notes.txt first"
    Then the prompt attaches a file holding "HOME_TOKEN" mentioned as "~/notes.txt"

  Scenario: A mention that ends a sentence
    When the user sends "Summarize @README.md."
    Then the prompt attaches "README.md" holding "README_TOKEN"

  Scenario: A quoted path with a space in it
    When the user sends "fix the typos in @"notes/my draft.md" please"
    Then the prompt attaches "notes/my draft.md" holding "DRAFT_TOKEN"

  Scenario: A package name names no file and stays text
    When the user sends "npm install @google/genai and wire it into @README.md"
    Then the prompt attaches only "README.md" holding "README_TOKEN"

  Scenario: The composer marks only what a sent prompt would attach
    When the composer checks the draft "npm install @google/genai, compare @src/app.go notes, then read @README.md"
    Then the check marks "@README.md" as a mention of a file
    And the check marks "@src/app.go" as a mention of a file
    And the check leaves "@google/genai" unmarked

  Scenario: A folder is attached as its listing
    When the user sends "what lives in @src/ ?"
    Then the prompt attaches the folder "src/" listing "app.go" and "util/help.go"

  Scenario: Another session is attached as a digest of its conversation
    Given an earlier session where the user wrote "move the tokens to vault" and the assistant answered "The tokens live in vault now"
    When the user sends "continue what we did in @session:<earlier session>"
    Then the prompt attaches the earlier session holding "move the tokens to vault" and "The tokens live in vault now"

  Scenario: A subagent the user names
    When the user sends "@agent:explore find where the tokens are parsed"
    Then the prompt attaches the subagent "explore" asking to hand the work to spawn_agent

  Scenario: A web page the user names
    Given a web page "https://example.com/guide" reading "GUIDE_TOKEN"
    When the user sends "follow @https://example.com/guide."
    Then the prompt attaches "https://example.com/guide" holding "GUIDE_TOKEN"

  Scenario: A section of Coddy's own documentation
    When the user sends "how do ranges work? see @coddy:features/mentions#what-a-prompt-can-mention"
    Then the prompt attaches the documentation "features/mentions#what-a-prompt-can-mention" holding "## What a prompt can mention"

  Scenario: The composer marks a page of the documentation and leaves a page that is not there
    When the composer checks the draft "compare @coddy:features/mentions with @coddy:features/nowhere"
    Then the check marks "@coddy:features/mentions" as a mention of a doc
    And the check leaves "@coddy:features/nowhere" unmarked

  Scenario: A rule of a subproject's Coddy rule folder, named with its folder
    Given the subproject "db" keeps a Coddy rule "deploy" to apply by hand, reading "DEPLOY_TOKEN"
    When the user sends "ship the migration the way @rule:db/deploy says"
    Then the prompt attaches "db/.coddy/rules/deploy.mdc" holding "DEPLOY_TOKEN"

  Scenario: The composer offers the rules of a subproject under its folder
    Given the subproject "db" keeps a Coddy rule "deploy" to apply by hand, reading "DEPLOY_TOKEN"
    When the composer completes "@rule:"
    Then the completion offers "@rule:db/" to look into
    When the composer completes "@rule:db/"
    Then the completion offers "@rule:db/deploy"
    When the composer checks the draft "follow @rule:db/deploy"
    Then the check marks "@rule:db/deploy" as a mention of a rule

  Scenario: A rule in a folder of the workspace's rule folder, named by its path there
    Given the workspace keeps a Coddy rule "frontend/style" to apply by hand, reading "FRONTEND_STYLE_TOKEN"
    And the workspace keeps a Coddy rule "backend/style" to apply by hand, reading "BACKEND_STYLE_TOKEN"
    When the composer completes "@rule:"
    Then the completion offers "@rule:frontend/" to look into
    When the composer completes "@rule:frontend/"
    Then the completion offers "@rule:frontend/style"
    When the user sends "lay it out the way @rule:frontend/style says"
    Then the prompt attaches ".coddy/rules/frontend/style.mdc" holding "FRONTEND_STYLE_TOKEN"
