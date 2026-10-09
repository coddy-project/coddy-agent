# Справочник по протоколу ACP

## Обзор

Coddy реализует ACP как **контракт обмена для харнеса**. ACP стандартизирует то, как клиенты (например, редакторы, скрипты или оркестраторы) общаются с процессом агента. Стандартная конфигурация представляет **агента для программирования**, но транспорты и RPC-методы относятся к общему интерфейсу харнеса - initialize, жизненный цикл сессии, `session/prompt`, потоки разрешений и параметры, связанные с MCP.

Спецификация протокола - https://agentclientprotocol.com/protocol/overview

## Транспорт

Все сообщения - это JSON-объекты, разделённые переводом строки и передаваемые через **stdin/stdout**.

```
stdin  -> messages from Client to Agent
stdout -> messages from Agent to Client (responses + notifications)
stderr -> agent logs (not protocol messages)
```

## Типы сообщений

### Запрос (от клиента к агенту)

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "session/prompt",
  "params": { ... }
}
```

### Ответ (от агента к клиенту)

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": { ... }
}
```

### Ответ с ошибкой

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "error": {
    "code": -32600,
    "message": "Invalid request"
  }
}
```

### Уведомление (от агента к клиенту, ответ не ожидается)

```json
{
  "jsonrpc": "2.0",
  "method": "session/update",
  "params": { ... }
}
```

## Клиенты через stdio (особенность Coddy)

Самописные скрипты, которые управляют **`coddy acp`** через канал, должны реализовать описанное ниже поведение. Эталонный харнес - **`examples/acp/acp_e2e_todo.py`**.

1. **Nil `result` и `omitempty`** - успешные ответы JSON-RPC формируются без `result`, когда обработчик на Go возвращает указатель **`nil`** (например, **`session/set_mode`**). Строка ответа может содержать только **`jsonrpc`** и **`id`**, без **`result`** и без **`error`**. Считайте любой объект с совпадающим **`id`** и без **`method`** завершением вашего ожидающего запроса;
2. **Перемежающиеся `session/update`** - после **`session/prompt`** агент отдаёт потоком много уведомлений до финального ответа. Читайте stdout построчно, пока не придёт строка с **`id`** вашего запроса, а между делом обрабатывайте **`session/request_permission`** или **`session/request_question`**, записывая ответ клиента с тем же **`id`**;
3. **Буферизация stdout** - когда stdout не TTY, вывод может буферизоваться блоками. Оберните бинарник в **`stdbuf -oL -eL`** (или аналог), чтобы строки появлялись по мере записи;
4. **Параллельные обработчики запросов** - ожидающие запросы обрабатываются асинхронно. Если ваш клиент рассчитывает на строгий порядок, не отправляйте второй RPC, пока не прочитали ответ на предыдущий.

## Порядок обмена

```
Client                          Agent
  |                               |
  |-------- initialize ---------->|
  |<------- initialize resp ------|
  |                               |
  |-------- session/new --------->|
  |<------- session/new resp -----|
  |                               |
  |-------- session/prompt ------>|
  |<------- session/update -------|  (notifications: plan, available_commands_update, chunks, tool_calls)
  |<------- session/update -------|
  |<------- session/update -------|
  |<------- session/prompt resp --|  (stopReason: end_turn)
  |                               |
