# Web search

The `websearch` tool asks several search engines at once and merges what comes back, and the `webfetch` tool downloads one page and returns it as Markdown. Together they are how the agent reads something that is not in the repository.

Search engines do not treat a program the way they treat a browser. One answers with a challenge page, one renders its results in the browser and serves a document with nothing in it, and one answers a query it dislikes with a full page of well-formed results about a different subject entirely. A merged answer that hides which of those happened is worse than no answer: the model reads an empty list as "the web has nothing on this" and stops looking. So every engine reports its own outcome next to the results.

## What comes back

```json
{
  "query": "golang context cancellation",
  "page": 1,
  "engines": [
    { "engine": "brave", "status": "ok", "results": 12, "took_ms": 620 },
    { "engine": "bing", "status": "blocked", "reason": "decoy: none of the results share a word with the query", "took_ms": 710 }
  ],
  "results": [
    {
      "title": "Canceling in-progress operations - The Go Programming Language",
      "url": "https://go.dev/doc/database/cancel-operations",
      "description": "You can manage in-progress operations by using Go context.Context...",
      "source": "brave"
    }
  ],
  "hint": "More results are available: call websearch again with page incremented, or narrow the query."
}
```

Every engine carries one of four outcomes.

`ok` - the engine answered and its rows were parsed. `results` counts what it contributed to the merged answer, so a page another engine had already supplied is not counted twice;
`empty` - the engine answered a result page the parser understood and there was nothing on it. This is the only outcome that honestly means the web has nothing under those words;
`blocked` - the engine answered something that is not a result page: a non-2xx status, a challenge, a layout the parser no longer recognises, or a batch of rows unrelated to the query. `reason` says which;
`error` - the engine could not be reached at all.

When **every** engine is blocked the call fails rather than returning an empty list, and the error names each engine and its reason. That is the difference the whole design turns on: a failure of the search is reported as a failure of the search, not as a fact about the world.

`cached: true` marks an answer reused from a recent identical search rather than asked again.

## Engines

| Engine | Key needed | Notes |
|--------|-----------|-------|
| `brave` | no (optional) | Default and first in the merge order. Reads the public result page; with `brave_api_key` it uses the official Search API instead, which has no parser to break. |
| `bing` | no | Default, second. Serves an unrelated result set to a client it dislikes, which the relevance gate below catches. |
| `searxng` | no, but needs `searxng_url` | Your own instance, asked over its JSON API. The durable choice: not rate-limited against its owner, never served a decoy. |
| `ddg` | no | Not asked by default. From a server, both its endpoints answer every query with an HTTP 202 anti-bot page. |
| `google` | no | Not asked by default. The page served to an unapproved client carries no organic results for any user agent; the results are rendered in the browser. |

Results merge in the order the engines are listed, deduplicated by normalised URL - the same page reached through `www`, over `http`, with a tracking parameter or a trailing slash is one row, and the URL handed to the model is the one its engine returned.

### The relevance gate

Bing answers a client it dislikes with a page whose title echoes the query and whose ten rows are about something else: pizza toppings for a question about Go, a tourism site for a question about a GitHub repository, and a different subject again on the next request. Nothing downstream can tell such a row from a real one.

So a batch is judged as a whole. When an engine returns at least three rows and not one of them shares a word with the query - after lowercasing, dropping words too common to carry a subject, and folding endings so `cancellation` meets `cancel` - the engine did not answer the query, and its whole batch is discarded with `status: blocked`, `reason: decoy`.

The judgement is deliberately batch-wide. One row that misses every query word is ordinary: a page titled "Terminating programs" is a fine answer to a question about killing a process. A whole page of them is not.

## Configuring it

```yaml
tools:
  websearch:
    engines: [brave, bing]        # merge order; brave, bing, ddg, google, searxng
    engine_timeout_seconds: 8     # per-engine budget
    total_timeout_seconds: 20     # budget for the whole search
    max_concurrent_engines: 4     # how many engines are asked at once
    snippet_chars: 320            # per-result description cap
    cache_ttl_seconds: 300        # reuse an engine answer for this long; negative = off
    searxng_url: ""               # e.g. http://localhost:8080
    brave_api_key: ""             # official Brave Search API
```

An engine name the loader does not know is a configuration error rather than a silently skipped backend, and `searxng` without `searxng_url` is refused the same way - `coddy -t` reports both with the line they are on.

### Your own SearXNG

A self-hosted [SearXNG](https://docs.searxng.org/) is the answer to every way a scraped engine fails. It aggregates the engines itself, it is not rate-limited against the machine that owns it, and it is never served a decoy.

```yaml
tools:
  websearch:
    engines: [searxng, brave]
    searxng_url: http://localhost:8080
```

The instance must serve the JSON API, which its default configuration does not: add `json` to `search.formats` in its `settings.yml`. A wrong setting there answers HTTP 403, which arrives as `blocked` with that reason rather than as an empty result list.

Unlike the URL `webfetch` is handed, this address comes from your own configuration rather than from the model, and a self-hosted instance normally listens on `localhost` or a LAN address - so private ranges are allowed on purpose. What is refused is the link-local metadata range (`169.254.0.0/16`), where no SearXNG listens and where a mistyped value would turn a search into a credential read.

### Caching

One engine's answer to one query is remembered for `cache_ttl_seconds` (300 by default), so a model that reaches for the same search twice does not make the engine answer twice. A blocked answer is remembered for 30 seconds at most, which is long enough to stop a retry inside one turn and short enough that a recovering engine is noticed. An error that is not a block - a refused connection - is not remembered at all.

## Arguments

| Argument | Meaning |
|----------|---------|
| `query` | Required. The search terms, in the language the answer is likely written in. |
| `page` | Result page, from 1. Goes further down the same result list. |
| `max_results` | Rows to return for this page; 15 by default, 25 at most. |
| `site` | Restrict to one domain, e.g. `go.dev`. Folded into each engine's own syntax. |

## Reading a page

`websearch` returns snippets, and a snippet is not an answer. `webfetch` takes one URL and returns the article as Markdown through readability extraction, refusing private networks and localhost - see [Tools](../reference/tools.md).

## Related

[Tools](../reference/tools.md) - every built-in tool and its arguments;
[Configuration](../getting-started/configuration.md) - where `config.yaml` lives and how to check it;
[Operating modes](modes.md) - `websearch` and `webfetch` are offered in every mode, including the read-only `ask`.
