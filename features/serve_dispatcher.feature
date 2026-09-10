Feature: coddy serve keeps itself running in the background
  A daemon that dies with the terminal it was started from, or stays down after a
  crash at three in the morning, is one an operator has to babysit. `coddy serve
  --daemon` detaches and leaves a dispatcher behind: the surfaces run in a worker
  process, and the dispatcher brings a fresh worker back whenever that one goes
  away for any reason other than being told to stop.

  It never gives up on the worker. A surface that fails the moment it starts - a
  port somebody else holds, a database that is not up yet - is waited out with a
  growing pause rather than retried in a tight loop, and the pause goes back to
  its shortest value once a worker has managed to stay up.

  The one restart the daemon performs on purpose is a configuration change the
  running process cannot adopt. A bot token is rotated inside the live process,
  but a bind address is not: the listener is what the operator is talking
  through. The worker exits asking for a replacement, and the dispatcher starts
  one that reads the new address.

  Scenario: a worker that dies is replaced
    Given a dispatcher supervising a worker
    When the worker fails
    Then the dispatcher starts another worker

  Scenario: a worker that is told to stop is not replaced
    Given a dispatcher supervising a worker
    When the worker exits cleanly
    Then the dispatcher starts no further worker

  Scenario: a worker that keeps failing is waited out, not spun
    Given a dispatcher supervising a worker
    When the worker fails 4 times without staying up
    Then each restart waits longer than the one before it
    And the dispatcher is still supervising

  Scenario: a worker that stayed up starts over from the shortest wait
    Given a dispatcher supervising a worker
    When the worker fails 3 times without staying up
    And a worker stays up before failing again
    Then the last restart waits the shortest time again

  Scenario: a bind address the configuration moved restarts the process
    Given a running runtime with the httpserver enabled under a dispatcher
    When the HTTP bind address is changed through the configuration
    Then the runtime asks its dispatcher for a restart

  Scenario: a bind address that moved without a dispatcher keeps the process
    Given a running runtime with the httpserver enabled in the foreground
    When the HTTP bind address is changed through the configuration
    Then the "httpserver" subsystem keeps running

  Scenario: a running dispatcher can be found again
    Given a dispatcher that recorded itself under the agent home
    When another coddy looks for a running dispatcher
    Then it reports the dispatcher's process and the configuration it was started with
