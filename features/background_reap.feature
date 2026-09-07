Feature: Reaping background processes left behind by an earlier run
  A coddy that is drained stops the background tasks it started. A coddy that is killed
  does not, and the shell trees it spawned keep running with nobody supervising them:
  holding a port, holding a lock, burning CPU. The task record persists the process group
  leader pid, so a fresh coddy reading the same session bundle can tell a record whose
  processes are gone from one whose processes are still on this machine. background_list
  marks the survivors, and background_reap kills all of a session's leftovers at once.

  Reaching for a pid recorded by a process that is no longer around is only safe while the
  pid still means what the record says it means, so the probe has to answer "is the task's
  process still running", not "does something answer to this number".

  Scenario: A fresh coddy finds and reaps a process left behind by an earlier run
    Given an earlier coddy run left a background task running
    When a fresh coddy takes over the same session bundle
    Then the task listing marks that task as still alive from an earlier run
    When I reap the leftover background processes
    Then the reaping reports that task
    And the leftover process is gone

  Scenario: Reaping a session with nothing left behind kills nothing
    Given a session with an empty background task pool
    When I reap the leftover background processes
    Then the reaping reports that there was nothing to kill

  Scenario: A task this coddy is still supervising is not a leftover
    Given a session with an empty background task pool
    And this coddy started a background task of its own
    When I list the background tasks
    Then the listing does not offer that task for reaping
    When I reap the leftover background processes
    Then the reaping reports that there was nothing to kill
    And that task is still running
