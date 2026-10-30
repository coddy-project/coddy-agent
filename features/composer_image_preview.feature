Feature: Seeing an attached image in the web UI
  An image the operator attaches is a preview card, before and after it is
  sent: the picture fills it, a click opens it enlarged in the viewer the
  documentation reader uses, and in the composer the card keeps its remove
  control. The sent bubble opens the original bytes the session bundle still
  holds, and the server serves those bytes only when they really are an image.

  Scenario: An attached image can be looked at properly
    Then an image in the composer is a preview card that opens the picture enlarged
    And removing a preview card in the composer drops the attachment and opens nothing
    And an image in the sent bubble opens the full-size asset
    And a bubble sent before the full-size asset existed opens its preview instead

  Scenario: Pictures from a screenshot tool outlive its temporary files
    Then a dropped file is replaced by a copy held in memory
    And files dropped one after another keep their order whichever is read first
    And a file that cannot be read leaves the others and says which it was

  Scenario: A queued picture is a picture as soon as the agent reads it
    Then the row of a queued picture opens it from the session's assets at once
    And a server file with nothing to show yet keeps the picture the row has
