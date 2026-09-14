Feature: The documentation checks leave alone what git ignores
  docs/ is not only the published tree. Local scratch lands there too - the
  repository's own .gitignore keeps docs/superpowers/ out of git - and the
  generator walked it all the same: every local plan came back as a page
  missing from docs/nav.yaml and every link inside one as a broken link. The
  publication then wrote the site layer and exited non-zero anyway, which reads
  as a failed deploy and is not one.

  The walk asks git what it excludes and skips it. Nothing else moves: a page
  the map really is missing is still reported, a broken link on a page of the
  map is still reported, and the design records of docs/plans - tracked files,
  deliberately outside the map - keep leaving it through their own rule.

  Background:
    Given a repository whose .gitignore excludes "docs/superpowers/"
    And the page "docs/features/hooks.md" listed in docs/nav.yaml

  Scenario: A page git ignores is not a page missing from the map
    Given the file "docs/superpowers/plans/selector.md" which docs/nav.yaml does not list
    When the documentation checks run
    Then the documentation checks pass

  Scenario: A broken link inside a page git ignores is nobody's contract
    Given the file "docs/superpowers/plans/selector.md" which docs/nav.yaml does not list, linking to "nowhere.md"
    When the documentation checks run
    Then the documentation checks pass

  Scenario: A page the map really is missing is still reported
    Given the file "docs/features/orphan.md" which docs/nav.yaml does not list
    When the documentation checks run
    Then "docs/features/orphan.md" is reported as not listed in docs/nav.yaml

  Scenario: A broken link on a page of the map is still reported
    Given the page "docs/features/plan.md" listed in docs/nav.yaml and linking to "nowhere.md"
    When the documentation checks run
    Then "docs/features/plan.md" is reported as carrying a broken link

  Scenario: The design records keep leaving the map through their own rule
    Given the file "docs/plans/selector.md" which docs/nav.yaml does not list
    When the documentation checks run
    Then the documentation checks pass
