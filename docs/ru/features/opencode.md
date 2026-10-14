# OpenCode

Провайдер `type: opencode` даёт Coddy модели шлюза [OpenCode](https://opencode.ai) - подписки **Go** или **Console** (Zen) с оплатой по мере использования - по одному API-ключу. OpenCode здесь только бэкенд: агент остаётся собственным агентом Coddy, со своим системным промптом, своими инструментами, своим шлюзом разрешений и своим циклом ReAct на каждом интерфейсе - в консоли, в веб-интерфейсе, в ACP-редакторах и в Telegram-шлюзе.

OpenCode держит подобранный каталог моделей для кодинга (Grok, GPT, Claude, Qwen, GLM, Kimi, DeepSeek, MiniMax, MiMo и остальные) и отдаёт каждую по тому протоколу обмена, который ей подходит. Coddy выбирает протокол для каждой модели, поэтому одна строка провайдера охватывает весь каталог.

## Получение ключа

1. Войдите в [консоль OpenCode](https://opencode.ai/console).
2. Оформите подписку **Go** (или **Go Plus**) либо добавьте кредиты для **Console** и укажите платёжные данные.
3. Скопируйте API-ключ.

## Использование ключа

Вставьте ключ в строку провайдера или позвольте команде login сохранить его за вас.

```yaml
# yaml-language-server: $schema=https://coddy.dev/config.schema.json
providers:
  - name: opencode
    type: opencode
    api_key: "${OPENCODE_API_KEY}"   # или литерал ключа, или пусто, чтобы читать OPENCODE_API_KEY
    # api_base: https://opencode.ai/inference/v1   # Console; без значения - Go по умолчанию
models:
  - model: opencode/glm-5.3
  - model: opencode/gpt-6-luna
  - model: opencode/claude-haiku-5-5
agent:
  model: opencode/glm-5.3
```

Пустой `api_key` читает переменную окружения `OPENCODE_API_KEY` (обычное правило `NAME_API_KEY`, так что строка с именем `opencode` читает `OPENCODE_API_KEY`), затем ключ, который сохранил login. Форма `${OPENCODE_API_KEY}` раскрывается при загрузке файла; просто пустое значение читается в момент вызова.

### Сохранить ключ через login

```bash
coddy providers login opencode
```

У OpenCode нет браузерного или device-потока - ключ копируется из веб-консоли, - поэтому login сводится к вставке. Он читает ключ из терминала (без эха) или из `--api-key`, сохраняет его в `$CODDY_HOME/providers/opencode/opencode-auth.json` с правами `0600` и, если не указан `--no-config`, записывает строку провайдера, модели шлюза и `agent.model` в `config.yaml`.

```text
$ coddy providers login opencode
Copy your API key from https://opencode.ai/console, then paste it here.
API key:
Stored the OpenCode key for https://opencode.ai/inference/go/v1 at /home/jane/.coddy/providers/opencode/opencode-auth.json
Updated /home/jane/.coddy/config.yaml: provider opencode, agent.model opencode/glm-5.3-flash, 39 models (opencode/glm-5.3-flash, opencode/kimi-k3, opencode/deepseek-v4-flash, ...)
```

Явный `api_key`, `api_key_command` или `OPENCODE_API_KEY` всё равно имеет приоритет над сохранённым входом. `coddy providers logout opencode` удаляет сохранённый файл; ключ остаётся действительным, пока вы не отзовёте его в консоли. `coddy providers list` показывает, какой источник использует строка, с маскированием ключа.

## Два шлюза

`api_base` выбирает шлюз; оба принимают ключ одного формата, но ключ выпускается для одного из них.

| Шлюз | `api_base` | Что это |
|---------|-----------|------------|
| **Go** (по умолчанию) | `https://opencode.ai/inference/go/v1` | месячная подписка Go / Go Plus |
| **Console** | `https://opencode.ai/inference/v1` | кредиты с оплатой по мере использования (Zen) |

Пустой `api_base` означает Go. Значение, которое не называет ни один шлюз, сводится к Go, так что ключ OpenCode никогда не уходит на произвольный хост. В веб-интерфейсе в разделе **Settings → LLM providers** строка показывает выбор шлюза вместо текстового поля базы. `CODDY_OPENCODE_BASE_URL` переводит весь процесс на другой адрес - для стенда или теста.

## Протокол обмена для каждой модели

Шлюз OpenCode отдаёт разные модели по разным протоколам, а его список `/models` не несёт подсказки, по какому именно, - только id. Coddy выбирает протокол по id модели.

| Протокол | Эндпоинт | Семейства моделей |
|----------|----------|----------------|
| OpenAI Responses | `/responses` | `grok-*`, `gpt-*`, `muse-*` |
| Anthropic | `/messages` | `claude-*`, `qwen*`, `minimax-*` |
| OpenAI-совместимый | `/chat/completions` | `glm-*`, `kimi-*`, `deepseek-*`, `longcat-*`, `mimo-*`, `hy*`, `space-bunny`, бесплатные модели и всё остальное |

Id, которого нет в этой таблице, сводится к `/chat/completions` - самой частой форме. Соответствие повторяет каталог из [документации OpenCode Go](https://opencode.ai/v2/docs/console/go/) и [документации Console](https://opencode.ai/docs/zen/); когда OpenCode добавляет семейство на новом протоколе, меняется карта в `internal/llm/opencode.go` (`opencodeProtocolFor`).

Уровни рассуждения работают как у любого провайдера: уровень, который разрешается для записи модели, отправляется как `reasoning.effort` в OpenAI Responses, как бюджет размышлений в Anthropic или как `reasoning_effort` в OpenAI-совместимом протоколе - по протоколу этой модели.

## Обязательные заголовки клиента

Шлюз отслеживает злоупотребления и просит каждый клиент представляться и держать диалог на одном маршруте. Coddy отправляет в каждом запросе к OpenCode - на всех трёх протоколах, для завершений и для списка моделей:

- `User-Agent: coddy/<version>` - собственное имя, а не типовое имя SDK или HTTP-библиотеки;
- `x-opencode-session: <session id>` - стабильный id диалога, чтобы OpenCode мог оптимизировать маршрутизацию и повторно использовать свой кэш промптов. Вызов, который не является диалогом (список моделей, проба `--dry-run`), отправляет сгенерированный id.

Оба заголовка добавляются поверх собственного транспорта строки, поэтому они никогда не открывают маршрут в обход `providers[].proxy`.

## Прокси

`providers[].proxy` маршрутизирует каждый запрос к OpenCode - завершения на всех трёх протоколах, список моделей и запрос каталога при входе - точно так же, как у любого другого провайдера: `inherit` следует прокси окружения, `none` соединяется напрямую, а URL идёт через этот прокси. См. [Прокси провайдера](../getting-started/configuration.md#прокси-провайдера).

## Лимиты использования

OpenCode учитывает расход каждой модели в рамках месячного долларового бюджета (с подлимитами на 5 часов и на неделю), и вы можете следить за ним в [консоли](https://opencode.ai/console). OpenCode не публикует API расхода, который клиент мог бы читать, поэтому Coddy не показывает панель расхода для строки `opencode`: переключатель `usage_limits_panel` нечем управлять, а окно контекста модели сводится к `max_context_tokens` или 128000, поскольку `/models` шлюза не сообщает его.

## Тестирование

- Модульные тесты: `internal/llm/opencode_test.go` фиксирует карту протоколов, разрешение шлюза и его переопределение через окружение, обязательные заголовки и маршрутизацию для каждой модели против `httptest`-стенда, который обслуживает все три протокола, а также цикл сохранения учётных данных и изоляцию носителя рассуждения от носителя Codex.
- Транспорт Responses общий с бэкендом Codex (`internal/llm/responses_stream.go`); его покрывает `internal/llm/codex_test.go`.

## См. также

- [Конфигурация](../getting-started/configuration.md) - провайдеры, модели и порядок разрешения учётных данных.
- [Переменные окружения](../reference/environment-variables.md) - `OPENCODE_API_KEY`, `CODDY_OPENCODE_BASE_URL`.
- [Справочник конфигурации](../reference/config.md) - поля `providers[]` и `models[]`.

<!-- docsgen:source sha256=21c0d6726a92f32d -->
