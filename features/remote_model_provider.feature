Feature: A model shared by a remote Coddy serves another Coddy's harness
  A remote coddy serve offers models[] rows to other Coddys under an alias
  (models[].shared_as). A local Coddy lists them through a provider of type
  coddy and keeps everything of its own harness: the system prompt, the tool
  definitions, the history and the tool-call loop. Each model turn is one
  stateless call to the remote, always answered as a stream, which runs the
  provider the row is configured with. Nothing of the call is stored on the
  remote, and neither the provider name nor the upstream model id leaves its
  host: the alias is the only name on the wire. A credential made for shared
  models opens these routes and nothing else. At most five such calls run at
  once per credential, and a local Coddy waits out a busy remote for a bounded
  time instead of failing at once. A remote without authentication offers
  nothing unless its operator has said httpserver.allow_insecure, and a node
  that holds only shared-model tokens refuses everything else to everyone. Scenarios tagged @phase2 belong to the second step of
  docs/plans/remote-model-provider.md.

  Scenario: The remote lists only the models it shares, under their aliases
    Given a remote coddy sharing "stub/qwen3-secret" as "coder" with a 200000 token context and the reasoning levels "low,high"
    And the remote also has a model "stub/private-model" that it does not share
    When a client lists the shared models of the remote
    Then the listing names exactly "coder"
    And the listing never mentions "qwen3-secret", "private-model" or "stub"

  Scenario: The local coddy sizes the model by the context window the remote lists
    Given a remote coddy sharing "stub/qwen3-secret" as "coder" with a 200000 token context and the reasoning levels "low,high"
    And a local coddy with a provider "remote" of type coddy pointing at the remote
    When the local coddy adds the model "remote/coder" without a context window
    Then GET /v1/models of the local coddy reports "remote/coder" with a context of 200000 tokens

  @phase2
  Scenario: The local coddy offers the reasoning levels the remote lists
    Given a remote coddy sharing "stub/qwen3-secret" as "coder" with a 200000 token context and the reasoning levels "low,high"
    And a local coddy with a provider "remote" of type coddy pointing at the remote
    When the local coddy adds the model "remote/coder" without reasoning levels
    Then GET /v1/models of the local coddy reports "remote/coder" with the reasoning levels "low, high"

  @phase2
  Scenario: The local coddy offers images when the remote lists the model as multimodal
    Given a remote coddy sharing "stub/qwen3-secret" as "coder" that is multimodal
    And a local coddy with a provider "remote" of type coddy pointing at the remote
    When the local coddy adds the model "remote/coder" without declaring it multimodal
    Then GET /v1/models of the local coddy reports "remote/coder" as multimodal
    And the local coddy lets a user attach an image to a message sent to "remote/coder"

  Scenario: A completion streams from the remote model and leaves no session behind
    Given a remote coddy sharing "stub/qwen3-secret" as "coder" whose model answers "Hello from the remote." and reports 120 input tokens, 8 output tokens and 100 cached tokens
    And a local coddy with a provider "remote" of type coddy pointing at the remote
    When the local coddy streams "remote/coder" with the system prompt "You are the local harness." and the message "Hi"
    Then the local coddy assembles the answer "Hello from the remote."
    And the remote model received the system prompt "You are the local harness." and the message "Hi"
    And the usage the local coddy reads is 120 input tokens, 8 output tokens and 100 cached tokens
    And the remote holds no session
    And nothing the remote sent mentions "qwen3-secret"

  Scenario: A tool call from the remote model reaches the local harness
    Given a remote coddy sharing "stub/qwen3-secret" as "coder" whose model calls "get_weather" with {"city":"Paris"} under the call id "call_weather_1"
    And a local coddy with a provider "remote" of type coddy pointing at the remote
    When the local coddy streams "remote/coder" offering its own tool "get_weather"
    Then the remote model was offered the tool "get_weather" with the schema the local coddy declared
    And the local coddy receives the tool call "get_weather" with arguments {"city":"Paris"} under the id "call_weather_1"
    And the remote executed no tool

  Scenario: The tool result goes back to the remote model and the answer comes out
    Given a remote coddy sharing "stub/qwen3-secret" as "coder" whose model answers "It is 18°C in Paris." once it sees a tool result
    And a local coddy with a provider "remote" of type coddy pointing at the remote
    When the local coddy streams "remote/coder" with the message "Weather in Paris?", the assistant call "get_weather" with {"city":"Paris"} under the id "call_weather_1" and the tool result "18°C"
    Then the remote model received the tool result "18°C" under the call "call_weather_1", after that assistant call
    And the local coddy assembles the answer "It is 18°C in Paris."

  Scenario: The local harness drives a shared model through a whole tool turn
    Given a remote coddy sharing "stub/qwen3-secret" as "coder" whose model calls "get_weather" with {"city":"Paris"} and then answers "It is 18°C in Paris." once it sees the result
    And a local coddy with a provider "remote" and a local tool "get_weather" that returns "18°C"
    When the local coddy runs a turn on "remote/coder" with the prompt "Weather in Paris?"
    Then the local tool "get_weather" ran once, on the local coddy, with {"city":"Paris"}
    And the local coddy's answer is "It is 18°C in Paris."
    And the remote model received the local coddy's system prompt
    And the remote executed no tool and holds no session

  Scenario: Reasoning and its signature survive the hop and come back on the next turn
    Given a remote coddy sharing "stub/qwen3-secret" as "coder" whose model reasons "Check the file first." with the signature "sig-abc"
    And a local coddy with a provider "remote" of type coddy pointing at the remote
    When the local coddy streams "remote/coder" with the message "Hi"
    Then the local coddy receives the reasoning "Check the file first." with a non-empty signature
    When the local coddy streams "remote/coder" again with that assistant message in its history
    Then the remote model received the reasoning signature "sig-abc" unchanged

  Scenario: A signature of an earlier model never reaches the provider after the alias is reassigned
    Given a remote coddy sharing "stub/qwen3-secret" as "coder" whose model reasons "Check the file first." with the signature "sig-abc"
    And a local coddy with a provider "remote" of type coddy pointing at the remote
    When the local coddy streams "remote/coder" with the message "Hi"
    And the remote operator points "coder" at another model
    And the local coddy streams "remote/coder" again with that assistant message in its history
    Then the remote model did not receive the signature "sig-abc"
    And the call succeeds

  Scenario: A request made on a stale view is refused before the provider is called and answered after a refresh
    Given a remote coddy sharing "stub/qwen3-secret" as "coder" with a 200000 token context whose model answers "Done after the refresh."
    And a local coddy with a provider "remote" of type coddy pointing at the remote
    And the local coddy has read the listing of the remote
    When the remote operator changes the context window of "coder" to 100000 tokens
    And the local coddy streams "remote/coder" with the message "Hi"
    Then the remote answers "stale_revision" before the provider is called
    And the local coddy refreshes its view of "coder" and sends the request once more
    And the local coddy assembles the answer "Done after the refresh."
    And the provider was called once

  @phase2
  Scenario: Reasoning levels narrowed on the remote are refused once and the next request succeeds
    Given a remote coddy sharing "stub/qwen3-secret" as "coder" with the reasoning levels "low,high" whose model answers "Answered at low."
    And a local coddy with a provider "remote" of type coddy pointing at the remote
    And the local coddy has read the listing of the remote
    When the remote operator narrows "coder" to the reasoning level "low"
    And the local coddy streams "remote/coder" at the reasoning level "high"
    Then the remote answers "stale_revision" before the provider is called
    And the local coddy falls back to the level "low" and the provider received "low"
    And the local coddy assembles the answer "Answered at low."

  @phase2
  Scenario: A key written in the local row is not overwritten by a refresh
    Given a remote coddy sharing "stub/qwen3-secret" as "coder" with a 200000 token context
    And a local coddy with a provider "remote" of type coddy pointing at the remote
    And the local row "remote/coder" sets max_context_tokens 64000
    When the local coddy refreshes its view of the remote listing
    Then GET /v1/models of the local coddy reports "remote/coder" with a context of 64000 tokens
    And the local configuration file still says 64000

  Scenario: The requested reasoning level reaches the provider
    Given a remote coddy sharing "stub/qwen3-secret" as "coder" with the reasoning levels "low,high"
    And a local coddy with a provider "remote" of type coddy pointing at the remote
    And the local row "remote/coder" has the reasoning levels "low,high"
    When the local coddy streams "remote/coder" at the reasoning level "high"
    Then the remote model's provider received the reasoning level "high"

  @phase2
  Scenario: The reasoning level off reaches the provider when the remote allows it
    Given a remote coddy sharing "stub/qwen3-secret" as "coder" that allows reasoning off
    And a local coddy with a provider "remote" of type coddy pointing at the remote
    When the local coddy streams "remote/coder" at the reasoning level "off"
    Then the remote model's provider received the reasoning level "off"

  Scenario: An image in the history reaches a vision model as an image
    Given a remote coddy sharing "stub/qwen3-secret" as "coder" that is multimodal and whose model answers "A red square."
    And a local coddy with a provider "remote" of type coddy pointing at the remote
    When the local coddy streams "remote/coder" with a text part and an image part
    Then the remote model received one image part after the text part, with the same MIME type and the same bytes
    And the local coddy assembles the answer "A red square."

  @phase2
  Scenario: The account usage of the remote provider is read through the hop
    Given a remote coddy sharing "stub/qwen3-secret" as "coder" whose provider reports 62 percent of its quota used
    And a local coddy with a provider "remote" of type coddy pointing at the remote
    When the local coddy reads the usage of the model "remote/coder"
    Then it reports 62 percent of the quota used
    And the remote marks the reading as account-wide
    And the document never mentions the upstream model id, the provider name, the key name or any amount of money

  Scenario: A token made for shared models reaches them and nothing else
    Given a remote coddy with a main token, sharing "stub/qwen3-secret" as "coder", and a shared-model token
    When a client holding the shared-model token lists the shared models of the remote
    Then the listing names exactly "coder"
    When the same client asks the remote for its sessions
    Then the remote refuses it as unauthorized
    When the same client posts to "/v1/chat/completions" for "stub/qwen3-secret"
    Then the remote refuses it as unauthorized
    When the same client posts to "/coddy/llm/completions" for the selector "stub/qwen3-secret" instead of the alias
    Then the remote answers 404
    And a client holding the main token still reaches the sessions of the remote

  Scenario: A remote that holds only shared-model tokens refuses everything else to everyone
    Given a remote coddy with no main token and no web login, sharing "stub/qwen3-secret" as "coder", and a shared-model token
    When a client holding the shared-model token lists the shared models of the remote
    Then the listing names exactly "coder"
    When a client holding the shared-model token asks the remote for its sessions
    Then the remote refuses it as unauthorized
    When a client holding no token asks the remote for its sessions
    Then the remote refuses it as unauthorized

  Scenario: A remote without authentication offers no shared model
    Given a remote coddy with no token and no web login, sharing "stub/qwen3-secret" as "coder"
    When a client lists the shared models of the remote
    Then the remote answers 403 with the kind "auth"
    And the answer says that shared models need authentication

  Scenario: Five streams run at once and a sixth is told to retry
    Given a remote coddy sharing "stub/qwen3-secret" as "coder" whose model holds every stream open until released
    And the remote allows 5 shared-model streams at once
    When 5 streams are requested from "coder"
    Then all 5 streams are running on the remote
    When a sixth stream is requested from "coder"
    Then the remote answers 429 with the kind "busy" and a Retry-After of 1 second
    And the provider was not called for the sixth
    When one of the 5 streams ends
    And a sixth stream is requested from "coder"
    Then the sixth stream is served

  Scenario: A local coddy waits for a free slot instead of failing
    Given a remote coddy sharing "stub/qwen3-secret" as "coder" whose model answers "Done after the wait."
    And the remote allows 1 shared-model stream at once and one stream is held open
    And a local coddy with a provider "remote" of type coddy pointing at the remote with a busy wait of 5 seconds
    When the local coddy streams "remote/coder" with the message "Hi" and the held stream ends once the local coddy has been told busy
    Then the local coddy assembles the answer "Done after the wait."
    And the provider was called once for the local coddy's request
