# Coddy as a model in VS Code Copilot

VS Code Copilot takes any OpenAI-compatible endpoint as a chat model through `chatLanguageModels.json` (Manage Models, then a custom endpoint). Pointing it at a running `coddy serve` puts coddy's models, and coddy's agent, in the model picker of Copilot Chat. The stream Copilot reads is the strict one described in [Two stream dialects](../reference/http-api.md#two-stream-dialects); the model ids are the ones `GET /v1/models` lists ([HTTP API](../reference/http-api.md)).

1. **Serve on an address VS Code can reach.** The default bind is loopback on port 12345, enough for a VS Code on the same machine. For a VS Code elsewhere, bind and protect the server the way [A server driven from a laptop](server-driven-from-a-laptop.md) does, and hand the token to VS Code as `apiKey`.

   ```bash
   coddy serve
   curl -s http://127.0.0.1:12345/v1/models | jq -r '.data[].id'   # the ids step 2 takes
   ```

2. **Register the endpoint.** One entry per model id. A `models[].model` id is coddy as a plain model proxy: the provider's answer streamed back, reasoning included, no tools. The `agent` id is coddy's ReAct agent as a model: it reads and edits the workspace `coddy serve` was started in with its own tools, and Copilot only sees the answer. Copilot's `toolCalling` is for tools the client runs itself, which coddy never returns, so leave it off for both.

   ```json
   {
     "name": "Coddy",
     "vendor": "customendpoint",
     "apiKey": "coddy",
     "apiType": "chat-completions",
     "models": [
       {
         "id": "neuraldeep/qwen3.8-27b",
         "name": "Coddy · Qwen 3.8 27B",
         "url": "http://127.0.0.1:12345/v1/chat/completions",
         "toolCalling": false,
         "maxInputTokens": 128000,
         "maxOutputTokens": 8192
       },
       {
         "id": "agent",
         "name": "Coddy · agent",
         "url": "http://127.0.0.1:12345/v1/chat/completions",
         "toolCalling": false,
         "maxInputTokens": 128000,
         "maxOutputTokens": 8192
       }
     ]
   }
   ```

   Bearer auth is off by default, and any `apiKey` string satisfies VS Code's form; with `httpserver.auth_token` set, the `apiKey` is that token. Copilot sends the whole conversation with every request, so each request is a session of its own on the server unless a `requestHeaders` entry on the model sets `X-Coddy-Session-ID` to a fixed `sess_<hex>` id, which keeps the turns in one transcript.

3. **Mind the agent's permission gate.** The `agent` model runs tools under `tools.permission_mode`, and a permission prompt cannot be answered from Copilot: the turn waits for an answer that never comes. Give the serving config `bypass` when the workspace is one you trust the agent with, or keep `ask` and answer the prompt in the web UI, where the turn is live under its session.

   ```yaml
   tools:
     permission_mode: bypass
   ```

4. **Check that it worked.** Pick "Coddy · Qwen 3.8 27B" in Copilot Chat and ask anything: the answer streams in, and with a reasoning model the thinking shows in Copilot's own foldout, because `reasoning_content` is a field it reads. The same request from a terminal shows the stream Copilot consumed, one choice finished with `stop` before `[DONE]`:

   ```bash
   curl -sN http://127.0.0.1:12345/v1/chat/completions -H 'Content-Type: application/json' \
     -d '{"model":"neuraldeep/qwen3.8-27b","messages":[{"role":"user","content":"hello"}],"stream":true}' | tail -4
   ```

   "Response contained no choices." in Copilot means the server did not finish a choice: check that `coddy serve` is the version that streams `finish_reason` (`coddy -v`, 1.1.5 or later), and that nothing between them rewrites the stream.
