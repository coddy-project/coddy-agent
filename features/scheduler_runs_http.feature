Feature: The HTTP surface exposes the runs of a scheduler job
  A run of a scheduler job is a background task of the job's session, so the runs
  panel of the web UI polls the background tasks route of that session, the same
  route the chat's Tasks panel polls. The scheduler routes answer with the ids the
  panel needs, list the runs from the job's side, clear them, and the run's
  transcript is a read-only child session that names its job.

  Scenario: A manual run answers with its task and its run session
    Given a running coddy serve server with the scheduler and a job "nightly"
    When I POST a run for the job "nightly"
    Then the API answers 202 with a task id, the job session and a run session
    And the job row of "nightly" reports it running with that job session

  Scenario: The runs of a job are the background tasks of its session
    Given a running coddy serve server with the scheduler and a job "nightly"
    And the job "nightly" ran once and the model answered "REPORT: nightly done"
    When I GET the runs of the job "nightly"
    Then the runs list one finished run with status "succeeded" and trigger "manual"
    And the background tasks of the job session list the same task of kind "agent"
    And the messages of the run session are read-only and name the scheduler job "nightly"
    And the messages of the run session contain "REPORT: nightly done"

  Scenario: The runs panel's Stop cancels the run through the job session
    Given a running coddy serve server with the scheduler and a job "slow"
    And a run of "slow" is in flight
    When I POST a stop for the run's background task on the job session
    Then the runs list one run with status "stopped"
    And the job row of "slow" reports it not running

  Scenario: Clearing the runs removes the transcripts with the task records
    Given a running coddy serve server with the scheduler and a job "nightly"
    And the job "nightly" ran once and the model answered "REPORT: nightly done"
    When I DELETE the runs of the job "nightly"
    Then the API answers 200 with 1 cleared
    And the runs of "nightly" are empty
    And the run session bundle is gone

  Scenario: A prompt against the job session is refused
    Given a running coddy serve server with the scheduler and a job "nightly"
    And the job "nightly" ran once and the model answered "REPORT: nightly done"
    When I POST a prompt to the job session
    Then the API answers 409 naming the scheduler job "nightly"