```

## Методы

### `initialize`

Согласует версию протокола, и стороны обмениваются возможностями.

**Параметры запроса.**
```json
{
  "protocolVersion": 1,
  "clientCapabilities": {
    "fs": {
      "readTextFile": true,
      "writeTextFile": true
    },
    "terminal": true
  },
  "clientInfo": {
    "name": "example-acp-client",
    "title": "Example ACP Client",
    "version": "1.0.0"
  }
}
```

**Результат ответа.**
```json
{
  "protocolVersion": 1,
  "agentCapabilities": {
    "loadSession": true,
    "sessionCapabilities": {
      "list": {}
    },
    "promptCapabilities": {
      "image": false,
      "audio": false,
      "embeddedContext": true
    },
    "mcpCapabilities": {
      "http": true,
      "sse": false
    }
  },
  "agentInfo": {
    "name": "coddy-agent",
    "title": "Coddy Agent",
    "version": "0.1.0"
  },
  "authMethods": []
}
```

### `session/new`

Создаёт новую сессию диалога.

**Параметры запроса.**
```json
{
  "cwd": "/home/user/project",
  "mcpServers": [
    {
      "name": "my-mcp",
      "command": "/path/to/mcp-server",
      "args": ["--stdio"],
      "env": []
    }
  ]
}
```

**Результат ответа.**

Coddy возвращает и **Session Config Options** (их предпочитают современные клиенты ACP), и устаревшее поле **`modes`** для совместимости. Клиентам, которые поддерживают `configOptions`, следует выбирать через них режим и модель.

```json
{
  "sessionId": "sess_abc123def456",
  "configOptions": [
    {
      "id": "mode",
      "name": "Session mode",
      "description": "Agent runs tools; Plan focuses on design without execution.",
      "category": "mode",
      "type": "select",
      "currentValue": "agent",
      "options": [
        {
          "value": "agent",
          "name": "Agent",
          "description": "Execute tasks with full tool access"
        },
        {
          "value": "plan",
          "name": "Plan",
          "description": "Plan and design without code execution"
        }
      ]
    },
    {
      "id": "model",
      "name": "Model",
      "description": "LLM used for this session.",
      "category": "model",
      "type": "select",
      "currentValue": "openai/gpt-5.6-terra",
      "options": [
        {
          "value": "openai/gpt-5.6-terra",
          "name": "gpt-5.6-terra",
          "description": "openai"
        }
      ]
    },
    {
      "id": "reasoning",
      "name": "Reasoning",
      "description": "Controls the reasoning effort used for this session.",
      "category": "thought_level",
      "type": "select",
      "currentValue": "medium",
      "options": [
        { "value": "low", "name": "low" },
        { "value": "medium", "name": "medium" },
        { "value": "high", "name": "high" }
      ]
    },
    {
      "id": "permission_mode",
      "name": "Permission mode",
      "description": "Controls when the agent asks for user approval before running tools.",
      "category": "_permission_mode",
      "type": "select",
      "currentValue": "ask",
      "options": [
        {
          "value": "ask",
          "name": "Ask",
          "description": "Always ask before running commands or writing files"
        },
        {
          "value": "accept_edits",
          "name": "Accept edits",
          "description": "Auto-approve file writes; ask before running commands"
        },
        {
          "value": "bypass",
          "name": "Bypass",
          "description": "Never ask for permission"
        }
      ]
    }
  ],
  "modes": {
    "currentModeId": "agent",
    "availableModes": [
      {
        "id": "agent",
        "name": "Agent",
        "description": "Execute tasks with full tool access"
      },
      {
        "id": "plan",
        "name": "Plan",
        "description": "Plan and design without code execution"
      },
      {
        "id": "ask",
        "name": "Ask",
        "description": "Answer questions from the repository without changing anything"
      }
    ]
  }
}
```

Параметр `model` есть, только если список `models` в конфигурации агента не пуст. Действующая модель по умолчанию - `agent.model`, пока выбор через этот параметр её не изменит. Это может быть собственный выбор пользователя или запомненный выбор интерфейса, который интерактивный клиент ставит на только что созданную сессию (у консоли и у веб-интерфейса он свой; см. [Конфигурация](../getting-started/configuration.md)). Каждое `value` в списке совпадает со строкой YAML `models[].model` (`provider_name/api_model_id`).

### `session/load`

Перезагружает сохранённую сессию по `sessionId`. Агент восстанавливает `session.json` и `messages.json`, заново собирает скилы и подключения MCP по данным запроса, воспроизводит прежние ходы пользователя и ассистента (и сводки вызовов инструментов) через `session/update`, отправляет обновление `plan`, если существует `todos/active.md`, и отправляет `available_commands_update`, как только ответ ушёл в канал. Воспроизведённое сообщение пользователя выглядит так, как его набрали. Вложения, которые принесли его упоминания, сворачиваются обратно в эти упоминания (`@src/app.go:3-5`), а их содержимое никогда не показывается.

Здесь воспроизведение идёт до ответа, как требует ACP, и это безопасно, потому что клиент сам назвал сессию. При повторном открытии бандла через **`session/new`** (`coddy acp --session-id <id>`) всё наоборот. Клиент узнаёт id только из ответа, поэтому воспроизведение его дожидается, а всё, что было бы записано раньше, пришло бы для сессии, которую клиент ещё не зарегистрировал.

**Параметры запроса** (по ACP обязательны `cwd`, `sessionId` и `mcpServers`).

```json
{
  "sessionId": "sess_abc123def456",
  "cwd": "/home/user/project",
  "mcpServers": []
}
```

**Результат ответа** - `modes` и `configOptions`, как у `session/new`.

### `session/list`

Перечисляет сохранённые сессии, найденные в настроенном корне сессий (см. README), новые первыми. В ответе для каждой записи есть `sessionId`, `cwd`, `title` и `updatedAt`; дочерние сессии запусков субагентов и запусков планировщика опускаются.

Необязательный `cwd` сужает список до одной рабочей папки. Он называет папку, а не строку. Путь нормализуется, его симлинки разрешаются, если папка существует, а на Windows и macOS сравнение идёт без учёта регистра. Сессия хранит cwd, который ей дал её клиент, а два клиента часто записывают одну и ту же папку по-разному (консоль хранит логический `$PWD` чекаута через симлинк, редактор отправляет физический путь, который он разрешил, а клиент на Windows может записать букву диска строчной), поэтому сессия, созданная одним клиентом, видна в списке и для другого. Сессии родительской или дочерней папки в список не входят, поэтому консоль, запущенная в подпапке рабочей папки, не числится среди сессий этой рабочей папки. `coddy sessions list --cwd` и `GET /coddy/sessions?cwd=` применяют то же правило.

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "session/list",
  "params": { "cwd": "/home/user/project" }
}
```

