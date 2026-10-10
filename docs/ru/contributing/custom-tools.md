# Руководство по собственным инструментам

## Обзор

Агент открывает свои возможности LLM через **реестр инструментов** (`internal/tools`). Каждое
действие агента - чтение файлов, запуск команд, поиск по коду - реализовано как инструмент. Это
руководство объясняет, как добавить новые встроенные инструменты прямо в бинарник агента.

Чтобы добавить инструменты, не меняя исходный код агента, используйте вместо этого
[MCP-серверы](../features/mcp.md).

## Как устроена система инструментов

```
User prompt
    │
    ▼
ReAct loop (internal/agent/react.go)
    │
    ├── tools.NewRegistryFor(cfg) registers builtins (internal/tools/export.go)
    │
    ├── Registry.AllToolDefinitions()
    │
    ├── FilterToolDefinitions(..., ToolSetForMode(mode))  ← internal/agent/toolsets.go
    │
    ├── (agent or plan mode, never ask) append MCP tool definitions
    │
    ├── narrow to the session model's models[].tools / disallowed_tools  ← internal/agent/model_tools.go
    │
    ├── passes tool definitions to LLM via provider.Stream()
    │
    └── on tool_call in LLM response:
            │
            ├── permission check (ACP)
            │
            └── registry.Execute(name, argsJSON, env)  →  tool.Execute()
```

LLM получает список **определений** инструментов (имя, описание, JSON Schema). Когда LLM решает
вызвать инструмент, агент выполняет его и возвращает результат в диалог.

## Структура `Tool`

Каждый инструмент - значение типа `tools.Tool`, псевдонима для `tooling.Tool`, который определён в
[`internal/tooling/tool.go`](../../../internal/tooling/tool.go).

```go
type Tool struct {
    Definition llm.ToolDefinition

    // RequiresPermission indicates the tool needs user approval before running.
    RequiresPermission bool

    Execute func(ctx context.Context, argsJSON string, env *Env) (string, error)
}
```

### `llm.ToolDefinition`

Именно это видит LLM.

```go
type ToolDefinition struct {
    Name        string      `json:"name"`
    Description string      `json:"description"`
    InputSchema interface{} `json:"input_schema"` // JSON Schema object
}
```

