# Агент ReAct - проектная спецификация

## Роль в Coddy

Coddy устроен как **харнес плюс исполняющий движок**. Этот документ описывает движок:

- **цикл ReAct** в `internal/agent`, который превращает промпты и инструменты в ходы с потоковой передачей;
- поведение **агента для программирования** по умолчанию - реестр инструментов, режимы `agent`, `plan` и `ask`, проверки разрешений.

Тот же харнес может работать с более узким набором инструментов или с другими клиентами (автоматизация, а не только IDE).

Описанные ниже поток ReAct и контракт сообщений остаются неизменными для любой сессии, работающей по ACP.

## Что такое ReAct?

ReAct (Reasoning + Acting) - агентная парадигма, в которой LLM чередует:
- **Мысль** - внутреннее рассуждение о том, что делать дальше;
- **Действие** - вызов инструмента или вывод результата;
- **Наблюдение** - получение результата действия.

Исходная статья - https://arxiv.org/abs/2210.03629

## Реализация цикла ReAct

### Структура системного промпта

Шаблоны лежат в **`internal/prompts/agent.md`**, **`plan.md`** и **`ask.md`** (по умолчанию встроены, переопределяются через **`prompts.dir`**). Они используют Go **`text/template`**.

После рендеринга порядок примерно повторяет markdown-файлы.

```
[Identity line — see "Agent identity" below; absent when the template opens with it]
[Intro + Mode + How to work / How to plan]
Working directory: {{.CWD}}

{{if .Tools}}
## Available tools
{{.Tools}}
{{end}}

{{if .Skills}} ... skill catalog + always-active/glob-matched bodies
          (invoked /name bodies are injected into the user message instead) ... {{end}}

{{if .Memory}}
## Session memory
{{.Memory}}
{{end}}

<environment_context>
<os>...</os>
<arch>...</arch>
<shell>...</shell>
</environment_context>
```

Блок окружения добавляется вне настраиваемого шаблона, поэтому собственный промпт не может случайно потерять сведения об ОС и оболочке.

### Системный промпт заморожен на весь ход

Системное сообщение рендерится **один раз за ход** функцией **`buildSystemPromptParts`**
(**`internal/agent/system_prompt.go`**), и каждый шаг цикла ReAct затем отправляет это же
сообщение байт в байт. Между шагами **`messages[0]`** никто не переписывает.

Причина в кэше промптов у провайдера. Провайдер кэширует запрос по **префиксу** (сначала определения
инструментов, затем сообщения с начала), и первый байт, отличающийся от предыдущего запроса,
выбрасывает всё закэшированное после него. Системное сообщение стоит перед всем диалогом, поэтому
часы с секундами или строка чек-листа, которую только что переписал инструмент, раньше заставляли
историю в пятьдесят тысяч токенов выглядеть новой при каждом запросе. Шлюз Alibaba кэширует блоками
по 1024 токена; отличие внутри первого блока оставляет **`cached_tokens`** нулевым, сколько бы
одинаковой истории ни шло дальше.

### Блок контекста хода

То, что всё-таки меняется во время хода, идёт **после** повторно отправляемой истории, одним
замыкающим сообщением `user`, которое собирает **`buildTurnContext`** (**`internal/agent/turn_context.go`**).

```
<turn_context>
Runtime state refreshed by Coddy for this step. It is not a message from the user; do not answer it, just take it into account.

## Current UTC time
2026-09-15T11:39:03Z

## Current todo checklist
- [ ] ...

## Long-term memory
Already on disk:
- ...
</turn_context>
```