### Раскладка на диске (Coddy)

Когда процесс запущен с корнем сессий, доступным для записи (по умолчанию **`$CODDY_HOME/sessions`**), каждый бандл лежит в `<root>/<sessionId>/` и содержит:

- `session.json` - id, cwd, режим, переопределение модели, уровень рассуждения, режим разрешений, под которым работала дочерняя сессия субагента (`permissionMode`; переопределение обычной сессии никогда не записывается), память агента, производное или закреплённое название (`titlePinned`), отметки времени, необязательные **`activitySeq`** / **`readActivitySeq`** / **`lastErrorSeq`** для синхронизации активности поля ввода и состояния ошибки между HTTP-интерфейсами (последнее - поколение активности последнего настоящего сбоя хода или ноль, если ошибки нет);
- `messages.json` - история сообщений LLM (роли user, assistant, tool);
- `assets/` - зарезервировано для будущих файлов сессии;
- `todos/active.md` - текущий чек-лист задач, синхронизированный из инструментов плана;
- `todos/archive/todo-<nanos>.md` - архивный список, когда завершённый список заменяется новым;
- `plans/<slug>.plan.md` - файлы планов проектирования (YAML frontmatter + тело в markdown), которые записываются в режиме "План" через **`plan_write`**.

Сервер всегда объявляет **`loadSession`**, когда настроено хранилище (`coddy acp` и **`coddy serve`** открывают **`FileStore`** при запуске).

### Планы проектирования (режим "План")

Режим "План" использует только стандартный ACP. Агент сохраняет файлы инструментами **`plan_write`** / **`plan_list`** (это не расширения JSON-RPC).

После **`plan_write`** Coddy публикует:

- **`session/update`** с `sessionUpdate: "plan"` и пунктами чек-листа **`entries`** из frontmatter файла (превью для любого клиента ACP);
- **`_meta`** в этом обновлении - `coddy.dev/planSlug`, `coddy.dev/planKind: "design"` (по желанию; отличает планы проектирования от живых обновлений чек-листа задач);
- сохранённую строку **`plan_document`** в **`messages.json`** для встроенного веб-интерфейса (она видна и как markdown ассистента в чате, когда модель пересказывает план).

**Запустить план** (начать реализацию) без собственного метода `_coddy/*` можно двумя способами:

1. **С поддержкой Coddy** - `session/prompt` с `_meta`.

```json
{
  "sessionId": "sess_…",
  "prompt": [{ "type": "text", "text": "Implement the plan." }],
  "_meta": { "coddy.dev/runPlanSlug": "my-feature" }
}
```

Coddy переключается в режим **Агент**, подставляет тело плана в системный промпт и запускает ход. Список задач сессии (`todos/active.md`) из плана проектирования **не** заполняется автоматически. В режиме **Чат** хук отклоняется с ошибкой, а упоминание `@plans/<slug>.plan.md` только вставляется как материал для чтения, поэтому сначала переключите режим, а потом запускайте план.

2. **Переносимый** - клиент ставит `mode` в **agent**, затем отправляет `session/prompt` со ссылкой на `@plans/<slug>.plan.md` или с текстом вроде *реализуй план my-feature*.

HTTP **`POST /v1/responses`** принимает тот же хук через JSON **`metadata.runPlanSlug`** (встроенный веб-интерфейс). CRUD для файлов планов есть только в HTTP, в **`/coddy/sessions/{id}/plans`** (он не входит в основной ACP).

### `session/prompt`

Отправляет сообщение пользователя и запускает цикл ReAct.

**Параметры запроса.**
```json
{
  "sessionId": "sess_abc123def456",
  "prompt": [
    {
      "type": "text",
      "text": "Refactor the auth module to use JWT"
    }
  ],
  "_meta": {
    "coddy.dev/runPlanSlug": "optional-slug-for-run-plan"
  }
}
```

**Результат ответа.**
```json
{
  "stopReason": "end_turn"
}
```

Причины остановки - `end_turn` | `max_tokens` | `max_turns` | `agent_refused` | `cancelled`

