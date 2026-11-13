Feature: Provider usage over the OpenAI-compatible surface
  A Coddy deployment that fronts metered upstreams lets OpenAI-type clients
  read the account quota instead of learning about it from a 429. The server
  answers GET /v1/limits in the NeuralDeep hub's schema-1 shape - the one
  clients already read - and embeds the whole provider usage update in a
  coddy member, so a Coddy client loses nothing of what its own surfaces
  show. The same contract works in the other direction: an openai provider
  row pointed at a limits-capable upstream (another Coddy or a NeuralDeep
  hub) gets a usage source, read once and cached like every other provider;
  an upstream without a limits endpoint is marked unsupported after a single
  probe instead of being asked again on every read.

  @http
  Scenario: An OpenAI-type client reads the account usage over GET /v1/limits
    Given a coddy HTTP server with an openai provider backed by a stand-in limits API
    When an OpenAI-type client asks for the limits over /v1/limits
    Then the limits answer is schema 1 and names the plan "pro" with the session window at 3%
    And the limits answer carries the upstream provider usage in its coddy field
    And the stand-in limits API was asked with the row's key

  @http
  Scenario: The limits endpoint selects the provider a query names
    Given a coddy HTTP server with an openai provider backed by a stand-in limits API
    When an OpenAI-type client asks for the limits of provider "hub" over /v1/limits
    Then the limits answer is schema 1 and names the plan "pro"
    When an OpenAI-type client asks for the limits of model "stub/model" over /v1/limits
    Then the limits answer is a 404

  @http
  Scenario: An openai provider row reads the usage of its limits-capable upstream
    Given a coddy HTTP server with an openai provider backed by a stand-in limits API
    When I read the usage of the "hub" provider over REST
    Then the usage names the plan "pro" and the provider type of the upstream account
    And the usage carries the reset times and the wallet balance of the account

  @http
  Scenario: An openai provider whose upstream has no limits endpoint says so once
    Given a coddy HTTP server with an openai provider backed by an upstream without a limits endpoint
    When I read the usage of the "hub" provider over REST
    Then the usage answer marks the provider as unsupported
    When the usage of the "hub" provider is read again after the cache would have expired
    Then the usage answer marks the provider as unsupported
    And the stand-in limits API was asked only once
