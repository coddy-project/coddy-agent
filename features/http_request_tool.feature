Feature: The agent sends HTTP requests it shapes itself
  webfetch reads one public page and hands back its article text, which is the
  right answer for reading and the wrong one for everything else an agent does
  over HTTP: calling an API, creating an object, uploading a file, reaching the
  dev server it has just started on localhost. "http_request" is the curl of the
  tool set. The model chooses the method, the query parameters and every header,
  and sends a raw, base64, JSON, urlencoded or multipart body, or a workspace
  file as the body. The answer comes back the way `curl -i` prints it: the status
  line, the response headers, then the body, or the path the body was saved to.

  Because such a request changes things somewhere else and can carry anything
  the agent has read, it goes through the permission gate. Under "ask" the
  operator is shown where the request goes and what it carries - the method and
  the address, the headers, the body, the local files it uploads and the file it
  would write - and can allow it once, allow that address or that whole origin
  for the rest of the session, or refuse it. A file is part of what is being
  approved: allowing an origin covers requests to it, not uploads of files the
  operator never saw. Addresses in tools.http_request.allowlist never ask, and
  "bypass" asks about nothing.

  Background:
    Given a local HTTP service the agent can reach

  Scenario: A JSON request with its own method, query and headers
    Given the permission mode is "bypass"
    When the model calls http_request with:
      """
      {
        "method": "PATCH",
        "url": "{service}/items/7?verbose=1",
        "query": {"tag": ["red", "blue"]},
        "headers": {"Authorization": "Bearer t0ken", "X-Trace": "abc-123"},
        "json": {"name": "lamp"}
      }
      """
    Then the service received "PATCH /items/7?verbose=1&tag=red&tag=blue"
    And the service received the header "Authorization: Bearer t0ken"
    And the service received the header "X-Trace: abc-123"
    And the service received the header "Content-Type: application/json"
    And the service received the body "{"name":"lamp"}"
    And the tool answered with the status line "HTTP/1.1 200 OK"
    And the tool answer contains "X-Service: echo"
    And the tool answer contains "patched item 7"

  Scenario: A workspace file travels as multipart form data next to a field
    Given the permission mode is "bypass"
    And a workspace file "report.txt" containing "quarterly numbers"
    When the model calls http_request with:
      """
      {
        "url": "{service}/upload",
        "form_data": [
          {"name": "title", "value": "Q3"},
          {"name": "doc", "file": "report.txt", "content_type": "text/plain"}
        ]
      }
      """
    Then the service received "POST /upload"
    And the service received the form field "title" with "Q3"
    And the service received the file "doc" named "report.txt" with "quarterly numbers"

  Scenario: A response body is saved to a file instead of the context
    Given the permission mode is "bypass"
    When the model calls http_request with:
      """
      {"url": "{service}/logo.png", "output_file": "assets/logo.png"}
      """
    Then the workspace file "assets/logo.png" holds the bytes the service sent
    And the tool answer contains "saved 8 bytes to"

  Scenario: The operator sees where a request goes and approves the whole origin
    Given the permission mode is "ask"
    And the operator answers permission prompts with "allow_always_origin"
    When the model calls http_request with:
      """
      {"method": "POST", "url": "{service}/items", "json": {"name": "lamp"}}
      """
    Then the operator was asked 1 time
    And the permission prompt shows "POST {service}/items"
    And the permission prompt shows "{"name":"lamp"}"
    And the permission dialog offers "Always allow {service}/items"
    And the permission dialog offers "Always allow {service}"
    And the service received "POST /items"
    When the model calls http_request with:
      """
      {"method": "DELETE", "url": "{service}/items/7"}
      """
    Then the operator was asked 1 time
    And the service received "DELETE /items/7"

  Scenario: An approved origin does not approve a file the operator never saw
    Given the permission mode is "ask"
    And the operator answers permission prompts with "allow_always_origin"
    And a workspace file "notes.txt" containing "private notes"
    When the model calls http_request with:
      """
      {"url": "{service}/items"}
      """
    Then the operator was asked 1 time
    When the model calls http_request with:
      """
      {"method": "PUT", "url": "{service}/files/notes", "body_file": "notes.txt"}
      """
    Then the operator was asked 2 times
    And the permission prompt shows "notes.txt"
    And the service received the body "private notes"

  Scenario: An address the operator allowlisted never asks
    Given the permission mode is "ask"
    And the operator allowlisted the service in tools.http_request.allowlist
    When the model calls http_request with:
      """
      {"method": "DELETE", "url": "{service}/items/7", "headers": {"X-Reason": "cleanup"}}
      """
    Then the operator was asked 0 times
    And the service received "DELETE /items/7"