**Упоминания и блоки контекста.** Coddy объявляет `promptCapabilities.embeddedContext`, поэтому клиент вроде Zed отправляет то, что выбрали в его меню упоминаний, блоками содержимого рядом с текстом:

- `resource` с `text` прикрепляется в том виде, в каком отправлен (Zed включает несохранённые правки). Ресурс `file://` называется по своему пути, относительному, если файл лежит в `cwd` сессии, а фрагмент строк (`#L10-20`, `#L10:20`, `#L10-L20`, `#L10`) помечает вложение этими строками; параметр запроса вроде `?symbol=` у Zed отбрасывается;
- `resource` без `text` читается с диска, где бы клиент его ни назвал, будь то файл, диапазон строк в нём или листинг папки. Отсутствующий файл или строки за концом файла проваливают промпт, потому что клиент запросил их явно;
- `resource_link` (блок, который обязан принимать каждый агент ACP) на локальный файл или папку читается как упоминание этого файла или папки; ссылка, которую Coddy сам открыть не может (внутренний URI редактора, веб-адрес), доходит до модели строкой, которая её называет.

Упоминания `@`, набранные в блоке `text`, разрешаются так же, как в любом интерфейсе, - файлы в любом месте диска, папки, диапазоны строк, `@session:<id>`, `@rule:<name>`, `@agent:<name>`, `@coddy:<page>#<section>`, веб-страницы - во вложения того же сообщения пользователя ([Упоминания](../features/mentions.md)). Упоминание никогда не проваливает промпт, и упоминание, которое ничего не называет, остаётся обычным текстом.

### Запуски субагентов и дочерние сессии (особенность Coddy)

Когда агент передаёт работу субагенту (`docs/features/subagents.md`), на уровне протокола ничего не меняется. Строки `tool_call` / `tool_call_update` родителя несут вызов `spawn_agent` и его результат; дочерний агент работает в своей сессии, и **его обновления никогда не доходят до клиента ACP** - ход работы дочернего агента идёт в журнал вывода фоновой задачи, поэтому редактору никогда не приходит `session/update` для сессии с id, который он не создавал. Единственное сообщение, которое клиент может получить от имени дочернего агента, - `session/request_permission`, пока порождающий ход ещё идёт; оно приходит с `sessionId` **родителя** и `toolCall.title` с префиксом `[subagent <name>]`, и на него отвечают как на любое другое. После того как этот ход вернулся, запросы дочернего агента отклоняются, не доходя до клиента.

Дочерние сессии - транскрипты только для чтения. `session/list` их опускает, `session/load` воспроизводит такую сессию как любой другой бандл, а `session/prompt` с id дочерней сессии возвращает ошибку, которая называет родителя (`subagent sessions are read-only transcripts: sess_… belongs to sess_…`); хук `_meta` для запуска плана закрыт той же проверкой. По самому id не понять, какая сессия дочерняя, это говорят метаданные `subagentRun` бандла.

### `session/cancel`

Отменяет идущий ход промпта (уведомление).

```json
{
  "jsonrpc": "2.0",
  "method": "session/cancel",
  "params": {
    "sessionId": "sess_abc123def456"
  }
}
```

Для бандла сессии, доступного для записи, Coddy ещё и пишет на диск небольшой сигнал отмены, чтобы другой процесс **`coddy`** (например, **`coddy serve`**, пока **`coddy acp`** выполняет ход) мог заметить кооперативную отмену между тиками опроса во время хода. Ход внутри процесса по-прежнему завершается через тот же хук отмены **`TurnCtx`**, когда сессия загружена в этом процессе.

### `session/set_mode`

Переключает режимы агента (устаревший API). Если клиент поддерживает Session Config Options, предпочитайте `session/set_config_option` с `configId` `mode`.

**Параметры запроса.**
```json
{
  "sessionId": "sess_abc123def456",
  "modeId": "plan"
}
```

**Результат ответа** - `null`.

Когда режим меняется, агент ещё и отправляет `session/update` с `config_option_update`, чтобы клиенты, которые используют параметры конфигурации, оставались синхронизированы (например, показанная модель по умолчанию может смениться, если у сессии не задано переопределение модели).

### `session/set_config_option`

Меняет параметр конфигурации сессии (ACP Session Config Options). Поддерживаемые параметры - **`mode`**, **`model`**, **`reasoning`** (предлагается, когда у модели сессии есть уровни), **`permission_mode`**. Каждое изменение проходит через тот же сеттер, что и команды настроек и другие интерфейсы, поэтому браузер или консоль, которые следят за сессией, его видят ([Настройки сессии](../features/session-settings.md)).