- **текущее время**, всегда. Оно фиксируется один раз при рендеринге промпта хода и используется
  каждым шагом этого хода, потому что канал провайдера повторяет шаг, который ничего не выдал, и этот
  повтор должен быть тем же запросом, что и неудачный, байт в байт (*Empty-assistant re-issue* в разделе
  [Шаги цикла](#шаги-цикла));
- **чек-лист задач** (markdown из **`internal/tools/todo.FormatPlanMarkdown`** по
  **`session.Plan`**), если он есть у сессии, чтобы вызов **`coddy_todo_*`** в этом ходе
  отразился уже на следующем шаге;
- **отчёт субагента памяти** за этот ход, если включена долговременная память
  ([memory.md](../features/memory.md)). Найденное в памяти меняется от хода к ходу, поэтому, если
  рендерить его в системное сообщение, **`messages[0]`** становился бы новым на каждом ходе, и каждый
  раз пропадал бы закэшированный диалог. Отчёт попадает в блок на первом запросе, если запуск
  завершился в пределах **`memory.wait_seconds`**, иначе на одном из следующих шагов, и остаётся до
  конца хода; слот **`{{.Memory}}`** в шаблонах содержит только заметки сессии;
- **фоновые задачи, которые ещё выполняются** в этой сессии, по строке на задачу в формулировке
  **`background_list`** (**`backgroundTasksSection`**, [background-tasks.md](../features/background-tasks.md)),
  чтобы модель без лишнего вызова знала, что оставила работать. Завершённые и системные задачи в
  список не попадают, а при **`tools.background.enable`**, равном false, раздел не пишется.

Блок никогда не сохраняется. Он добавляется на границе отправки **`provider.Stream`**, рядом с
проекцией вытеснения результатов read/grep, и рабочий срез сообщений, к которому цикл продолжает
добавлять сообщения, его никогда не видит. Поэтому некэшированными остаются только последние
несколько сотен токенов запроса, а диалог перед ними попадает в кэш.

**Правила, которое активирует вызов инструмента**, в блоке нет. Это glob-правило, чей шаблон
совпадает с файлом, который вызов читает или пишет, или вложенные **`AGENTS.md`** и **`DESIGN.md`**
папок, в которые вызов заходит. Такое правило передаётся в **результате этого вызова** (**`toolCallRules`**, **`internal/agent/rules_activation.go`**)
и хранится в собственном поле **`Rules`** строки транскрипта, чтобы вывод, который показывает интерфейс
и который заменяет вытеснение результатов, оставался выводом самого инструмента; с содержимым оно
соединяется только на той же границе отправки (**`withToolRules`**). Записанное один раз вместе с
результатом, правило повторяется байт в байт каждым следующим запросом, тогда как блок отправлял бы
его заново на каждом шаге, а системный промпт следующего хода вобрал бы его в себя. Правило, которое
модель уже может прочитать с тем же текстом (в более раннем результате или сообщении, которое ещё
отправляется), повторно не прикрепляется; правило, которое убрало сжатие контекста или чей файл с тех
пор изменился, возвращается со следующим подходящим вызовом, а вызов над самим документом правила не
получает его обратно как правило. Блоки правил и инструкций самого системного промпта рендерятся один раз на поколение правил
сессии (**`standingParts`**, **`session.RulesPrompt`**), поэтому **`AGENTS.md`**, изменённый посреди
сессии, тоже не сдвигает **`messages[0]`**; сжатие контекста начинает следующее поколение.
См. [Правила и кэш промптов](../features/rules.md#правила-и-кэш-промпта).

**`UTCNow`** и **`TodoList`** остаются доступны шаблону из **`prompts.dir`**, и он по-прежнему может
их рендерить ценой этого кэша на каждом запросе. Такой шаблон не получает часов и чек-листа после
истории; его блок несёт два раздела, которые не печатает ни один шаблон, то есть отчёт памяти и
выполняющиеся фоновые задачи, и не добавляется, если нет ни того, ни другого.

### Вторая половина - вытеснение результатов read/grep

Заморозка системного сообщения даёт только половину стабильного префикса. Вытеснение результатов
read/grep (**`internal/agent/result_eviction.go`**) пишет заглушки **в середину** повторно
отправляемой истории, и это так же надёжно сбрасывает кэш начиная с этого места. Со скользящим
рабочим окном по умолчанию так раньше происходило почти на каждом шаге, и диалог обрабатывался
заново каждый раз, даже при замороженном промпте. **`compaction.result_eviction.start_percent`** (по умолчанию 50)
откладывает проекцию, пока оценка контекста не достигнет этой доли **`max_context_tokens`** модели;
ниже порога история уходит ровно в том виде, в каком она уже есть у провайдера. Решение принимается
по **необрезанным** сообщениям, поэтому обрезка не может вернуть оценку ниже отметки и заставить
проекцию метаться между двумя формами. См.
[compaction.md](../features/compaction.md).

### Как прочитать попадание в кэш

Провайдеры сообщают закэшированную долю запроса как
**`usage.prompt_tokens_details.cached_tokens`** (OpenAI-совместимые) или
**`cache_read_input_tokens`** (Anthropic). Coddy хранит её в
**`llm.Response.CachedInputTokens`**, части **`llm.Response.InputTokens`**, которое считает
весь промпт так же, как **`prompt_tokens`** у OpenAI, поэтому у Anthropic **`input_tokens`**,
**`cache_creation_input_tokens`** и **`cache_read_input_tokens`** складываются. Оба значения Coddy
пишет в лог для каждого вызова.

```
export CODDY_LOG_LEVEL=debug   # or logger.levels: {agent: debug}
# msg="llm call usage" input_tokens=6044 cached_input_tokens=5120 output_tokens=12
```

Если в длинной сессии на втором шаге **`cached_input_tokens`** близко к нулю, значит, что-то
переписывает префикс. Большинство OpenAI-совместимых серверов вообще не отдают это поле, и тогда
оно равно нулю, хотя промаха нет.

### Представление агента

**Каждый системный промпт, который отправляет Coddy, начинается с названия продукта.** Это предложение лежит в **`internal/prompts/identity.go`** как **`prompts.Identity`** (**`"You are Coddy, an AI coding agent."`**), а ставит его туда **`prompts.WithIdentity`**.

Зачем это нужно. LLM-шлюз не может отличить одного OpenAI-совместимого клиента от другого по протоколу, поэтому шлюзы определяют источник трафика, сравнивая **начало системного промпта** с таблицей известных продуктов (**`"You are Claude Code…"`**, **`"You are Cline…"`**). Раньше Coddy начинал промпт с общей фразы и поэтому был не виден в такой аналитике. Считайте **`prompts.Identity`** опубликованным контрактом. Шлюзы ищут подстроку **`you are coddy`**, и если её переформулировать, Coddy молча выпадет из их отчётов, пока они не подстроятся.

Где она применяется:

- **`buildSystemPrompt`** (`internal/agent/system_prompt.go`), последний шаг перед разбивкой контекста; покрывает режимы "Агент", "План" и "Чат", собственный шаблон пользователя из **`prompts.dir`** и запасной вариант рендеринга;
- **`buildCompactionRequest`** (`internal/agent/compact.go`), потому что суммаризатор - отдельный запрос со своим системным промптом;
- вспомогательные HTTP-промпты, то есть генерация названия чата (`external/httpserver/coddy_coddy.go`) и улучшение промпта (`external/httpserver/enhance_prompt.go`); шаблон субагента памяти (`external/memory/prompts/memory_agent.md`) рендерит `buildSystemPromptParts` через `PromptTemplate` его дочерней сессии, поэтому он сам по себе начинается со строки представления.

Тесты закрепляют два свойства (`internal/prompts/identity_test.go`, `internal/agent/identity_prompt_test.go`):

1. маркер попадает в **первые 220 символов**, потому что именно этот префикс проверяет шлюз, и длинный **`{{.CWD}}`** не должен его вытеснить;
2. строка встречается **ровно один раз**. Встроенные шаблоны уже начинаются с **`You are Coddy, …`**, поэтому **`WithIdentity`** находит маркер и возвращает их нетронутыми, не добавляя сверху вторую строку представления.

Собственному шаблону из **`prompts.dir`** **не** нужно называть Coddy, строка добавляется в его начало автоматически.

### Вызов инструментов через Function Calling API

Современные LLM поддерживают нативный вызов функций и инструментов. Агент использует его вместо
текстового ReAct-промптинга:

1. Инструменты описываются объектами JSON Schema и передаются в API LLM;
2. LLM возвращает структурированные запросы на вызов инструментов (а не сырой текст);
3. Агент выполняет запрошенные инструменты;
4. Результаты добавляются в диалог как сообщения с ролью `tool`;
5. LLM продолжает рассуждать с результатами инструментов в контексте.

Такой подход надёжнее разбора текста, и его поддерживают все крупные провайдеры
(OpenAI, Anthropic, Ollama с совместимыми моделями).

### Структура сообщений диалога

```
messages: [
  { role: "system",    content: <system_prompt> },
  { role: "user",      content: <user_prompt> },
  { role: "assistant", content: "", tool_calls: [{ id: "call_1", name: "read_file", args: {...} }] },
  { role: "tool",      tool_call_id: "call_1", content: <file_contents> },
  { role: "assistant", content: "", tool_calls: [{ id: "call_2", name: "write_file", args: {...} }] },
  { role: "tool",      tool_call_id: "call_2", content: "OK" },
  { role: "assistant", content: <final_answer> }
]
```

### Шаги цикла

```
1. BUILD_MESSAGES
   - Load applicable skills and project rules for current context (separate prompt sections)
   - Build system prompt (template + TemplateData incl. TodoList snapshot)
   - Skill bodies are already in the history: on Run entry, before the user turn is persisted, Run appends the body of each skill the typed text invokes as `/name` to that message as a `<coddy_attachment kind="skill">` element (`invokedSkillBlocks`), so later requests replay the same bytes and the prefix cache holds. Surfaces leave the element out of the transcript (`mention.ForDisplay`, `stripCoddyAttachments.ts`); queued follow-ups get their skill bodies the same way.
   - Prepend system to session history (user turn already persisted on Run entry)

2. LLM_CALL
   - Send messages + tool definitions to LLM provider
   - Receive response: may contain text + tool_calls

3. STREAM_RESPONSE
   - For each text chunk: send session/update(agent_message_chunk)
   - For each tool_call: send session/update(tool_call, status=pending)

4. EXECUTE_TOOLS (if any tool calls)
   - For each tool_call sequentially inside one assistant message:
     a. Send session/update(tool_call_update, status=in_progress)
     b. If requires permission: session/request_permission -> wait for response
     c. Execute tool (built-in or MCP)
     d. Send session/update(tool_call_update, status=completed|failed, content=result)
     e. Append tool result to conversation history

5. REFRESH_SYSTEM
   - Next loop iteration repeats from step 2 after rewriting messages[0] with a fresh **`Render`** (same session state, potentially new Plan rows)

6. CHECK_COMPLETION
   - If no tool calls in last response -> DONE (stopReason: end_turn)
   - If turn_count >= max_turns -> DONE (stopReason: max_turns;
     the turn's stop notice names the key that set the limit). max_turns defaults
     to 165; an explicit 0 disables the step limit
   - Otherwise -> back to step 2

   Loop guard (**`agent.loop_guard`**, default on) can end the turn earlier:
   - A streamed response repeating the same passage **`loop_stream_repeat_cycles`** times
     in a row (answer text or reasoning) has its stream cancelled. The repeated run is
     stripped from the stored message, so it is never replayed to the model.
   - A tool call repeated **`loop_tool_repeat_limit`** times with identical canonical
     arguments is not executed; the model gets a result explaining why.
   - Either case first nudges the model to change course, up to **`loop_nudge_max`**
     times, then -> DONE (stopReason: agent_refused) with a notice.

   Recovery attempts and transport retries draw from the **`llm_retry_max`**
   shared budget (default 3). The initial model request for a step is not
   charged; each extra attempt or recovery consumes one slot; the budget resets
   when the model makes progress (a tool call or an answered followup) — it is
   not a lifetime cap on all LLM calls in a turn. Per-strategy limits apply
   independently of the budget:

   - **Transport retries.** The resilient wrapper repeats on HTTP 429, 408,
     5xx, and transport failures that left no output with the caller; each
     retry consumes one slot. A cancellation, an unknown host, and any failure
     after output was emitted stay final.
   - **First-token re-issue.** A streamed call the first-token guard
     (**`llm_first_token_timeout_ms`**) cuts with **nothing produced** is
     re-issued at most once, using a normal **`max_turns`** iteration. The
     re-issue is skipped after output was emitted or the caller cancelled.
     Consumes one slot.
   - **Empty-assistant re-issue.** A step that returns neither answer text nor
     a tool call is replayed once as the identical request; the empty assistant
     turn is removed from the LLM-facing message slice (the transcript keeps
     it, with signed thinking preserved). A reply stopped at `max_tokens` is
     not replayed: the same request would hit the same cap, and the
     output-limit nudge below handles it. Consumes one slot.
   - **Wording nudges.** If the re-issue also comes back empty, up to
     two nudges (**`maxEmptyAssistantContinuations`**) are sent, each consuming
     one slot and a normal **`max_turns`** iteration. The plain replay uses
     a normal iteration too; transport retries stay inside that iteration.
     The nudge projection also removes the empty assistant message from the
     LLM-facing history. If auto-compaction rebuilds that history between
     attempts, the pending recovery projection and its nudges are restored;
     signed reasoning stays in the transcript. After the budget or nudge limit is exhausted the turn
     ends with **`StopReasonRefused`**.
   - **Output-limit nudge.** A step that stops at `max_tokens`
     (`finish_reason: "length"`) with no tool call and no visible text - only
     thinking, or nothing - is answered with **`outputLimitNudge`** instead of
     ending the turn: the previous step hit the output limit before it
     produced an answer or a tool call, keep the reasoning short, split a
     large file write or edit into several smaller tool calls. The empty step
     stays in the transcript and leaves the LLM-facing history, and the nudge
     is appended at its end, so the cached prefix is untouched; an
     auto-compaction between the attempts restores the pending nudges with the
     other recovery projection. At most **`maxOutputLimitRecoveries`** (2) in
     a row. Each consumes one slot and a normal **`max_turns`** iteration, so
     `llm_retry_max: 0` turns it off, and tool progress or a follow-up resets
     the count. A step that already wrote visible text ends the turn as before.
     Subagents and scheduled runs take the same path. When the nudges are
     spent the turn ends with **`StopReasonMaxTokens`** and a notice
     (**`outputLimitNotice`**) that says the model was already asked.
   - **Provider recovery.** A call that failed because of the provider's lane
     (**`llm.IsTransientProviderError`**: 5xx, a cut or silent stream - an event
     cut inside its JSON included, a stream frame that is not JSON at all not -
     output already emitted or not; a 429 is left to the wrapper and the limit wait)
     does not end the turn. The answer text and
     reasoning already streamed are kept as an assistant message without tool
     calls, a **`notice`** row goes to the UI log, and after a pause
     (**`providerRecoveryDelay`**: 5x then 20x **`llm_retry_base_ms`**, or a
     longer **`Retry-After`**, capped at 2 minutes) the step runs again on a
     fresh allowance, with an LLM-facing nudge to continue when text was kept.
     At most **`maxProviderRecoveries`** (2) in a row; a successful call resets
     the count. Uses a normal **`max_turns`** iteration.

   An explicit **`llm_retry_max: 0`** disables all of the above. The
   `loop_guard`, Stop hooks, fallback models, and `wait_for_limit_reset` are
   independent policies, each governed by their own settings.

   At `logger.level: debug`, each LLM call completion logs
   `msg="llm call finished"` with `provider_attempts` (total inner adapter
   calls including transport-layer retries), `transport_retries`, `call_reason`
   (one of `step`, `empty_reissue`, `empty_nudge`, `output_limit_nudge`,
   `first_token_retry`, `provider_recovery`, `loop_guard`, `quota_reset_wait`,
   `stop_hook`, `queued_followup`), and
   `retries_remaining` (budget slots left after this call).

   Two guards bound a streamed call that stops answering. The first-token guard
   (**`llm_first_token_timeout_ms`**, 90 s) cuts a call that produced nothing;
   the stream idle guard (**`llm_stream_idle_timeout_ms`**, five minutes, in
   **`internal/llm/transport.go`**) cuts a response whose server sent nothing
   for that long after its first bytes. The text the user already watched
   stream in is persisted like a truncation, and the turn ends with the stall
   named. Neither guard applies to a **`stream: false`** model, whose answer
   arrives in one piece: **`providers[].timeout_ms`** is the only bound on
   such a call, next to the HTTP/2 liveness pings that close a connection
   whose far side stopped answering.

7. FINAL_RESPONSE
   - Send session/prompt response with stopReason
```

## Поведение в разных режимах

### Режим "Агент"

Встроенный **`agent.md`** описывает поведение агента (качество, оболочки, задачи, git worktree). Инструкции о задачах ссылаются на инструменты **`coddy_todo_plan_*`** и **`coddy_todo_item_*`**, которые попадают в **`Tools`**. Блок **Git worktrees** называет **`.coddy/worktrees/<branch>`** местом для worktree, тем же каталогом, который использует переключение рабочей папки в HTTP-интерфейсе ([Сессии](../features/sessions.md#git-worktree)); без этого блока модель выбирает место сама и оставляет неотслеживаемую папку в корне репозитория.

Типичные встроенные инструменты (без инструментов из пространств имён MCP):

- `read_file`, `write_file`, `write_text_file`, `list_dir`, `search_files`;
- `websearch`, `webfetch`, `http_request`;
- `run_command`, `apply_diff`;
- изменения файловой системы - **`mkdir`**, **`rm`**, **`rmdir`**, **`touch`**, **`mv`** (часть из них может требовать разрешённых путей);
- чек-лист сессии - **`coddy_todo_plan_read`**, **`coddy_todo_plan_replace`**, **`coddy_todo_plan_archive`**, **`coddy_todo_item_add`**, **`coddy_todo_item_remove`**, **`coddy_todo_item_update`**, **`coddy_todo_item_move`**;
- все инструменты MCP-серверов (имена вида **`serverName__toolName`**).

### Режим "План"

Встроенный **`plan.md`** оставляет набор инструментов **реестра** по умолчанию ориентированным на чтение (в объявленном наборе нет встроенных инструментов записи и инструментов задач **coddy**). **`run_command`** и все инструменты **MCP** из настроенных серверов по-прежнему доступны для исследования.

Типичные встроенные инструменты, доступные LLM (разрешённый список реестра):

- `read_file`, `list_dir`, `search_files`;
- `websearch`, `webfetch`;
- `run_command`.

Плюс инструменты MCP (**`serverName__toolName`**). Когда пора переходить к реализации, промпты велят переключить клиент в режим **`agent`**.

### Режим "Чат"

Встроенный **`ask.md`** описывает ассистента только для чтения, который отвечает по репозиторию и вебу и ничего не меняет. Разрешённый список реестра (**`internal/agent.ToolSetForMode("ask")`**) - это **`read`**, **`keep_result`**, **`glob`**, **`grep`**, **`print_tree`**, **`websearch`**, **`webfetch`**, **`question`**, **`load_skill`**, **`coddy_docs_search`** и **`coddy_docs_read`**; в нём нет ни оболочки, ни инструментов плана, задач или конфигурации, ни **`spawn_agent`**, а инструменты **MCP** никогда не добавляются. В отличие от режима "План", разрешённый список проверяется и при выполнении, поэтому вызов, повторённый из истории, отклоняется с уведомлением о режиме только для чтения. Упоминание плана или метаданные **`runPlanSlug`** в режиме "Чат" никогда не запускают выполнение плана, а субагент памяти только читает из памяти. Дочерний субагент никогда не работает в режиме "Чат", если только ход его родителя уже не шёл в этом режиме, а из него порождать субагентов нельзя.

## Спецификация встроенных инструментов

### `read_file`
```json
{
  "name": "read_file",
  "description": "Read the contents of a file",
  "parameters": {
    "path": { "type": "string", "description": "Absolute or relative (to cwd) path" },
    "start_line": { "type": "integer", "description": "First line to read (1-based, optional)" },
    "end_line": { "type": "integer", "description": "Last line to read (1-based, optional)" }
  },
  "required": ["path"]
}
```

### `write_file`
```json
{
  "name": "write_file",
  "description": "Write or create a file with the given content",
  "parameters": {
    "path": { "type": "string", "description": "Absolute or relative (to cwd) path" },
    "content": { "type": "string", "description": "Full file content to write" }
  },
  "required": ["path", "content"]
}
```

### `list_dir`
```json
{
  "name": "list_dir",
  "description": "List files and directories at the given path",
  "parameters": {
    "path": { "type": "string", "description": "Directory path (default: cwd)" },
    "recursive": { "type": "boolean", "description": "Include subdirectories" }
  }
}
```

### `grep`
```json
{
  "name": "grep",
  "description": "Search file contents recursively (system ripgrep with a built-in fallback)",
  "parameters": {
    "pattern": { "type": "string", "description": "Regex or literal search pattern" },
    "path": { "type": "string", "description": "Directory to search in (default: cwd)" },
    "glob": { "type": "string", "description": "File glob filter (e.g. '**/*.go')" },
    "case_sensitive": { "type": "boolean", "default": false },
    "max_results": { "type": "integer", "default": 100 }
  },
  "required": ["pattern"]
}
```

### `run_command`
```json
{
  "name": "run_command",
  "description": "Execute a shell command in the working directory",
  "parameters": {
    "command": { "type": "string", "description": "Shell command to execute" },
    "timeout_seconds": { "type": "integer", "default": 30 }
  },
  "required": ["command"]
}
```

### `apply_diff`
```json
{
  "name": "apply_diff",
  "description": "Apply a unified diff to a file",
  "parameters": {
    "path": { "type": "string", "description": "File to patch" },
    "diff": { "type": "string", "description": "Unified diff content" }
  },
  "required": ["path", "diff"]
}
```

## Формат обновления плана

Клиенты получают уведомления `session/update`, у которых поле **`sessionUpdate`** равно **`plan`**, со структурированными **`entries`**. Когда сохранение сессий включено, инструменты списка задач также сохраняют активный чек-лист в бандле в **`todos/active.md`** (зеркально **`FormatPlanMarkdown`** / **`ParsePlanMarkdown`**).

Пример содержимого уведомления.

```json
{
  "sessionUpdate": "plan",
  "entries": [
    { "content": "Read current auth module", "priority": "high", "status": "pending" },
    { "content": "Analyze JWT requirements", "priority": "high", "status": "pending" },
    { "content": "Write new auth implementation", "priority": "medium", "status": "pending" },
    { "content": "Update tests", "priority": "low", "status": "pending" }
  ]
}
```

По мере работы агента записи плана обновляются.
```json
{ "content": "Read current auth module", "priority": "high", "status": "completed" }
```

## Обработка ошибок в цикле ReAct

- Ошибка API LLM - попытки берутся из общего бюджета шага `agent.llm_retry_max` (по умолчанию 3, явный 0 отключает); повторы транспорта, повторная отправка после тайм-аута первого токена и восстановление после пустого ответа делят один пул; бюджет сбрасывается, когда инструменты продвигают работу, и не ограничивает все вызовы LLM за весь ход;
- Поток LLM завис (после первых байтов ничего не приходит в течение `llm_stream_idle_timeout_ms`) - частичный ответ сохраняется, ход завершается ошибкой с указанием зависания; повтор в пределах оставшегося общего бюджета выполняется, только если ничего не было доставлено;
- Ошибка выполнения инструмента - ошибка возвращается как наблюдение, следующий шаг решает LLM;
- Отказ в разрешении - возвращается наблюдение "permission denied";
- Тайм-аут инструмента - после настроенного тайм-аута возвращается наблюдение "timeout";
- Слишком длинный контекст - старые сообщения сжимаются в сводку, работа продолжается со сводкой;
- Отмена - все операции прерываются, возвращается причина остановки `cancelled`.

<!-- docsgen:source sha256=46b89c7c22a40efc -->
