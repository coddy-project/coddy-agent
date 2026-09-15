Feature: Web search over several engines, honestly reported
  The websearch tool asks more than one search engine and merges what comes
  back. The engines it asks are not equally reliable: one answers with an
  anti-bot interstitial, another renders its results in the browser and serves
  a page with nothing in it, and a third answers a query it dislikes with ten
  well-formed results about an entirely different subject. A merged answer that
  hides which of those happened teaches the model that the web is empty, so
  every engine reports its own outcome next to the results.

  An engine is "ok" when it parsed rows, "empty" when it answered a page the
  parser understood and there was nothing on it, and "blocked" when it answered
  something else: a non-2xx status, a challenge page, a layout the parser no
  longer recognises, or a batch of results that shares no word with the query.
  Results merge in the order the operator configured the engines, deduplicated
  by normalised URL so the same page found twice is listed once.

  Background:
    Given the operator configured the search engines "brave, bing"

  Scenario: Results from several engines merge in configured order
    Given engine "brave" answers with:
      | title             | url                        | snippet            |
      | Context package   | https://pkg.go.dev/context | Package context... |
      | Go Concurrency    | https://go.dev/blog/context | Share by...       |
    And engine "bing" answers with:
      | title             | url                         | snippet         |
      | Context on GitHub | https://github.com/golang/go | The Go source. |
    When the agent searches the web for "golang context cancellation"
    Then the search returns 3 results
    And result 1 is "https://pkg.go.dev/context"
    And result 3 is "https://github.com/golang/go"
    And engine "brave" is reported as "ok" with 2 results
    And engine "bing" is reported as "ok" with 1 result

  Scenario: The same page found by two engines is listed once
    Given engine "brave" answers with:
      | title           | url                        | snippet            |
      | Context package | https://pkg.go.dev/context | Package context... |
    And engine "bing" answers with:
      | title           | url                                          | snippet |
      | Context package | https://www.pkg.go.dev/context/?utm_source=b | Same.   |
    When the agent searches the web for "golang context"
    Then the search returns 1 result
    And result 1 is "https://pkg.go.dev/context"

  Scenario: A blocked engine is named instead of counted as nothing found
    Given engine "brave" answers with:
      | title           | url                        | snippet            |
      | Context package | https://pkg.go.dev/context | Package context... |
    And engine "bing" is blocked with "anti-bot interstitial"
    When the agent searches the web for "golang context"
    Then the search returns 1 result
    And engine "bing" is reported as "blocked" because of "anti-bot interstitial"

  Scenario: An engine answering an unrelated subject is discarded as a decoy
    Given engine "brave" answers with:
      | title           | url                        | snippet            |
      | Context package | https://pkg.go.dev/context | Package context... |
    And engine "bing" answers with:
      | title                             | url                                | snippet              |
      | Explorateur de fichiers Windows   | https://support.microsoft.com/fr/1 | Ouvrir l'explorateur |
      | Reparer l'Explorateur de fichiers | https://support.microsoft.com/fr/2 | Si l'explorateur...  |
      | Visit Rainier Official Site       | https://visitrainier.com/          | Mount Rainier.       |
    When the agent searches the web for "golang context cancellation"
    Then the search returns 1 result
    And engine "bing" is reported as "blocked" because of "decoy"

  Scenario: Every engine blocked is an error, not an empty answer
    Given engine "brave" is blocked with "http 403"
    And engine "bing" is blocked with "anti-bot interstitial"
    When the agent searches the web for "golang context"
    Then the search fails
    And the failure names engine "brave" and its reason "http 403"
    And the failure names engine "bing" and its reason "anti-bot interstitial"

  Scenario: Engines that genuinely found nothing report an empty answer
    Given engine "brave" answers with no results
    And engine "bing" answers with no results
    When the agent searches the web for "zzqx nonexistent phrase"
    Then the search returns 0 results
    And the search succeeds
    And engine "brave" is reported as "empty"
