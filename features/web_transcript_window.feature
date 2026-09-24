Feature: A long conversation in the web UI is held as a sliding window
  A session with thousands of messages froze the web UI, worst of all on an
  old phone: the whole history was read, mapped and rendered at once. The web
  UI reads the newest page, renders a bounded slice of it, reads older pages
  as the reader scrolls up to them, and lets them go once the reader is back
  at the newest message. What the reader sees - the rows, their order, the
  index an edit rewinds by - is what a whole read would show.

  Scenario: A long session opens on its newest page
    Then a long session opens on its newest page, numbered as the whole history

  Scenario: Older pages arrive in front while scrolling up, and are let go at the newest message
    Then the page above arrives in front, and returning to the newest message lets it go

  Scenario: Pages read from the end join into the whole history
    Then pages read from the end join into what a whole read shows

  Scenario: An edit in a partly held history rewinds the right prompt
    Then an edit names the prompt by the server's index when the list holds only the end of the history

  Scenario: The top of a partly held transcript offers what is above it
    Then the top of a transcript with history above offers it, says it is loading, and retries
