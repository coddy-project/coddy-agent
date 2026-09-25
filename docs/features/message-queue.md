# Message queue

You can send another message while Coddy is working. Each waiting message has a mode:

- **Steer** enters the current turn at its next ReAct step, after the tool results already in flight.
- **After turn** starts a separate prompt after the current answer. Several deferred messages run one at a time, in the order they were queued.

The first time you send during a turn, the browser or console asks which mode Enter should use. It saves the answer as `agent.queue_mode` in `config.yaml`; you can change it in **Settings → Agent**. While a turn runs, **Enter** uses that default and **Tab** uses the other mode. The browser's send button uses the default. The queue above the composer shows each message's mode and attachment count; click its mode to switch it before delivery, or remove it to return the text and attachments to the draft. The console shows the same queue above its editor; use `/queue mode <n> steer|after_turn`, `/queue drop <n>`, or `/queue clear` to manage it.

![First-use queue mode chooser in the browser](../assets/message-queue/message-queue-choice-dark-1280.png)

*The first queued message asks which mode Enter should use.*

![Queue mode chooser on a narrow screen](../assets/message-queue/message-queue-choice-dark-390.png)

*The choice wraps within a narrow composer.*

![Steer and after-turn messages above the browser composer](../assets/message-queue/message-queue-modes-dark-1280.png)

*Each waiting message shows its mode; the second also carries an attachment.*

![Waiting messages on a narrow screen](../assets/message-queue/message-queue-modes-dark-390.png)

*Modes and attachment counts remain visible at 390 px.*

## In the console

![The console queue with steer and after-turn messages](../assets/message-queue/message-queue-console-modes-dark.png)

*The console numbers waiting messages and shows each mode above the editor.*

An attached image follows its text into the queue. Coddy saves it with the session's assets when the agent reads the message, sends it to a multimodal model, and shows it on the user message in the transcript. A message containing only an attachment is valid. `@` mentions in the text resolve when the message is read. Settings commands at the start of a queued message apply immediately; only the remaining prompt waits.

## Turn boundaries and Stop

Steer messages waiting at the end of an ordinary answer are read before the turn releases. After-turn messages each start a fresh agent run after that answer. A failed or cancelled turn drops unread steer messages. **Stop leaves after-turn messages in the queue without starting them**; you can remove or change them, and they resume after the next ordinary turn. The queue is in process memory and does not survive a service restart. It holds at most 20 messages per session. Child sessions do not accept operator follow-ups.

A queue change is published on the turn stream and `GET /coddy/events` as `message_queue`, with the full list and a monotonic version. This keeps browsers and remote consoles viewing the same session in sync. A client joining later reads `GET /coddy/sessions/{id}/queue`; a retained after-turn message remains visible even while the session is idle.

## HTTP API

| Route | Action |
|---|---|
| `GET /coddy/sessions/{id}/queue` | List waiting messages with `id`, `text`, `mode`, `imageParts`, and `createdAt`. |
| `POST /coddy/sessions/{id}/queue` | Queue `{"text":"...","mode":"steer|after_turn","inline_files":[{"name":"image.png","data_url":"data:image/png;base64,..."}]}`. Mode defaults to `steer`. |
| `PATCH /coddy/sessions/{id}/queue/{message_id}` | Change mode with `{"mode":"steer|after_turn"}`. |
| `DELETE /coddy/sessions/{id}/queue/{message_id}` | Remove one waiting message. |
| `DELETE /coddy/sessions/{id}/queue` | Clear all waiting messages. |

A message accepted after the active turn closes receives `409 no_active_turn`; send it as an ordinary prompt once the session is idle. A full queue receives `409 queue_full`, and a child session receives `409 subagent_read_only`. See the [HTTP API reference](../reference/http-api.md) for response shapes.