**Параметры запроса.**
```json
{
  "sessionId": "sess_abc123def456",
  "configId": "permission_mode",
  "value": "accept_edits"
}
```

Допустимые значения `permission_mode` - `ask` | `accept_edits` | `bypass`. Переопределение действует в пределах сессии и имеет приоритет над значением `tools.permission_mode` из файла конфигурации, но хранится только в памяти агента, и перезапуск агента возвращает сессию к настроенному режиму. Параметр объявляется в категории `_permission_mode`, уровень рассуждения - в `thought_level`.

У изменения только на следующие ходы нет параметра конфигурации. Отправьте команду настроек в начале текста промпта (`/model <id> --once review this`, `/nothink --count=3`), и агент снимет её до начала хода.

**Результат ответа** - полный массив `configOptions` с обновлёнными полями `currentValue`.

```json
{
  "configOptions": [ ... ]
}
```

Неизвестный `configId` или `value`, которого нет среди значений этого параметра, даёт ошибку JSON-RPC (invalid params).

## Уведомления (агент → клиент)

Все они отправляются методом `session/update` с полем-дискриминатором `sessionUpdate`.

### `plan` - план выполнения агента

```json
{
  "sessionUpdate": "plan",
  "entries": [
    { "content": "Read auth module", "priority": "high", "status": "pending" },
    { "content": "Design JWT structure", "priority": "high", "status": "pending" },
    { "content": "Implement changes", "priority": "medium", "status": "pending" }
  ]
}
```

### `available_commands_update` - слэш-команды

