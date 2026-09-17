Feature: Scheduled jobs run as background subagent tasks
  A scheduler job is a markdown file with a cron schedule and an instruction. Its runs
  used to be sessions built by hand outside the session manager, with nothing owning
  them afterwards. Now a run is what a spawn_agent call starts: a task of kind agent in
  the background task pool, backed by a child session, both hanging under one session
  per job. The job session is the run history: the Tasks panel of that session lists the
  runs, opens their progress logs and their transcripts, and retention keeps the newest
  runs of every job.

  Scenario: A manual run is a background agent task under the job's session
    Given a scheduler with a job "nightly" whose instruction says "Report the marker"
    When the job "nightly" is run by hand and the model answers "REPORT: marker seen"
    Then the run is a task of kind "agent" under the job session of "nightly"
    And the job's sidecar names that job session
    And the run session is a child of the job session and belongs to job "nightly" by trigger "manual"
    And the run's transcript ends with "REPORT: marker seen"
    And the run's output log carries the report "REPORT: marker seen"
    And the job "nightly" is no longer running

  Scenario: A cron tick starts a due job and commits its checkpoint before the run
    Given a scheduler with a job "minute" scheduled "* * * * *"
    When the daemon ticks at "2026-09-18T10:00:00Z" and the model answers "tick done"
    Then the run of "minute" is labelled "minute · cron 2026-09-18 10:00 UTC"
    And the checkpoint of "minute" is "2026-09-18T10:00:00Z"
    And the run session belongs to job "minute" by trigger "cron"
    When the daemon ticks at "2026-09-18T10:00:00Z" again
    Then the job "minute" has 1 run

  Scenario: A running job is not started twice
    Given a scheduler with a job "slow" scheduled "* * * * *"
    And a run of "slow" is in flight
    When the job "slow" is run by hand
    Then the manual run is refused because the job is running
    When the daemon ticks at "2026-09-18T11:00:00Z"
    Then the job "slow" has 1 run
    When the run in flight is released
    Then the job "slow" is no longer running

  Scenario: Cancelling a running job stops its task
    Given a scheduler with a job "slow" scheduled "* * * * *"
    And a run of "slow" is in flight
    When the job "slow" is cancelled
    Then the run of "slow" is recorded as "stopped"
    And the job "slow" is no longer running
    And the run session of "slow" is no longer live

  Scenario: Retention keeps the newest finished runs of a job
    Given a scheduler with a job "chatty" retaining 2 runs
    When the job "chatty" is run by hand 3 times and the model answers "done"
    Then the job "chatty" lists 2 finished runs, newest first
    And the transcript of the oldest run is gone from disk
    And the task record of the oldest run is gone from disk

  Scenario: The job session is a read-only transcript
    Given a scheduler with a job "nightly" whose instruction says "Report the marker"
    And the job "nightly" was run by hand and the model answered "done"
    When a prompt is sent to the job session of "nightly"
    Then the prompt is refused because the session belongs to scheduler job "nightly"
    And the job session of "nightly" is hidden from the default session listing

  Scenario: A job under a definition runs with the definition's tools and role
    Given a user-scope subagent definition "reader" allowing only "read, grep" with the role "You only read."
    And a scheduler with a job "audit" running the agent "reader"
    When the job "audit" is run by hand and the model answers "read only"
    Then the run's model was offered "read" and not "run_command"
    And the run's system prompt names the scheduled job "audit" and carries the role "You only read."

  Scenario: Clearing the history removes finished runs and their transcripts
    Given a scheduler with a job "nightly" whose instruction says "Report the marker"
    And the job "nightly" was run by hand and the model answered "done"
    When the runs of "nightly" are cleared
    Then the job "nightly" has 0 runs
    And no run bundle is left under the job session of "nightly"

  Scenario: A job under an unapproved project definition does not start
    Given a workspace definition "reviewer" under .coddy/agents of the job's workspace
    And a scheduler with a job "audit" running the agent "reviewer"
    When the job "audit" is run by hand
    Then the manual run is refused because the definition is not approved
    And the job "audit" has 0 runs

  Scenario: A manual run past scheduler.max_queue is refused
    Given a scheduler with max_queue 1 and the jobs "first" and "second"
    And a run of "first" is in flight
    When the job "second" is run by hand
    Then the manual run is refused because scheduler.max_queue runs are in flight
    When the run in flight is released
    Then the job "first" is no longer running
