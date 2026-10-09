# Parallel Search MCP

Give Coddy web search and page fetching with [Parallel Search MCP](https://docs.parallel.ai/integrations/mcp/search-mcp), using the existing HTTP MCP transport. The anonymous endpoint needs no Parallel account or API key and has lower rate limits intended for exploration and light use.

## Setup

Install Coddy using the [installation guide](https://coddy.dev/docs/getting-started/install), and configure a model using the [configuration guide](https://coddy.dev/docs/getting-started/configuration). From a source checkout, `make build TAGS=cli` builds a console-capable binary at `build/coddy` (Go 1.26 or newer).

Merge the `parallel-search` entry from [mcp.json](mcp.json) into the `mcpServers` object in your agent home's `mcp.json` (`~/.coddy/mcp.json` by default). Keep your other entries. For a fresh file, copy the whole example. The entry sends a project User-Agent and contains no authentication header or environment reference.

For a workspace-only setup, merge the entry into `<workspace>/.coddy/mcp.json` instead. Review and approve it before connecting:

```bash
coddy mcp list --cwd /path/to/workspace
coddy mcp trust parallel-search --cwd /path/to/workspace
```

Run these with `./build/coddy` instead of `coddy` when using a source build. Project-local entries still follow Coddy's [workspace trust policy](https://coddy.dev/docs/features/mcp#workspace-trust-for-project-local-servers).

## Use

Start Coddy in that workspace in `agent` or `plan` mode. Once the server connects, `/mcp` lists `parallel-search` and its `web_search` and `web_fetch` tools. An existing session receives a newly added global server on its next turn.

Ask, for example:

```text
Use the parallel-search MCP tools to find Go's official documentation about
modules. Fetch the relevant page if the search excerpts are insufficient,
and answer with source URLs.
```

The model decides which tools to call; MCP tool names are prefixed with the server name. This example adds tools alongside Coddy's built-in `websearch` and `webfetch`, and does not replace them or select a different model. A configured model is still needed to run an agent turn.

If the server is missing, check the JSON syntax, the file location, the server's enabled switch and any pending project trust decision. If a call hits a rate limit, follow the returned retry guidance rather than retrying immediately. To remove the example, delete only its `parallel-search` entry or disable that server through `/mcp`.
