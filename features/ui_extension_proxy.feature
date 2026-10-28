Feature: Server-side HTTP requests from approved UI extensions
  Scenario: Call a local API without CORS and without leaking Coddy credentials
    Given an approved extension and a local API without CORS
    When the extension posts binary data through the server proxy
    Then the API receives the explicit headers and original bytes only
    And the extension receives the API status headers and binary response
    When the extension is disabled
    Then another proxy request is refused before reaching the API