- `Name` - уникальный идентификатор в snake_case, например `fetch_url`, `git_log`;
- `Description` - описание простым английским языком; по нему LLM решает, когда вызывать инструмент;
- `InputSchema` - стандартная [JSON Schema](https://json-schema.org/), которая описывает аргументы инструмента.

### `tools.Env`

Структура `Env` передаётся в каждый вызов `Execute` и даёт контекст сессии.

```go
type Env struct {
    CWD              string   // session working directory
    PermissionMode   string   // "ask", "accept_edits", or "bypass"
    CommandAllowlist []string // commands that skip permission checks

    // Plan/todo support (always populated by the ReAct agent):
    SessionID string                      // current session ID
    Sender    acp.UpdateSender            // sends session/update to connected ACP client
    GetPlan   func() []acp.PlanEntry      // read current todo list
    SetPlan   func([]acp.PlanEntry)       // replace todo list
}
```

Относительные пути отсчитывайте от `env.CWD`. Чтобы превратить пути, которые передал пользователь,
в абсолютные, используйте `resolvePath(path, env.CWD)` (вспомогательную функцию, закрытую в пакете).

Если инструменту нужно отправить клиенту состояние плана, вызовите `sendPlanUpdate(env, entries)` -
закрытую вспомогательную функцию пакета из `internal/tools/todo.go`, которая перед отправкой
проверяет `Sender` на nil.

## Создание встроенного инструмента шаг за шагом

### 1. Создайте конструктор инструмента

Добавьте новый файл `.go` в `internal/tools/` (или допишите код в существующий файл, если он близок по теме).

```go
package tools

import (
    "context"
    "fmt"
    "net/http"

    "github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// fetchURLTool returns a tool that performs an HTTP GET request.
func fetchURLTool() *Tool {
    return &Tool{
        Definition: llm.ToolDefinition{
            Name:        "fetch_url",
            Description: "Perform an HTTP GET request and return the response body as text.",
            InputSchema: map[string]interface{}{
                "type": "object",
                "properties": map[string]interface{}{
                    "url": map[string]interface{}{
                        "type":        "string",
                        "description": "Full URL to fetch, e.g. https://example.com/api/data",
                    },
                },
                "required": []string{"url"},
            },
        },
        RequiresPermission: false,
        Execute:            executeFetchURL,
    }
}
```

### 2. Определите структуру аргументов

```go
type fetchURLArgs struct {
    URL string `json:"url"`
}
```

### 3. Реализуйте функцию `Execute`

```go
func executeFetchURL(ctx context.Context, argsJSON string, _ *Env) (string, error) {
    args, err := parseArgs[fetchURLArgs](argsJSON)
    if err != nil {
        return "", err
    }

    req, err := http.NewRequestWithContext(ctx, http.MethodGet, args.URL, nil)
    if err != nil {
        return "", fmt.Errorf("fetch_url: invalid request: %w", err)
    }

    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        return "", fmt.Errorf("fetch_url: %w", err)
    }
    defer resp.Body.Close()

    body := make([]byte, 0, 4096)
    buf := make([]byte, 512)
    for {
        n, readErr := resp.Body.Read(buf)
        body = append(body, buf[:n]...)
        if readErr != nil {
            break
        }
    }

    return fmt.Sprintf("HTTP %d\n%s", resp.StatusCode, string(body)), nil
}
```

Главные правила для `Execute`:
- возвращайте `("", error)` при настоящих ошибках (неверные аргументы, сбой ввода-вывода и т. п.);
- возвращайте `(errorMessage, nil)`, если команда выполнилась, но с неуспешным результатом, - так
  LLM увидит сбой и решит, что делать дальше;
- используйте `context.Context` для отмены и тайм-аутов;
- используйте `parseArgs[T]`, чтобы разобрать JSON-аргументы в типизированную структуру.

### 4. Зарегистрируйте инструмент в `NewRegistryFor()`

Откройте `internal/tools/export.go` и добавьте `r.Register(...)` внутри `NewRegistryFor()` (или зарегистрируйте инструмент из вспомогательной функции вроде `internal/tools/fs/register.go`, если расширяете набор инструментов файловой системы).

```go
func NewRegistryFor(cfg *config.Config) *Registry {
    r := tooling.NewRegistry()
    // ...
    r.Register(fetchURLTool()) // <-- add here
    registerSchedulerTools(r, cfg)
    return r
}
```

Это всё. После пересборки (`go build ./...`) LLM увидит новый инструмент.

## Справочник по полям инструмента

### Видимость в режимах "План" и "Агент"

Видимость по режимам **не** настраивается в структуре `Tool`. Вместо этого `internal/agent/toolsets.go`
определяет список разрешённых инструментов `ToolSet`:

- режим **`agent`** использует **пустой** `ToolSet`, то есть работает **без фильтрации** - модели предлагается каждый инструмент из реестра, а инструменты MCP добавляются в конец;
- режим **`plan`** сужает встроенные инструменты **реестра** до **фиксированного списка** (`read_file`, `list_dir`, `search_files`, `websearch`, `webfetch`, `run_command`). Остальные встроенные инструменты (запись, инструменты todo, планировщик, память и т. п.) остаются зарегистрированными, чтобы выполнение было согласованным, но **скрыты** от LLM. Определения инструментов **MCP** с подключённых серверов **всё равно добавляются** после этого фильтра, так же как в режиме "Агент".

Если новый встроенный инструмент должен быть безопасен для режима "План", добавьте его имя в `planToolNames` в `internal/agent/toolsets.go` и дополните тесты в `internal/agent/toolsets_test.go`.

### `RequiresPermission`

При `true` цикл ReAct перед вызовом инструмента останавливается и отправляет ACP-клиенту
уведомление `session/request_permission`. Выполнение продолжится только после того, как
пользователь одобрит вызов.

Ставьте `RequiresPermission: true` для:
- инструментов, которые запускают внешние процессы или команды оболочки;
- инструментов, которые удаляют или необратимо меняют данные за пределами явно читающих сценариев.

Сетевые инструменты могут обходиться `RequiresPermission: false`, если реализация сама обеспечивает защиту (например, проверки SSRF и ограничения размера ответа у `webfetch`). Сетевой инструмент, запрос которого модель формирует свободно, как `http_request`, ставит `RequiresPermission: true` и получает собственную ветку в проверке разрешений (`permission.HTTPRequestAllowedWithSession`), которая решает по адресу назначения и по тому, что несёт запрос.

Ставьте `RequiresPermission: false` для:
- операций только для чтения;
- записи внутри рабочей папки (это можно регулировать через `env.RequirePermissionForWrites`).

## JSON Schema для `InputSchema`

`InputSchema` - стандартный объект JSON Schema. Самые частые шаблоны приведены ниже.

### Обязательное строковое поле

```go
"url": map[string]interface{}{
    "type":        "string",
    "description": "URL to fetch",
},
```

### Необязательное целочисленное поле со значением по умолчанию

```go
"timeout_seconds": map[string]interface{}{
    "type":        "integer",
    "description": "Timeout in seconds (default: 30)",
},
```

### Поле-перечисление

```go
"method": map[string]interface{}{
    "type":        "string",
    "enum":        []string{"GET", "POST", "PUT", "DELETE"},
    "description": "HTTP method",
},
```

### Логическое поле

```go
"follow_redirects": map[string]interface{}{
    "type":        "boolean",
    "description": "Follow HTTP redirects (default: true)",
},
```

### Пометка обязательных полей

```go
"required": []string{"url", "method"},
```

## Полный пример - инструмент `git_log`

Ниже полный, готовый к работе пример инструмента, который запускает `git log` в рабочей
папке и возвращает отформатированную сводку.

Файл **`internal/tools/git.go`**.

```go
package tools

import (
    "bytes"
    "context"
    "fmt"
    "os/exec"

    "github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func gitLogTool() *Tool {
    return &Tool{
        Definition: llm.ToolDefinition{
            Name:        "git_log",
            Description: "Show recent git commit history for the working directory repository.",
            InputSchema: map[string]interface{}{
                "type": "object",
                "properties": map[string]interface{}{
                    "limit": map[string]interface{}{
                        "type":        "integer",
                        "description": "Number of commits to show (default: 10)",
                    },
                    "branch": map[string]interface{}{
                        "type":        "string",
                        "description": "Branch name (default: current branch)",
                    },
                },
            },
        },
        RequiresPermission: false,
        Execute:            executeGitLog,
    }
}

type gitLogArgs struct {
    Limit  int    `json:"limit"`
    Branch string `json:"branch"`
}

func executeGitLog(ctx context.Context, argsJSON string, env *Env) (string, error) {
    args, err := parseArgs[gitLogArgs](argsJSON)
    if err != nil {
        return "", err
    }

    limit := 10
    if args.Limit > 0 {
        limit = args.Limit
    }

    cmdArgs := []string{
        "log",
        fmt.Sprintf("--max-count=%d", limit),
        "--oneline",
        "--decorate",
    }
    if args.Branch != "" {
        cmdArgs = append(cmdArgs, args.Branch)
    }

    cmd := exec.CommandContext(ctx, "git", cmdArgs...)
    cmd.Dir = env.CWD

    var out bytes.Buffer
    cmd.Stdout = &out
    cmd.Stderr = &out

    if err := cmd.Run(); err != nil {
        return fmt.Sprintf("git log failed: %v\n%s", err, out.String()), nil
    }

    result := out.String()
    if result == "" {
        return "(no commits)", nil
    }
    return result, nil
}
```

Затем в `export.go`.

```go
r.Register(gitLogTool())
```

## Чек-лист

Перед отправкой нового инструмента проверьте:

- [ ] имя инструмента уникально и следует соглашению `snake_case`;
- [ ] описание ясно объясняет, что делает инструмент и когда его использовать;
- [ ] все обязательные поля перечислены в `InputSchema.required`;
- [ ] у необязательных полей есть разумные значения по умолчанию, указанные в их описании;
- [ ] если инструмент должен быть виден в режиме **План**, его имя добавлено в `planToolNames` в `internal/agent/toolsets.go`, а `internal/agent/toolsets_test.go` дополнен;
- [ ] `RequiresPermission` равно `true` для разрушительных команд оболочки и записи чувствительных данных;
- [ ] `Execute` возвращает `("", error)` при ошибках разбора аргументов;
- [ ] `Execute` возвращает `(errorMessage, nil)` при сбоях во время выполнения, чтобы LLM их видела;
- [ ] конструктор инструмента зарегистрирован в `NewRegistry()`;
- [ ] `go build ./...` и `go test ./...` проходят.

## Встроенные инструменты плана и todo

Чек-листом управляют семь инструментов **`coddy_`** (`internal/tools/todo`). Изменения плана
отправляют `session/update` с содержимым **`plan`**. Эти инструменты регистрируются для каждой сессии, но **предлагаются LLM только в режиме `agent`** (режим плана скрывает их через `ToolSet`).

| Имя инструмента | Назначение |
|-----------|---------|
| **`coddy_todo_plan_read`** | Возвращает активный план в виде markdown-чек-листа (`{}`); если активного плана нет, возвращает однострочную пометку с именем **`coddy_todo_plan_replace`** вместо пустой строки. |
| **`coddy_todo_plan_replace`** | Заменяет весь план (`markdown`). Отклоняется, пока есть незавершённая строка, если сначала не вызвать **`coddy_todo_plan_archive`**. Завершённые списки перед заменой архивируют **`todos/active.md`**. |
| **`coddy_todo_plan_archive`** | Помечает каждую незавершённую строку как **`completed`**, записывает **`todos/archive/plan_<unix_seconds>.md`**, если задан **`SessionDir`**, очищает план в памяти и отправляет пустой **`plan`**. |
| **`coddy_todo_item_add`** | Добавляет строку в конец или вставляет её (`content`, необязательные `status` и `after_index`; `-1` вставляет в начало). |
| **`coddy_todo_item_remove`** | Удаляет строку (`index`). |
| **`coddy_todo_item_update`** | Изменяет строку (`index` плюс **`content`** и/или **`status`**). Допустимые статусы - `pending`, `in_progress`, `completed`, `failed`, `cancelled`. |
| **`coddy_todo_item_move`** | Переставляет строку (`from_index`, `to_index`; индексы считаются с учётом удаления строки со старого места). |

Пример полной замены.

```json
{
  "markdown": "- [ ] Read existing code\n- [ ] Write tests\n- [ ] Implement feature\n- [ ] Update docs"
}
```

Пример смены статуса.

```json
{ "index": 0, "status": "in_progress" }
```

Редакторы с поддержкой ACP показывают состояния плана в своём интерфейсе. Маркеры Markdown примерно соответствуют статусам из таблицы.

| Символ | Статус |
|--------|--------|
| `[ ]` | pending |
| `[>]` | in_progress |
| `[x]` | completed |
| `[!]` | failed |
| `[-]` | cancelled |

## Альтернатива - MCP-серверы

Чтобы добавить инструменты, не меняя исходный код агента (например, инструменты для конкретного
проекта, сторонние интеграции или инструменты на других языках), используйте
[интеграцию с MCP-серверами](../features/mcp.md).

Инструменты MCP регистрируются во время работы с префиксом `serverName__toolName` и подчиняются
той же модели `RequiresPermission` и списка разрешённых для плана, что и встроенные инструменты.

<!-- docsgen:source sha256=b807c273d10aaec3 -->
