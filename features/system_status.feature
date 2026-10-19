Feature: System status
  As an operator I open Settings -> System and read what this installation is,
  how it was started, what runs, how much room the sessions disk has and what
  needs my attention, from one document the server builds: GET /coddy/status.

  Scenario: the System page reads what this installation is and what needs attention
    Given a coddy serve with the HTTP API on a free port and the sessions disk reported below 512 MiB
    When the web UI reads "/coddy/status"
    Then it names the version and the build tags of this binary and how it was started
    And the attention list names the low disk
    And it lists the HTTP API as running on that port

  @ui
  Scenario: the System tile says how many things need attention
    Given a server whose status names a low disk and a pending restart
    When the operator opens Settings on a phone
    Then the System tile reads "2 need attention"
    And Settings asked for the status once