После **`session/new`** и **`session/load`** Coddy перечисляет свои слэш-команды. Встроенные команды идут первыми, это те же строки, что и в **`GET /coddy/commands`**, то есть команды настроек (**`model`**, **`reasoning`**, **`think`**, **`nothink`**, **`agent`**, **`plan`**, **`ask`**, **`permissions`**), затем **`compact`**, пока включено сжатие контекста, **`export`** и **`plugin`**. За ними следуют скилы из того же конвейера **`ListSkills`**, что и у **`GET /coddy/slash-commands`**, кроме скила, названного как встроенная команда или её алиас. Ответ, который регистрирует сессию, записывается до этого уведомления, поэтому клиенты не отбрасывают каталог как обновление для неизвестной сессии. Строки используют поля ACP **`name`**, **`description`** и, для команды настроек, **`input.hint`** с её аргументом и флагами ([слэш-команды](https://agentclientprotocol.com/protocol/slash-commands)). Агент может повторять это уведомление всякий раз, когда каталог меняется.

```json
{
  "sessionUpdate": "available_commands_update",
  "availableCommands": [
    { "name": "model", "description": "Switch the model for this session, or for the next turns with --once / --count=N", "input": { "hint": "<model id> [--once|--count=N]" } },
    { "name": "demo", "description": "Runs the demo checklist" }
  ]
}
```

### `agent_message_chunk` - фрагмент текстового ответа

```json
{
  "sessionUpdate": "agent_message_chunk",
  "content": {
    "type": "text",
    "text": "I'll start by reading the current auth module..."
  }
}
```

### `tool_call` - вызов инструмента начат

```json
{
  "sessionUpdate": "tool_call",
  "toolCallId": "call_001",
  "title": "Reading auth.go",
  "kind": "read",
  "status": "pending"
}
```

### `tool_call_update` - обновление статуса вызова инструмента

```json
{
  "sessionUpdate": "tool_call_update",
  "toolCallId": "call_001",
  "status": "completed",
  "content": [
    {
      "type": "content",
      "content": { "type": "text", "text": "File contents: ..." }
    }
  ]
}
```

Статусы вызова инструмента - `pending` | `in_progress` | `completed` | `failed` | `cancelled`

### Собственные обновления сессии Coddy (`token_usage`, `usage_update`, `provider_usage`)

Coddy отправляет три вида `session/update`, которых нет в спецификации
ACP; клиент, который игнорирует неизвестные виды, продолжает работать.

- **`token_usage`** после каждого завершённого вызова модели - `inputTokens`,
  `outputTokens` этого вызова и `totalTokens`, накопленный за ход.
  `inputTokens` охватывает весь промпт вызова, включая часть, отданную из кеша
  промптов провайдера; эта часть передаётся как `cachedInputTokens` и опускается,
  если провайдер её не сообщил;
- **`usage_update`**, когда меняется заполнение окна контекста (вызов модели,
  ручное или автоматическое сжатие контекста), - `used` и `size` в токенах;
- **`provider_usage`** - квота аккаунта у провайдера модели сессии, чтобы
  клиент мог нарисовать строку состояния, как третья строка подвала консоли.
  Отправляется, когда сессия готова, и после каждого хода (никогда для дочерней
  сессии субагента), если у провайдера есть источник данных о расходе и панель строки
  включена (`providers[].usage_limits_panel`, по умолчанию true); типы, у которых сейчас
  есть источник, - `neuraldeep` (`GET /v1/limits` хаба, только чтение), `codex`
  (эндпоинт расхода бэкенда Codex) и `devin` (RPC статуса
  управления местами). Снимок охватывает весь аккаунт
  и кешируется менеджером (20 с, не чаще одного чтения в 15 с); относительные
  длительности (`resetInSec`, `retryInSec`, `rate.resetInSec`) при доставке поправляются
  на возраст снимка. Сумм в долларах и учётных данных там не бывает
  никогда. HTTP-клиенты читают ту же структуру из
  `GET /coddy/providers/{name}/usage` (см. `docs/reference/http-api.md`). Если
  включён `agent.wait_for_limit_reset`, ход, который упёрся в лимит, который повторы
  никогда бы не покрыли, сам отправляет то же обновление каждые 20 с, пока ждёт,
  с `blocked`, `retryAt`, `retryInSec` и `resuming: true`; затем он
  повторяет вызов, а снимок в конце следующего хода заменяет это обновление,
  например
  `{"sessionUpdate": "provider_usage", "provider": "neuraldeep",
  "providerType": "neuraldeep", "blocked": true, "retryAt":
  "2026-09-06T20:59:59Z", "retryInSec": 767, "resuming": true}`.

```json
{
  "sessionUpdate": "provider_usage",
  "provider": "neuraldeep",
  "providerType": "neuraldeep",
  "observedAt": "2026-09-06T17:47:02Z",
  "fetchedAt": "2026-09-06T17:47:10Z",
  "plan": "pro",
  "keyName": "coddy",
  "windows": [
    {"id": "session", "label": "3h", "used": 407, "limit": 15000, "remaining": 14593,
     "usedPercent": 2.71, "resetsAt": "2026-09-06T17:59:59Z", "resetInSec": 777},
    {"id": "week", "label": "week", "used": 9981, "limit": 150000, "remaining": 140019,
     "usedPercent": 6.65, "resetsAt": "2026-09-07T00:00:00Z", "resetInSec": 22378},
    {"id": "day", "label": "day", "usedPercent": 0, "resetsAt": "2026-09-07T00:00:00Z", "resetInSec": 22378}
  ],
  "rate": {"used": 2, "limit": 120, "remaining": 118, "resetInSec": 58},
  "wallet": {"balanceRub": -1229.24, "spentRub30d": 2000.74},
  "blocked": false,
  "unlimitedModels": ["qwen3.6-35b-a3b"],
  "blockedModels": [{"model": "kimi-k2.6", "blocker": "kimi_budget_exhausted",
                     "retryAt": "2026-10-09T20:15:41Z", "retryInSec": 2860119}]
}
```

`blocked: true` приходит вместе с `blockers` (`session_exhausted`, `week_exhausted`,
`rpm_exhausted`, `session_cooldown`, `abuse_cooldown`,
`daily_capacity_exhausted`, `key_blocked`, `key_cap_blocked`, `wallet_empty`,
`user_blocked`) и, для ограничений по времени, с `retryAt` / `retryInSec`.
`unlimited: true` отмечает ключ без окон объёма; `unlimitedModels` перечисляет
id моделей в апстриме, которые обходят эти окна на ключе с учётом расхода (клиент сравнивает
часть селектора модели после первого `/`). `blockedModels` устроен
зеркально, это id моделей, которые ключ сейчас **не** может вызывать, каждый со своим `blocker` и,
если ограничение временное, с `retryAt` / `retryInSec`. Такое ограничение закрывает часть
каталога, а не класс чатов целиком, поэтому `blocked` остаётся `false`, и
аккаунт работает для всех остальных моделей. Клиент, который читает только `blocked`,
показывает здоровый аккаунт, хотя каждый запрос к выбранной модели отклоняется.
Сопоставляйте этот список так же, как `unlimitedModels`. Неудачное чтение сохраняет
прежние окна с `stale: true` и `error` (`unavailable`, `invalid`),
кроме отклонённого ключа. `error: unauthorized` приходит без окон, потому что
числа, прочитанные с ключом, который хаб больше не принимает, уже не числа
аккаунта. `unsupported: true` отвечает маршрут REST для
типа провайдера без источника, а вместе с `disabled: true` - для строки,
у которой выключена панель лимитов (`providers[].usage_limits_panel:
false`); такая строка никогда не читается, и обновление для неё не отправляется.

### `memory_run` - запуск субагента памяти в ходе

Когда `memory.enable` равен true, каждый ход пользователя запускает **субагента памяти**, дочерний агент в пуле фоновых задач со своим бандлом сессии и журналом задачи ([Долговременная память](../features/memory.md)). Это обновление сообщает о таком запуске, но не передаёт ничего из его текста. `started` приходит, как только задача запущена, `finished` - когда она завершилась, пока ход ещё шёл (запуск, который переживает ход, больше ничего не отправляет), а `skipped` - когда запуск не удалось начать. Это обновление не сохраняется и не воспроизводится через `session/load`; запись о запуске - это задача (вид `agent`, `agent.system` равен true) и дочерний транскрипт, который она называет.

| Поле | Значение |
|---|---|
| `status` | `started`, `finished` или `skipped` |
| `taskId`, `childSessionId` | задача пула и дочерняя сессия запуска; пусто при пропуске |
| `taskStatus` | вердикт пула при `finished` - `succeeded`, `failed`, `timed_out`, `stopped` |
| `durationMs` | сколько длился запуск, при `finished` |
| `delivered` | дошёл ли непустой отчёт до основной модели в этом ходе, в блоке контекста хода первого запроса или более позднего шага |
| `reason` | почему запуск пропущен или на какой ошибке закончился неудачный запуск |

```json
{"sessionUpdate": "memory_run", "status": "started", "taskId": "bg_3", "childSessionId": "sess_9f1c2a7d4e5b6c8d9e0f1a2b"}
{"sessionUpdate": "memory_run", "status": "finished", "taskId": "bg_3", "childSessionId": "sess_9f1c2a7d4e5b6c8d9e0f1a2b", "taskStatus": "succeeded", "durationMs": 3210, "delivered": true}
{"sessionUpdate": "memory_run", "status": "finished", "taskId": "bg_4", "childSessionId": "sess_0a1b2c3d4e5f60718293a4b5", "taskStatus": "failed", "durationMs": 1200, "reason": "402 Payment Required: subscription expired"}
{"sessionUpdate": "memory_run", "status": "skipped", "reason": "memory runs in flight for this session: 2 of 2"}
```

Между `started` и `finished` клиент показывает фразу `Working with memory` и убирает её, когда ход заканчивается. Ждать `finished` нельзя, потому что запуск, который переживает ход, его никогда не отправляет. Консоль печатает одну строку, когда запуск завершается внутри хода. Обновлений `memory_phase` и `memory_message_chunk` из прежних релизов больше нет.

### `background_wake` - ход, который никто не набирал

Задача, которую модель запустила с `notify_on_finish`, будит агента, когда заканчивается ([Фоновые задачи](../features/background-tasks.md#пробуждение-агента-по-завершении-задачи)). Под `coddy acp` разбуженный ход выполняется с клиентом в роли отправителя. Его уведомления `session/update` приходят вне любого `session/prompt`, который отправлял клиент, а об инструменте, требующем разрешения, внутри него спрашивают через `session/request_permission`, как и в любом другом ходе. Ход открывается этим обновлением, которое отправляется до сохранения его первого сообщения и вместо этого сообщения. Это сообщение - инструкция, которую читает модель, и оно не отправляется как `user_message_chunk` ни вживую, ни при `session/load`.

| Поле | Значение |
|---|---|
| `tasks` | все задачи, о которых сообщает ход, в порядке завершения |
| `tasks[].id` | id задачи (`bg_3`) |
| `tasks[].kind` | `command` или `agent` |
| `tasks[].label` | команда или описание запуска субагента |
| `tasks[].agent` | определение субагента, стоящее за запуском агента |
| `tasks[].status` | `succeeded`, `failed`, `timed_out` или `stopped` |
| `tasks[].exitCode` | код выхода команды; у запуска агента он принадлежит пулу и ничего не говорит |
| `tasks[].durationMs` | сколько длилась задача |
| `tasks[].error` | что пошло не так, если пул что-то записал |

```json
{"sessionUpdate": "background_wake", "tasks": [{"id": "bg_3", "kind": "command", "label": "make test", "status": "failed", "exitCode": 2, "durationMs": 90000, "error": "exit status 2"}]}
```

Редактор, который показывает только стандартные обновления, показал бы ответ, о котором никто не просил, поэтому `coddy acp` после этого обновления отправляет то же пробуждение цитатой в `agent_message_chunk` в начале ответа, вживую и при `session/load`.

```text
> Woken by a finished background task: bg_3 make test, failed, exit 2, 1m 30s
```

`coddy acp --remote` так же воспроизводит разбуженный ход сервера при `session/load`, но вживую за разбуженными ходами сервера не следит.

### `current_mode_update` - режим изменён

```json
{
  "sessionUpdate": "current_mode_update",
  "currentModeId": "agent"
}
```

### `config_option_update` - параметры конфигурации сессии изменены

Отправляется после `session/set_config_option`, после `session/set_mode` или всякий раз, когда агент обновляет параметры конфигурации сессии под состояние во время работы (чтобы интерфейс оставался согласованным).

```json
{
  "sessionUpdate": "config_option_update",
  "configOptions": [
    {
      "id": "mode",
      "name": "Session mode",
      "category": "mode",
      "type": "select",
      "currentValue": "agent",
      "options": [ ... ]
    },
    {
      "id": "model",
      "name": "Model",
      "category": "model",
      "type": "select",
      "currentValue": "openai/gpt-5.6-terra",
      "options": [ ... ]
    },
    {
      "id": "permission_mode",
      "name": "Permission mode",
      "category": "_permission_mode",
      "type": "select",
      "currentValue": "accept_edits",
      "options": [ ... ]
    }
  ]
}
```

## Запросы разрешения (агент → клиент, ожидается ответ)

Эти запросы отправляются, только когда `permission_mode` равен `ask` (команды и запись) или `accept_edits` (только команды). Когда `permission_mode` равен `bypass`, агент никогда не отправляет `session/request_permission`. Режим задаётся через `session/set_config_option`, командой `/permissions` в промпте или ключом `tools.permission_mode` в `config.yaml`.

При `ask` запрос, который относится к самой сессии, несёт перед Reject ещё два варианта вида `allow_always` - `allow_session_bypass` (**Bypass for this session**) и, для записи файла, `allow_session_accept_edits` (**Allow edits for this session**). Выбор любого из них одобряет вызов и переключает режим разрешений сессии, о чём агент сообщает через `config_option_update`. Запрос, переданный от субагента, запрос, который вынудил хук, и `config_commit` / `config_rollback` их не несут.


```json
{
  "jsonrpc": "2.0",
  "id": 10,
  "method": "session/request_permission",
  "params": {
    "sessionId": "sess_abc123def456",
    "toolCall": {
      "toolCallId": "call_002",
      "title": "Run: go build ./...",
      "kind": "run_command",
      "status": "pending",
      "content": [
        { "type": "text", "text": "Execute: go build ./..." }
      ]
    },
    "options": [
      { "optionId": "allow", "name": "Allow", "kind": "allow_once" },
      { "optionId": "allow_always", "name": "Allow always", "kind": "allow_always" },
      { "optionId": "reject", "name": "Reject", "kind": "reject_once" }
    ]
  }
}
```

**Ответ.** Протокол вкладывает исход в отдельный объект, и именно так отвечают редакторы вроде Zed.

```json
{
  "jsonrpc": "2.0",
  "id": 10,
  "result": {
    "outcome": { "outcome": "selected", "optionId": "allow" }
  }
}
```

Запрос, закрытый без выбора, отвечает `{ "outcome": { "outcome": "cancelled" } }`. Coddy принимает и плоскую форму, которую отправляют его собственные интерфейсы и некоторые расширения редакторов (`{"outcome": "selected", "optionId": "allow"}`), и читает обе одинаково - вызов выполняется, если исход не `cancelled` и выбранный `optionId` не `reject`. Выбор `allow_always` (или варианта `allow_always_<program>` для всей программы) ещё и сохраняет разрешение на сессию, чтобы та же команда больше не спрашивала.

## Запросы с вопросами (агент → клиент, ожидается ответ)

Их использует инструмент **`question`**. Схема входящих JSON-RPC та же, что и у запросов разрешения (клиент обязан ответить с тем же **`id`**).

```json
{
  "jsonrpc": "2.0",
  "id": 11,
  "method": "session/request_question",
  "params": {
    "sessionId": "sess_abc123def456",
    "requestId": "q_1730000000000",
    "toolCallId": "call_003",
    "questions": [
      {
        "question": "Pick a stack",
        "options": [{ "label": "Go" }, { "label": "Rust" }]
      }
    ]
  }
}
```

**Ответ.**
```json
{
  "jsonrpc": "2.0",
  "id": 11,
  "result": {
    "answers": [["Go"]]
  }
}
```

## Методы файловой системы клиента

Агент может вызывать у клиента эти методы (если клиент их поддерживает).

### `fs/read_text_file`

```json
{
  "jsonrpc": "2.0",
  "id": 5,
  "method": "fs/read_text_file",
  "params": { "path": "/absolute/path/to/file.go" }
}
```

### `fs/write_text_file`

```json
{
  "jsonrpc": "2.0",
  "id": 6,
  "method": "fs/write_text_file",
  "params": {
    "path": "/absolute/path/to/file.go",
    "content": "package main\n..."
  }
}
```

<!-- docsgen:source sha256=2a21d5246b1001be -->
