# Справочник по конфигурации

Эта страница - описательное руководство. Полный список ключей дают два сопутствующих документа:

- **[config-reference.md](../reference/config.md)** - таблицы по каждому полю с типом, значением по умолчанию, запасной переменной окружения, обязательностью и примерами;
- **[config.schema.json](../../../internal/config/config.schema.json)** - JSON Schema (draft-07) для автодополнения и проверки в редакторе. Она опубликована по адресу **https://coddy.dev/config.schema.json**, чтобы редактор находил её без клона репозитория, и встроена в бинарник, чтобы `coddy -t` проверял файл по тому же документу (см. [Проверка файла из командной строки](#проверка-файла-из-командной-строки)). Любой редактор с языковым сервером YAML (расширение YAML для VS Code, Zed, Neovim, Helix) проверяет ключи и значения прямо при вводе, если в файле есть такая строка заголовка.

```yaml
# yaml-language-server: $schema=https://coddy.dev/config.schema.json
```

**Coddy записывает эту строку сам.** Каждое сохранение, которое перезаписывает `config.yaml` (экран настроек через `PUT /coddy/config`, источник скилов, собственные `config_set` / `config_commit` агента), добавляет заголовок, если его нет, и не трогает `$schema`, который вы выбрали сами (закреплённый тег, локальный путь). Те же сохранения оставляют ваши комментарии, в том числе закомментированные ключи, текущий порядок ключей и то, как вы записали каждое значение, которое они не меняют (см. [Ссылки на переменные окружения](#ссылки-на-переменные-окружения)). Сохранение записывает ключи, которые уже есть в файле, и всё, что действительно отличается от встроенных значений по умолчанию; необязательные поля, которые никогда не задавались, в файл не попадают вовсе, а не записываются как `null`. Поэтому файл, где целые разделы закомментированы, таким и остаётся, а запись провайдера или модели сохраняет только те поля, которые в ней были. IDE от JetBrains заголовок не читают; если `config.yaml` там не проверяется, привяжите тот же URL вручную в **Settings - Languages & Frameworks - Schemas and DTDs - JSON Schema Mappings**. VS Code можно сообщить то же самое, не трогая файл.

```json
"yaml.schemas": { "https://coddy.dev/config.schema.json": ["**/.coddy/config.yaml"] }
```

Тест `TestDocsConfigSchemaMatchesStructs` (`internal/config/docs_schema_test.go`) следит, чтобы схема совпадала со структурами конфигурации на Go; CI падает, если поле конфигурации добавили или переименовали, не обновив схему. Копия, которую отдаёт `coddy.dev`, - точное зеркало этого файла, оно хранится в [репозитории сайта](https://github.com/coddy-project/coddy-project.github.io).

## Расположение файла конфигурации и пути

Расположения определяются переменными окружения и флагами (см. README). Коротко:

- **`CODDY_HOME`** - каталог состояния агента. По умолчанию **`~/.coddy`**. В нём лежат `config.yaml`, `sessions/`, `skills/`, учётные данные провайдеров под управлением Coddy в `providers/`, а при использовании необязательного cron-планировщика ещё и **`scheduler/`**;
- **`CODDY_CWD`** - рабочий каталог по умолчанию, когда `session/new` присылает пустой `cwd`. По умолчанию это рабочий каталог процесса при запуске. Если переменная задана, она значит то же, что флаг **`--cwd`**;
- **`CODDY_CONFIG`** - явный путь к `config.yaml`. То же, что **`--config`**;
- **`CODEX_HOME`** - каталог состояния Codex CLI, который читает провайдер **`type: codex`**, если нет учётных данных под управлением Coddy, причём только единственная строка codex или, если их несколько, строка с именем **`codex`** ([Несколько профилей одного типа провайдера](#несколько-профилей-одного-типа-провайдера)). По умолчанию **`~/.codex`**;
- **`CODDY_CODEX_BASE_URL`** - переопределение эндпоинта бэкенда Codex (по умолчанию **`https://chatgpt.com/backend-api/codex`**). Оно намеренно действует на уровне процесса - **`api_base`** для **`type: codex`** по-прежнему игнорируется, чтобы документ настроек не мог перенаправить OAuth-токен ChatGPT. Его используют исполняемые спецификации и самостоятельно развёрнутые шлюзы Codex;
- **`CODDY_DEVIN_CLI_CREDENTIALS`** - файл **`credentials.toml`** Devin CLI, который читают провайдеры **`type: devin`**, если собственного входа Coddy нет. По умолчанию **`~/.local/share/devin/credentials.toml`** (или внутри **`$XDG_DATA_HOME`**). **`CODDY_DEVIN_API_SERVER_URL`**, **`CODDY_DEVIN_WEBAPP_URL`** и **`CODDY_DEVIN_API_URL`** переносят эндпоинты Devin для всего процесса, для стендов и тестов; см. [Devin](../features/devin.md).

Если **`--config`** не указан, загрузчик берёт **`$CODDY_HOME/config.yaml`** (домашний каталог по умолчанию **`~/.coddy`**). Если этого файла нет, он пробует **`config.yaml`** в текущем рабочем каталоге процесса (**`$CWD`** при запуске). Если нет ни того ни другого, действуют встроенные значения по умолчанию (без ошибки).

Если основной файл существует, но некорректен (ошибка разбора YAML или проверки), загрузчик автоматически восстанавливает конфигурацию из **`config.yaml.bak`** в том же каталоге (см. **`internal/config/recovery.go`**). После каждой успешной загрузки сервер записывает **`config.yaml.bak`**. HTTP-маршрут **`PUT /coddy/config`** (см. **`docs/reference/http-api.md`**) тоже сохраняет текущий файл в **`config.yaml.bak`** перед перезаписью, чтобы неудачную перезагрузку можно было откатить.

В файле лежат ключи провайдеров и токены, поэтому Coddy записывает `config.yaml`, его резервные копии и `mcp.json` с правами только для владельца (`0600`), какими бы права ни были до этого. Файл, созданный вручную или старой версией, сохраняет свои права, пока Coddy его не перезапишет; `coddy -t` предупреждает, пока его могут читать другие пользователи машины, а `chmod 600 ~/.coddy/config.yaml ~/.coddy/config.yaml.bak` закрывает его сразу.

Подкоманда `coddy acp` принимает также **`--home`** (переопределяет `CODDY_HOME`), **`--sessions-dir`** и **`--session-id`**. Необязательный **`sessions.dir`** в YAML переопределяет корень сессий, если **`--sessions-dir`** не задан (по умолчанию **`$CODDY_HOME/sessions`**).

## Проверка файла из командной строки

Каждая команда, которая загружает `config.yaml`, принимает и `-t` (длинная форма `--test-config`), то есть `coddy -t` без подкоманды, `coddy cli -t`, `coddy acp -t` и `coddy serve -t`. Флаг проверяет тот файл, который загрузила бы эта команда (`--config PATH` и `--home DIR` выбирают его точно так же, как при запуске, а перед проверкой загружается `~/.coddy/.env` и раскрываются ссылки `${VAR}`), и завершается, ничего не запустив. Он ничего не записывает. Восстановление из `config.yaml.bak`, которое обычная загрузка выполняет для сломанного файла (см. выше), не запускается, поэтому отчёт относится именно к файлу на диске. Флаг работает в любой сборке, включая минимальную без тега `cli`.

Проверка идёт в два этапа. Сначала документ проверяется по описанной выше JSON Schema, встроенной в бинарник, - той же, которой пользуются редакторы. Именно этот этап ловит ошибки, которые загрузчик молча принимает. `config.yaml` декодируется нестрого, поэтому неизвестный ключ или ключ с опечаткой (`enabled` вместо `enable`) игнорируется, а не отвергается, а значение неверной формы, значение вне перечисления или диапазона, отсутствующий обязательный ключ или повторяющийся ключ проявляются позже как странное поведение. Затем к разобранному документу применяются собственные правила загрузчика, которые ловят, например, модель с несуществующим провайдером, вывод `file` без `logger.file` или `agent.model`, которого нет в `models`. Каждая проблема выводится в виде `file:line:column: what is wrong`, под ней с отступом идёт строка `fix:` с указанием, как её исправить, а если в схеме есть описание поля, то и строка `doc:` с этим описанием.

```text
$ coddy serve -t
/home/me/.coddy/config.yaml:13:3: httpserver.enabled: unknown key "enabled" (the loader ignores it, so it has no effect)
    fix: did you mean "enable"? keys allowed under httpserver: allow_insecure, auth_token, cors, enable, host, port, public_docs, remotes
    doc: Serve the HTTP API (and the embedded SPA) in this process. Omitted means true; set false on a node that only polls a messenger or relays a swarm.
/home/me/.coddy/config.yaml:16:10: logger.level: "verbose" is not an allowed value
    fix: use one of debug, info, warn, warning, error
    doc: Minimum severity written to the configured outputs ("warning" is accepted as an alias of "warn").
/home/me/.coddy/config.yaml:18:11: warning: subagents.enable: "yes" is read as the boolean true, but the schema and editors expect true or false
    fix: write enable: true (true or false, unquoted)
/home/me/.coddy/config.yaml: 2 errors, 1 warning
config test failed
```

Код выхода равен 1, если в файле есть ошибки, и 0 в остальных случаях, поэтому флаг удобно поставить в скрипт развёртывания прямо перед `coddy serve restart`. Предупреждения (с пометкой `warning:`) проверку никогда не проваливают. Они отмечают написания, которые загрузчик ещё читает, а схема и редакторы отвергают (`yes` для булева значения, `40.0` для целого), файл без заголовка `# yaml-language-server:`, файл, который могут читать другие пользователи машины (исправление - закрывающий его `chmod 600`), и настройку, которую провайдер никогда не отправляет. Например, `max_tokens` у модели, которую обслуживает провайдер `codex`, ничего не ограничивает, потому что бэкенд Codex не принимает предел вывода. Загрузчик такую настройку по-прежнему принимает, поскольку форма настроек проставляет `max_tokens` в каждую добавляемую строку модели, а `coddy serve` называет её в предупреждении при запуске. Отсутствующий файл считается ошибкой, ведь флаг существует для проверки того файла, который использовал бы запуск. Значения ключей, похожих на секреты (`api_key`, `auth_token`, `pairing_tokens`), в сообщениях никогда не выводятся.

Для файла, который вообще не разбирается, место ошибки определяется иначе, чем для файла с просто неверными значениями. Парсер сообщает строку, с которой начинался читаемый им блок, а в файле с шапкой из комментариев это пустая строка далеко выше ошибки. Поэтому проверка перечитывает файл, находит строку, на которой разбор останавливается, и сообщает её. Запуск выводит ту же строку, так что `coddy -t` и `coddy serve` отправляют вас в одно и то же место.

То, что оставляет в файле редактор, в конфигурацию не входит. В файле, записанном в Windows, строки заканчиваются возвратом каретки и переводом строки, а перед первой строкой может стоять метка порядка байтов. При чтении и то и другое отбрасывается, поэтому заголовок `# yaml-language-server:` после метки всё равно находится, сообщение о проблеме указывает ту строку, которую показывает редактор, а сохранение возвращает файлу его собственные окончания строк. Единственный формат, который Coddy не читает, - UTF-16 (то, что Блокнот называет "Unicode"). О нём сообщается именно так, с переходом на UTF-8 в качестве исправления, а не как о синтаксической ошибке.

## Пробный запуск - проверка того, на что указывает файл

`--dry-run` проверяет внешние ресурсы, которые описывает файл, после той же проверки, что выполняет `--test-config`. Его принимает каждая команда, которая принимает `-t`, то есть `coddy --dry-run`, `coddy cli --dry-run`, `coddy acp --dry-run` и `coddy serve --dry-run`, а `--config` и `--home` выбирают файл так же, как при запуске. Сначала выполняется статическая проверка, и на файле с ошибками всё останавливается - проверка того, что называет сломанный файл, только похоронила бы первую ошибку под её последствиями. Если файл чистый, конфигурация загружается без побочных эффектов (`config.yaml.bak` не записывается и не восстанавливается) и проверяется по пунктам:

- **память** - если `memory.additional_prompt` длиннее `memory.additional_prompt_max_chars`, у этого ключа выводится предупреждение, потому что субагент памяти читает обрезанный текст;
- **пути** - отсутствие `sessions.dir`, `logger.file`, `memory.dir` и папки заданий планировщика допустимо, если их можно создать (процесс создаёт их при запуске), и считается ошибкой, если на их месте лежит обычный файл; `prompts.dir` должен существовать, а отсутствие в нём шаблона даёт предупреждение; отсутствующие записи `skills.dirs`, `subagents.dirs` и `hooks.files`, которые вы написали сами, дают предупреждения, а отсутствующие пути по умолчанию проходят молча; существующий файл хуков должен разбираться; запись `instructions.files` даёт предупреждение, если её файл не существует, не читается, оказывается папкой или пуст, кроме относительной записи или записи с `${CWD}`, файла которой нет в рабочей папке, где запущена проверка, - такая запись пропускается (файл может быть в другой рабочей папке); `swarm.tls` должен загружаться, а каждый `dial.ca_file` должен содержать сертификат;
- **LLM-провайдеры** - у каждого провайдера запрашивается список моделей, и этот один запрос проверяет адрес, прокси и учётные данные (включая полученные через `coddy providers login`); о провайдере, который направлен на официальный эндпоинт вендора, но ничего не может предъявить, сообщается без запроса. Затем каждая запись `models[]` сверяется с этим списком. Модель, которую сервер не называет, даёт предупреждение, потому что некоторые серверы обслуживают больше моделей, чем перечисляют. `max_tokens` у модели `codex` даёт предупреждение независимо от ответа провайдера, потому что ни один запрос его не передаёт;
- **MCP-серверы** из `~/.coddy/mcp.json` - исполняемый файл stdio-сервера ищется в `PATH` так же, как при запуске, но сам сервер не запускается; удалённому серверу отправляется запрос с его заголовками, и годится любой HTTP-ответ. К описаниям из локального `.coddy/mcp.json` проекта проверка не обращается, потому что они стоят за проверкой доверия к рабочей папке;
- **Telegram** - если `gateways.telegram.enable` равен true, токен проверяется через Bot API (`getMe`), с прокси `gateways.telegram.proxy`, если он задан; в отчёте указывается имя бота;
- **удалённые серверы** - каждому URL из `httpserver.remotes[]` отправляется запрос (если он недоступен, это предупреждение, ведь он используется только по требованию), а цель `--remote` при запуске консоли или `acp` должна принять токен;
- **только `coddy serve`** - подсистемы, которые включают конфигурация и указанные флаги, определяются так же, как при запуске (интерфейс, без которого собран этот бинарник, даёт ошибку, а не пропускается молча), каждый адрес прослушивания один раз занимается и освобождается, так что порт, занятый другим процессом, называется вместе со строкой, которая его задала, а до релеев из `swarm.join` и вышестоящих узлов, которые монтирует релей, проверка дозванивается с их настройками подключения.

Сам по себе флаг немногословен. Он выводит проблемы (каждое `warning` и `error` с местом в файле и исправлением) и одну строку состояния в конце, поэтому исправная установка отвечает только этой строкой, и скрипту развёртывания достаточно прочитать её.

```text
$ coddy --dry-run
dry run: 0 errors, 0 warnings, 4 ok
```

Если что-то не так, сначала идут проблемы, а строка состояния по-прежнему завершает отчёт; код выхода равен 1, а последняя строка - `dry run failed`.

```text
$ coddy --dry-run
warning  skills.dirs[0]: /home/me/.coddy/skills does not exist
         at /home/me/.coddy/config.yaml:22:10
         fix: create it or remove the entry; a ${CWD} entry is resolved per session, so a folder missing here may exist in another workspace
warning  skills.dirs[1]: /opt/team-skills does not exist
         at /home/me/.coddy/config.yaml:22:34
         fix: create it or remove the entry; a ${CWD} entry is resolved per session, so a folder missing here may exist in another workspace
error    mcp.json[tickets]: command "ticket-mcp" not found in PATH
         fix: install it or write an absolute path as the command of tickets in /home/me/.coddy/mcp.json
warning  models[local/llama-4]: not in the model list of provider local (the server may still serve it)
         at /home/me/.coddy/config.yaml:11:5
         fix: check the model id; the provider lists gpt-oss-20b, qwen3.6-35b
error    providers[gpu]: cannot reach http://127.0.0.1:18732/v1: dial tcp 127.0.0.1:18732: connect: connection refused
         at /home/me/.coddy/config.yaml:6:5
         fix: check api_base and that the server is running
dry run: 2 errors, 3 warnings, 4 ok
dry run failed
```

Добавьте `--test-config`, чтобы увидеть всю картину. Сначала идёт отчёт проверки конфигурации (тот же, что выводит `-t`, вместе с `valid`), затем все проверки, включая успешные, так что отчёт показывает, что именно проверялось и по какому адресу.

```text
$ coddy --dry-run --test-config
/home/me/.coddy/config.yaml: valid
ok       sessions.dir: /home/me/.coddy/sessions will be created at first start
warning  skills.dirs[0]: /home/me/.coddy/skills does not exist
         at /home/me/.coddy/config.yaml:22:10
         fix: create it or remove the entry; a ${CWD} entry is resolved per session, so a folder missing here may exist in another workspace
warning  skills.dirs[1]: /opt/team-skills does not exist
         at /home/me/.coddy/config.yaml:22:34
         fix: create it or remove the entry; a ${CWD} entry is resolved per session, so a folder missing here may exist in another workspace
ok       mcp.json[context7]: command "npx" resolves to /usr/bin/npx
error    mcp.json[tickets]: command "ticket-mcp" not found in PATH
         fix: install it or write an absolute path as the command of tickets in /home/me/.coddy/mcp.json
ok       providers[local]: openai at http://127.0.0.1:18731/v1 lists 2 models
         at /home/me/.coddy/config.yaml:3:5
ok       models[local/qwen3.6-35b]: listed by provider local
warning  models[local/llama-4]: not in the model list of provider local (the server may still serve it)
         at /home/me/.coddy/config.yaml:11:5
         fix: check the model id; the provider lists gpt-oss-20b, qwen3.6-35b
error    providers[gpu]: cannot reach http://127.0.0.1:18732/v1: dial tcp 127.0.0.1:18732: connect: connection refused
         at /home/me/.coddy/config.yaml:6:5
         fix: check api_base and that the server is running
skipped  models[gpu/qwen3.6-35b]: provider gpu failed
dry run: 2 errors, 3 warnings, 4 ok
dry run failed
```

Строки `ok` и `skipped` исправлений не содержат; `warning` никогда не проваливает запуск, а `error` проваливает. Файл, не прошедший статическую проверку, показывается всегда, какие бы флаги ни были указаны, потому что больше ничего проверить нельзя, пока его не исправят. Сетевые проверки идут параллельно, и каждая ограничена десятью секундами, поэтому недоступный сервер стоит одного ожидания, а не ожидания на каждую модель. Секреты не выводятся - токен Telegram маскируется в любом тексте ошибки, а ключ провайдера не печатается никогда. `CODDY_TELEGRAM_API_BASE` направляет проверку Telegram, как и самого бота, на другой адрес Bot API, например на собственный сервер или офлайн-стенд [tgfake](../surfaces/gateway.md#отладка-на-поддельном-bot-api).

## Полная схема конфигурации

Имя агента, заголовок и версия сборки здесь не настраиваются. Они зашиты в бинарник и сообщаются во время ACP `initialize` (`internal/acp` и `internal/version`).

```yaml
# LLM backends (Go: []config.ProviderConfig, internal/config/providers.go)
# Each providers[].name must match ^[a-zA-Z][a-zA-Z0-9_-]*$ (ASCII letter first, then letters, digits, hyphen, underscore).
# api_key may be a literal, "${ENV}" expanded when the file loads, or empty to read NAME_API_KEY at LLM call time
# (NAME is the provider name in uppercase with hyphens mapped to underscores, for example rpa -> RPA_API_KEY).
# api_key_command (optional): when api_key is empty, this command is run via the detected host shell and its trimmed stdout is
# used as the key (credential helper, like git/docker helpers or AWS credential_process). It lets a provider fetch
# short-lived or login-issued keys without storing a static secret. On failure resolution falls back to NAME_API_KEY.
# Resolution order: literal api_key -> api_key_command stdout -> NAME_API_KEY env.
# A codex row reads none of the three and runs no helper: it signs in with ChatGPT (coddy providers login).
providers:
  - name: "openai"
    type: "openai"
    api_key: "${OPENAI_API_KEY}"
    # api_base: ""                    # optional override for OpenAI-compatible base URL
    # api_key_command: "my-cli print-token"  # host shell: pwsh/powershell/cmd on Windows; bash/sh elsewhere
    # proxy: none                     # route of this row: inherit (default), none, or a proxy URL
    # timeout_ms: 300000               # optional bound on each LLM request incl. streamed read (0 = no client timeout)

  - name: "anthropic"
    type: "anthropic"
    api_key: "${ANTHROPIC_API_KEY}"

  - name: "neuraldeep"
    type: "neuraldeep"
    api_key: "${NEURALDEEP_API_KEY}"

  # In the bundled web UI, select codex and use Sign In with ChatGPT. Tokens are
  # stored at $CODDY_HOME/providers/codex/codex-auth.json, not in config.yaml.
  - name: "codex"
    type: "codex"

  # `coddy providers login devin` signs in to a Devin account in the browser
  # (or reuses the Devin CLI login with --devin-cli) and adds this row and one
  # model per family. The session token lives under $CODDY_HOME/providers/devin/.
  - name: "devin"
    type: "devin"

  - name: "local"
    type: "openai"
    api_base: "http://localhost:11434/v1"
    api_key: "~"

  - name: "deepseek"
    type: "openai"
    api_base: "https://api.deepseek.com/v1"
    api_key: "${DEEPSEEK_API_KEY}"

# Logical models (Go: []config.ModelEntry, internal/config/models.go).
# Each model value is "provider_name/api_model_id". The first path segment must match providers[].name.
# The same string is the ACP model selector and agent.model default.
models:
  - model: "openai/gpt-5.6-terra"
    max_tokens: 8192
    reasoning_default: medium     # reasoning models take a level, not a temperature
    multimodal: true              # accepts images/files; UI shows file attachment button

  - model: "anthropic/claude-3-5-sonnet-20241022"
    max_tokens: 8192
    temperature: 0.2
    multimodal: true

  - model: "openai/gpt-5"
    max_tokens: 8192
    reasoning_default: medium     # level pre-selected for new chats (composer reasoning selector)
    # reasoning_levels: [low, high]  # optional override of offered levels; [] hides the selector
    # allow_reasoning_off: true       # optional: add Off only after verifying this deployment honours the disable-reasoning request

  - model: "local/qwen2.5-coder:14b"
    max_tokens: 4096
    temperature: 0.1

  - model: "deepseek/deepseek-coder-v2"
    max_tokens: 8192
    temperature: 0.1

  - model: "neuraldeep/default"
    max_tokens: 8192
    temperature: 0.2

  - model: "codex/gpt-5.6-sol"
    max_tokens: 8192

  - model: "devin/claude-sonnet-5"
    reasoning_levels: [low, medium, high, xhigh, max]  # each level is a variant of the family
    reasoning_default: medium

  - model: "local/qwen3.6-35b"
    max_tokens: 4096
    max_context_tokens: 49152     # a small window: offer only the tools a coding session calls
    tools: [read, write, edit, apply_patch, glob, grep, print_tree, keep_result, run_command,
            background_output, background_wait, background_stop, compact_context]
    # disallowed_tools: [ssh_run_command, http_request]  # removes names after tools; alone it keeps the rest

# ReAct loop settings (Go: config.Agent, internal/config/agent.go)
agent:
  model: "openai/gpt-5.6-terra"  # optional default LLM until the client overrides per session;
                               # unset, interactive surfaces pick a model per session, while
                               # coddy -p / coddy acp / API calls without a model report "no model configured"
  max_turns: 165               # ReAct iterations per prompt, recoveries included; 0 explicitly disables the limit
  llm_retry_max: 3             # shared per-step budget: transport retries + no-answer recoveries
                               # (default 3; 0 disables these retries, not separately configured continuations)
  llm_retry_base_ms: 1000      # initial backoff between LLM retries; a server-provided
                               # pause (Retry-After-Ms / Retry-After headers, "Limit resets
                               # at" / "retry in Ns" body phrases) overrides the backoff,
                               # capped at 60s
  llm_min_interval_ms: 0       # min gap between consecutive LLM calls, retries included; e.g. 12000 on strict free tiers
  llm_first_token_timeout_ms: 90000  # cancel a silent streamed LLM call after this long (0 disables the guard)
  llm_stream_idle_timeout_ms: 300000 # cut a streamed answer that sends nothing for this long after its first bytes,
                                     # keeping the text already delivered (0 disables the guard; blocking models are never guarded)
  wait_for_limit_reset: false        # wait for a hit usage limit to lift and re-issue the call (off: the turn ends with the error)
  wait_for_limit_reset_max_ms: 14400000  # total wait per turn (4 h), the retry wrapper's sleeps on a limit included; under 60 s it also bounds ordinary 429 retries; 0 never waits
  loop_guard: true             # stop a response that repeats itself, and a tool called over and over with identical args
  loop_tool_repeat_limit: 2    # identical calls in successive ReAct responses before the guard steps in (0 disables)
  loop_stream_repeat_cycles: 5 # identical output cycles in one stream before it is cut (0 disables)
  loop_nudge_max: 1            # nudges before the guard stops the turn with a notice

# System prompt templates
prompts:
  # Empty dir = use embedded defaults. Otherwise a directory containing the files named below.
  #
  # Whatever you put here, Coddy prepends its identity line ("You are Coddy, ...") unless the
  # template already opens with it — gateways attribute traffic by the start of the system
  # prompt. Source: internal/prompts/identity.go, rationale: docs/contributing/react-agent.md (Agent identity).
  #
  # Go text/template data. Fields in internal/prompts/loader.go. YAML shape is config.Prompts in internal/config/prompts.go.
  #   {{.CWD}}      - session working directory
  #   {{.Tools}}    - markdown list of tool names and short descriptions for the current mode
  #   {{.Skills}}   - markdown block for active skills (omit section when empty via {{if .Skills}})
  #   {{.Rules}}    - the AGENTS.md and DESIGN.md of the agent home and of the workspace, then the always-on rules
  #   {{.Instructions}} - the files of instructions.files. A template that prints neither block still gets the
  #                   documents and those files: in {{.Instructions}} when it has it, otherwise appended after it
  #   {{.TodoList}} - current session todo checklist as markdown lines (empty until coddy todo tools update state)
  #   {{.Memory}}   - session notes. The memory subagent's report is not rendered here: it travels in the <turn_context> block
  #   {{.UTCNow}}   - date and time in UTC (RFC3339), refreshed whenever the system prompt is rendered
  #
  # Built-in templates order: Tools, Skills, Memory (session notes).
  # They deliberately render neither {{.TodoList}} nor {{.UTCNow}}: both move between the steps of a
  # turn, and the system prompt is what the provider's prompt cache keys the whole conversation on.
  # Coddy sends the clock and the checklist after the history instead, in a <turn_context> block,
  # and a rule a tool call activates rides in that call's result. Your own template may still render them, at the cost of that cache.
  # See docs/contributing/react-agent.md (The turn context block).
  dir: ""
  agent_prompt: "agent.md"     # optional; default agent.md
  plan_prompt: "plan.md"       # optional; default plan.md
  ask_prompt: "ask.md"         # optional; default ask.md

# Session bundle storage (Go: config.Sessions, internal/config/sessions.go)
sessions:
  # Empty = default $CODDY_HOME/sessions. Supports ${CODDY_HOME} and ~ in path.
  dir: ""

# Context compaction (Go: config.Compaction, internal/config/compaction.go).
# Summarizes history older than the keep-recent boundary into one transcript row;
# later LLM prompts replay only the summary plus the kept tail. Trigger manually
# with the built-in /compact command (optional -m/--model <id> and -r/--reasoning <level>
# for that one summary, then optional trailing summarizer instructions)
# or automatically at threshold_percent of the model's context window
# (models[].max_context_tokens, else the window the provider reports, else 128000).
compaction:
  enable: true             # master switch (manual command and automation)
  auto_enable: true        # false disables only automatic compaction
  threshold_percent: 80    # auto-compact trigger, 1..100, a percent of the context window
  keep_recent_turns: 2     # last N user turns stay verbatim; 0 summarizes everything
  model: ""                # models[].model for the summarizer; empty = session model
  fallback_models: []      # tried in order when the summarizer above them fails; the session
                           # model is the last resort whether or not it is listed

# Optional long-term memory subagent (Go: config.MemoryConfig, internal/config/memory.go; logic in external/memory).
# Linked with the memory build tag; enable at runtime with memory.enable. Every user turn then starts a memory
# subagent in the background task pool (docs/features/memory.md).
memory:
  enable: false
  # Exact id from models[]. The memory subagent runs on it; the main assistant model is unaffected.
  # Example: "rpa/qwen3.6-35b-a3b". Empty means the session's model.
  model: ""
  dir: "" # long-term memory root; empty = $CODDY_HOME/memory. Supports ${CODDY_HOME} and ~ when set.
  wait_seconds: 20      # how long a turn waits for the report before its first model call; 0 never waits
  timeout_seconds: 300  # hard limit of one memory run
  keep_runs: 20         # finished memory runs kept per session in the Tasks panel; 0 keeps all
  recall_max_turns: 6   # the child's round cap is the larger of the two
  persist_max_turns: 12
  copilot_max_tokens: 4096
  max_search_hits: 8
  max_note_chars: 900   # longest body one saved note may have, in characters; 0 = no cap
  additional_prompt: ""          # your own instructions for the memory subagent only; the main agent never sees them
  additional_prompt_max_chars: 0 # cut additional_prompt at this many characters (a warning is logged); 0 = no cap

# Skills directories (Go: config.Skills, internal/config/skills.go)
skills:
  # Four folders are always read, lowest -> highest priority, whatever this
  # key says (a lower folder wins a skill name over the ones above it):
  #   ${HOME}/.agents/skills    - your skills shared with every agent (npx skills / npx skillsbd)
  #   .agents/skills            - the project's skills shared with every agent
  #   ${CODDY_HOME}/skills      - Coddy's own: the standard delivery, installed skills
  #   .coddy/skills             - the project's skills for Coddy
  # dirs only ADDS directories, read after the four and stronger than them.
  # ${HOME} and ~ are your home, ${CWD} and a relative path the session's workspace.
  dirs:
    - "~/my-team-skills"

# Rules (Go: config.Rules, internal/config/rules.go)
# One project folder is read under the session CWD: the first of .coddy/rules,
# the shared .agents/rules, .cursor/rules, .claude/rules and .codex/rules that
# holds a rule file, so another agent's mirror of the same rules is not loaded
# twice. Your own ~/.coddy/rules joins it in every workspace. The AGENTS.md and
# DESIGN.md documents - yours in ~/.coddy, the workspace's, and the nested ones
# of the folders a tool enters - are read whatever these keys say and have no
# key here. .mdc files are read as Cursor rules, .md files as Claude Code rules.
# The rules that always apply go into {{.Rules}} in the system prompt (separate
# from skills); a rule scoped to paths arrives with the tool result or message
# that touches a matching path. See docs/features/rules.md.
rules:
  auto_discover: true
  systems: []   # optional: user, coddy, agents-dir, cursor, claude, codex ("agents" no longer affects the AGENTS.md files; alone it loads no rule folder)

# MCP servers are not declared here: they live in ~/.coddy/mcp.json (every
# session) and <workspace>/.coddy/mcp.json (that project, once approved), a
# Cursor-compatible "mcpServers" object. An old mcp_servers list is moved into
# ~/.coddy/mcp.json on the next start. See docs/features/mcp.md.

# Tool configuration (Go: config.Tools, internal/config/tools.go)
tools:
  # Controls when the agent asks for user approval before running tools.
  # ask          - always prompt for commands and file writes (default)
  # accept_edits - auto-approve file writes; prompt for shell commands
  # bypass       - never ask for permission (use only in trusted environments)
  # Overridable per session (ACP session/set_config_option "permission_mode", /permissions,
  # the web composer chip); the override lives in memory, a restart comes back to this value.
  permission_mode: ask

  # TCP dial timeout for SSH connections in seconds (default: 30).
  # ssh_connect_timeout: 30

# Subagents (Go: config.Subagents, internal/config/subagents.go). Child agents the model spawns with spawn_agent
# from markdown definitions; each run is a background task with its own child session. See docs/features/subagents.md.
# subagents:
#   enable: true
#   dirs: []                      # extra folders after ~/.agents/agents, .agents/agents,
#                                 # ${CODDY_HOME}/agents and .coddy/agents, which are always read
#   project_trust: ask            # ask (approve project files once per workspace) | allow | deny
#   max_concurrent: 4             # subagent runs in flight across the whole process
#   max_depth: 1                  # 1 = children cannot spawn further; 0 = nobody spawns
#   default_timeout_seconds: 1800 # hard limit when the definition and the call give none
#   max_turns: 0                  # 0 follows agent.max_turns

# Hooks (Go: config.Hooks, internal/config/hooks.go). Your own commands at lifecycle points of a session,
# defined in JSON files of Claude Code's shape; project files need a one-time approval. See docs/features/hooks.md.
# hooks:
#   enable: true
#   files: ["${CODDY_HOME}/hooks.json", "${CWD}/.claude/settings.json", "${CWD}/.claude/settings.local.json", "${CWD}/.coddy/hooks.json"]
#   project_trust: ask            # ask (approve project files once per workspace) | allow | deny
#   default_timeout_seconds: 60   # per hook process when the definition gives no timeout
#   stop_loop_limit: 5            # Stop-hook continuations per turn
#   max_output_chars: 10000       # cap on what one hook hands to the model or the user

# HTTP OpenAI gateway (only with go build -tags=http). Embedded SPA on / needs -tags=http,ui too. See docs/reference/http-api.md
# httpserver:
#   host: "127.0.0.1"
#   port: 8080

# Cron scheduler (only with go build -tags=scheduler). UTC crontab; flat *.md jobs in ${CODDY_HOME}/scheduler
# and, once approved, in <workspace>/.coddy/scheduler.
# scheduler:
#   enable: false
#   project_trust: ask
#   max_queue: 10
#   timeout: "30m"
#   retain_sessions: 5  # max completed run session dirs kept per job_id (default 5)

# Logging (Go: config.Logger, internal/config/logger.go)
logger:
  level: "info"           # debug | info | warn | error
  # Raise or lower one subsystem on its own. A component is the dotted name a
  # record carries in its "component" field (gateway, gateway.telegram,
  # session, agent, scheduler); a parent name covers what is nested under it
  # and the longest match wins. Omitted = every record follows level above.
  levels:
    - component: "gateway.telegram"
      level: "debug"
  # Where records go: any combination of stdout, stderr, file. Omitted or empty = stderr only.
  outputs: []
  # Path for the file sink; required when outputs includes file.
  file: ""
  # text (default) or json
  format: "text"
  rotation:
    max_size_mb: 0        # 0 = no size-based rotation
    max_files: 0          # rotated backups to keep when max_size_mb > 0
```

Флаги ACP, если они заданы, переопределяют те же настройки - это **`--log-level`**, **`--log-output`** (stdout, stderr, file, both), **`--log-file`** и **`--log-format`**. Пустые значения флагов оставляют значения из YAML (или встроенные). **`--log-level`** принимает и спецификацию по компонентам в виде списка через запятую (**`--log-level "info,gateway.telegram=debug"`**); голый уровень не трогает настроенные записи **`levels`**, а спецификация с компонентами заменяет их.

Если в старом двухпольном формате под **`logger`** задан **`file`**, но нет **`outputs`**, загрузчик разворачивает это в **`stderr`** плюс **`file`**, чтобы запись в файл заработала.

## Удалённое выполнение по SSH

Через встроенный инструмент `ssh_run_command` агент выполняет команды на удалённых хостах по SSH, внешний бинарник `ssh` для этого не нужен (реализация на чистом Go через `golang.org/x/crypto/ssh`). Единственная настройка - `tools.ssh_connect_timeout` (таймаут TCP-подключения, по умолчанию 30 с).

**Порядок аутентификации:**
1. **SSH-агент** - если `SSH_AUTH_SOCK` задан и доступен, первым используется агент. Это покрывает YubiKey, SSH-агент 1Password, gpg-agent и обычный `ssh-agent`;
2. **Файлы ключей** - Coddy всегда смотрит в каталог `~/.ssh` текущего пользователя ОС. Имена ключей пробуются по порядку - `id_ed25519`, `id_rsa`, `id_ecdsa`, `id_dsa`. Ключи, защищённые парольной фразой, молча пропускаются.

Оба источника работают одновременно - если агент доступен и в нём есть ключи, файлы всё равно служат запасным вариантом на случай, если агент откажет.

**Проверка ключа хоста** - определяется автоматически по `tools.permission_mode`:
- любой режим, кроме `bypass` **(по умолчанию)**, - новые хосты автоматически добавляются в `~/.ssh/known_hosts` при первом подключении (TOFU); если ключ известного хоста изменился, старая запись заменяется новой;
- `bypass` - проверка ключа хоста отключена (подходит для временных ВМ и окружений CI).

**Схема инструмента** - `ssh_run_command` принимает такие поля.
| Поле | Тип | Обязательное | Описание |
|---|---|---|---|
| `host` | string | да | `user@hostname`, указывать пользователя обязательно |
| `command` | string | да | Команда оболочки, которую нужно выполнить на удалённом хосте |
| `port` | integer | нет | Порт SSH (по умолчанию 22) |
| `timeout_seconds` | integer | нет | Таймаут команды в секундах |
| `permission_rationale` | string | нет | Текст, который показывается в диалоге разрешения |

Инструмент требует разрешения пользователя (как и `run_command`) и возвращает объединённые stdout и stderr.

## HTTP-шлюз (необязательная сборка)

Ключ **`httpserver`** (`config.HTTPServerConfig` в `internal/config/http.go`) игнорируется, если бинарник собран без **`-tags http`**. Он задаёт **`host`** и **`port`** по умолчанию, когда у **`coddy serve`** флаги остались со встроенными значениями (`0.0.0.0` и `12345`). См. **`docs/reference/http-api.md`**.

### Вход в веб-интерфейс (`httpserver.login`)

По умолчанию выключен. Он закрывает браузерный интерфейс сервера, доступного по сети, чтобы посторонний, нашедший порт, не мог прочитать транскрипты, вывод инструментов и редактор конфигурации. Учётную запись записывайте командой, а не вручную.

```bash
coddy serve set-password --user pasha
```

```yaml
httpserver:
  host: 0.0.0.0
  auth_token: "${CODDY_HTTP_TOKEN}"   # unchanged: the credential API clients present
  login:
    enable: true                       # omit to follow the credentials; false wins over everything
    user: "pasha"
    password_hash: "$$argon2id$$v=19$$..."   # argon2id, written by the command above
    session_ttl_hours: 720                   # 0 = the browser drops the cookie on close (the server still expires its record after 30 days)
```

В хеше, записанном в этот файл вручную, каждый `$` нужно удвоить (`$$argon2id$$v=19$$...`), потому что `$NAME` при загрузке файла раскрывается как ссылка на окружение. Команда делает это за вас; `coddy -t` сообщает о проблеме, если находит хеш, который больше не разбирается. Ссылка `${VAR}` в `user` или `password_hash` работает так же, как любое другое значение здесь, и после сохранения с экрана настроек остаётся ссылкой; чтобы учётные данные вообще не попадали в документ, используйте вместо этого `CODDY_HTTP_USER` / `CODDY_HTTP_PASSWORD`.

Учётную запись можно задать и только через окружение (`CODDY_HTTP_USER` и `CODDY_HTTP_PASSWORD`, см. раздел про `.env` ниже) - это путь для контейнера или systemd-юнита. Форма предназначена для браузеров; `coddy --remote`, `coddy acp --remote`, релей роя и любые скрипты по-прежнему предъявляют bearer-токен. Подробное описание поведения - на страницах [HTTP API](../reference/http-api.md#вход-в-веб-интерфейс-необязательно) и [Удалённый режим](../operate/remote.md#форма-входа).

## Планировщик (необязательная сборка)

### Доверие к MCP-серверам проекта

Ключ **`mcp.project_trust`** решает, может ли локальный для проекта **`<workspace>/.coddy/mcp.json`** запускать свои серверы. При **`ask`** (по умолчанию) они ждут, пока оператор одобрит каждое описание для этой рабочей папки, **`allow`** запускает их автоматически, **`deny`** не загружает никогда. Чтобы переопределить ключ для одного процесса, передайте **`coddy acp --mcp-project-trust <value>`** или **`coddy serve --mcp-project-trust <value>`**; так делают джобы CI и точки входа контейнеров вместо правки файла конфигурации. Неизвестное значение проваливает запуск. Добавлено в [issue #80](https://github.com/coddy-project/coddy-agent/issues/80); полное руководство - в [docs/features/mcp.md](../features/mcp.md).

Ключ **`scheduler`** (`config.SchedulerConfig` в `internal/config/scheduler.go`) используется, только если сборка сделана с **`-tags scheduler`**. Задайте **`scheduler.enable: true`** в YAML или передайте **`coddy acp -scheduler`** / **`coddy serve -scheduler`**, чтобы включить **`scheduler.enable`** для этого процесса без правки файла конфигурации.

Задания хранятся как плоские файлы **`*.md`** в двух фиксированных папках - ваши собственные в **`${CODDY_HOME}/scheduler`** и задания проекта, которые лежат в репозитории, в **`<workspace>/.coddy/scheduler`**; последние запускаются, только когда им доверяют по **`scheduler.project_trust`** (по умолчанию `ask`; задание проекта, созданное через Coddy, одобряется сразу, см. [Планировщик](../operate/scheduler.md#задания-проекта-и-доверие)). Старый ключ **`scheduler.dir`** больше не читается - при следующем запуске его задания копируются в **`${CODDY_HOME}/scheduler`**, а сам ключ удаляется. В каждом файле есть YAML-заголовок с полями **`description`**, **`schedule`** (пять полей cron, **UTC**), необязательным **`cwd`** (по умолчанию каталог, где был запущен **`coddy`**, а для задания проекта его рабочая папка, и cwd такого задания должен оставаться внутри неё), **`model`**, **`mode`** (`agent`, `plan` или `ask`), необязательным **`agent`** (описание субагента, от имени которого идёт запуск), необязательным **`permission_mode`** (`ask`, `accept_edits` или `bypass`; пустое значение означает `bypass`, режим по умолчанию для работы без присмотра) и необязательным **`paused`** (если true, запуски по cron и вручную пропускаются до возобновления). Тело в markdown - разовая инструкция для запуска, который выполняется как фоновая задача агента в собственной сессии задания ([Планировщик](../operate/scheduler.md)). Один служебный файл, **`basename.state`** (последний сработавший слот и id сессии задания), лежит рядом с **`basename.md`** для пользовательского задания и в **`${CODDY_HOME}/scheduler/.projects/`** для задания проекта.

**`retain_sessions`** (по умолчанию **5**) ограничивает, сколько **завершённых** запусков хранится для каждого **`job_id`** (их записи задач и транскрипты в сессии задания); более старые запуски удаляются, когда завершается очередной. **`max_queue`** ограничивает число одновременно идущих запусков по всем заданиям, а **`timeout`** задаёт жёсткий предел одного запуска (пул фоновых задач всё равно ограничивает его значением **`tools.background.max_timeout_seconds`**).

Когда планировщик фактически включён, инструменты **`coddy_scheduler_*`** покрывают просмотр списка и одного задания, создание, замену и частичное изменение, удаление, паузу и возобновление, ручной запуск, отмену и список метаданных запусков (**`coddy_scheduler_jobs_list`**, **`coddy_scheduler_job_get`**, **`coddy_scheduler_job_create`**, **`coddy_scheduler_job_replace`**, **`coddy_scheduler_job_patch`**, **`coddy_scheduler_job_delete`**, **`coddy_scheduler_job_pause`**, **`coddy_scheduler_job_resume`**, **`coddy_scheduler_job_run`**, **`coddy_scheduler_job_cancel`**, **`coddy_scheduler_job_runs`**). Со сборкой **`-tags=http,scheduler`** те же операции доступны через REST в **`/coddy/scheduler`** (см. **`docs/reference/http-api.md`**).

## Шлюз мессенджеров (`gateways`)

Нужен бинарник, собранный с **`-tags gateway.telegram`** (только Telegram) или **`-tags gateway`** (все адаптеры). Этот блок читает подкоманда `coddy serve`.

```yaml
# Messenger gateways (external/gateway/; build with -tags gateway.telegram or -tags gateway).
# Full guide: docs/surfaces/gateway.md
gateways:
  telegram:
    # Set to true to activate the Telegram adapter when coddy serve starts.
    enable: false

    # Bot token from @BotFather. Never hard-code; always use an env reference.
    token: "${TELEGRAM_BOT_TOKEN}"

    # How the bot reaches the Bot API: inherit (the default) follows HTTPS_PROXY,
    # none connects directly, or a proxy URL (http, https, socks5, socks5h).
    # proxy: "socks5h://127.0.0.1:1080"

    # Telegram user IDs with admin privileges.
    # Admins bypass every access check and can always interact with the bot.
    admins: []
    # Example:
    # admins: [98874093]

    # Default access level for chats without a per-chat override.
    #   "all"          - anyone who can write to the chat
    #   "admins"       - only user IDs listed in admins
    #   "group:<name>" - only users in the named user_groups entry (admins always pass)
    default_access: "all"

    # Default session isolation mode for group chats without a per-chat override.
    #   "individual"   - each group member gets their own session
    #   "shared"       - all members share one session
    #   "admin"        - only admins can interact; all admins share one session
    default_isolation: "individual"

    # Named sets of user IDs for group-based access control.
    user_groups: []
    # Example:
    # user_groups:
    #   - name: "devs"
    #     user_ids: [111222333, 444555666]

    # Per-chat overrides. chat_id is negative for groups and supergroups.
    chats: []
    # Example:
    # chats:
    #   - chat_id: -1001234567890
    #     isolation: "individual"
    #     access: "all"
    #   - chat_id: -1009876543210
    #     isolation: "admin"
    #     access: "admins"
```

`token` проверяется при запуске, если `enable: true`. `proxy` необязателен и читается так же, как у провайдера - пустое значение или `inherit` следует прокси из окружения (`HTTPS_PROXY`, `HTTP_PROXY`, `NO_PROXY`), `none` подключается напрямую, URL ведёт через этот прокси ([Прокси провайдера](#прокси-провайдера)). Для остальных полей, если они опущены, действуют значения по умолчанию `default_access: "all"` и `default_isolation: "individual"`.

Полное руководство по настройке, инструкции по запуску и описание того, как добавить адаптеры для других мессенджеров, - в **[docs/surfaces/gateway.md](../surfaces/gateway.md)**.

## Файл `.env`

Если **`$CODDY_HOME/.env`** существует, он читается при запуске **до** разбора `config.yaml`. Так все секреты хранятся в одном месте, и не нужно трогать профили оболочки или блоки environment в Docker compose.

```sh
# ~/.coddy/.env
OPENAI_API_KEY=sk-...
ANTHROPIC_API_KEY=sk-ant-...
TELEGRAM_BOT_TOKEN=8992982910:AAF...
CODDY_HTTP_TOKEN=a-long-random-token       # bearer credential for API clients
CODDY_HTTP_USER=pasha                      # web UI sign-in account...
CODDY_HTTP_PASSWORD=correct-horse-battery-staple   # ...enables the form on its own
```

`CODDY_HTTP_USER` и `CODDY_HTTP_PASSWORD` - единственная пара, на которую `config.yaml` вообще не ссылается. Пароль хешируется при запуске сервера, ни одно из значений в файл не попадает, и сохранение с экрана настроек не может их туда записать. Они перекрывают учётную запись из файла, а `httpserver.login.enable: false` выключает форму, даже если они заданы.

Затем сошлитесь на них в `config.yaml` как обычно.

```yaml
providers:
  - name: openai
    type: openai
    api_key: "${OPENAI_API_KEY}"
```

**Правила:**

- переменные, которые **уже заданы** в окружении процесса, **никогда не переопределяются** - окружение процесса всегда важнее. `.env` служит только запасным источником;
- отсутствующий `.env` молча игнорируется (это не ошибка);
- файл ищется относительно действующего `CODDY_HOME` (по умолчанию `~/.coddy` или путь из `--home` / переменной окружения `CODDY_HOME`).

**Поддерживаемый синтаксис.**

| Форма строки | Пример |
|-----------|---------|
| `KEY=value` | `OPENAI_API_KEY=sk-abc` |
| `export KEY=value` | `export DEBUG=true` |
| Значение в двойных кавычках | `MSG="hello world"` |
| Значение в одинарных кавычках | `PATH='no escape \n here'` |
| Escape-последовательности в `"…"` | `NOTE="line1\nline2"` → настоящий перевод строки |
| Комментарий в конце строки (без кавычек) | `KEY=val # this is ignored` |
| Строка комментария | `# full-line comment` |

Значения, которые уже есть в окружении процесса (например, заданные оболочкой, Docker или systemd), имеют приоритет, и `.env` их никогда не меняет.

## Ссылки на переменные окружения

Любое значение конфигурации может ссылаться на переменные окружения через синтаксис `${VAR_NAME}`.
Агент подставляет их при запуске.

**Буквальный `$` в значении.** Раскрытие выполняется над исходным текстом файла, поэтому в значении,
которое должно содержать буквальный знак доллара (например, секрет прокси или API-ключа вида `$2y$10$…`),
его нужно удвоить, то есть написать `$$`; `$$` раскрывается обратно в один `$`, точно как в docker-compose / envsubst.
Иначе фрагменты вроде `$2y` или `$10` считаются ссылками на переменные окружения и превращаются в
пустые строки, молча портя секрет. Экран настроек делает это автоматически для полей `proxy`
(`providers[].proxy` и `gateways.telegram.proxy`), которые всегда считаются буквальными URL и ссылки
`${VAR}` **не** поддерживают; для буквального `$` в `api_key` (который `${VAR}` поддерживает) пишите
`$$` вручную.

**Сохранение оставляет ссылки.** Загруженная конфигурация содержит то, во что раскрылась ссылка,
поэтому экран настроек работает с самим секретом и с абсолютными путями. При сохранении значение,
записанное в файле как `${VAR}`, `${CODDY_HOME}/...` или `~/...`, записывается обратно в том же виде,
если при загрузке оно по-прежнему даёт сохраняемое значение, причём как в одиночном значении (`memory.dir`),
так и в элементе списка (`skills.dirs`, `subagents.dirs`, `hooks.files`, `instructions.files`);
ссылку заменяет только значение, которое вы изменили на экране. Поэтому ключ, который хранится в
окружении или в `~/.coddy/.env`, никогда не попадёт в `config.yaml` из-за постороннего сохранения, а
сохранение нетронутой формы записывает каждое значение обратно так, как оно было написано в файле.
То же относится к тому, что процесс меняет после чтения файла. Флаг командной строки, адрес
прослушивания релея, который подставляет `coddy serve`, или токен присоединения из окружения не
записываются в `config.yaml`, если вы не измените это значение на экране, а значение, которое другое
сохранение изменило после того, как вы открыли форму, ваше сохранение назад не вернёт. Если в момент
сохранения файл на диске не загружается (сломанная ручная правка, удалённый файл), сохранение
записывает конфигурацию, с которой работает сервер, как и раньше. Отступы и пустые строки не
сохраняются - сохранение записывает файл с отступом в два пробела и без пустых строк между разделами.

Два заполнителя не относятся к переменным окружения:

- **`${CODDY_HOME}`** - итоговый каталог `CODDY_HOME`, подставляется при чтении файла;
- **`${CWD}`** - рабочий каталог **сессии**. При чтении файла он **не** подставляется, а остаётся в загруженном значении, и его раскрывает для запросившей сессии тот, кто использует путь, - загрузка скилов, поиск субагентов и хуков, шаблоны промптов (**`prompts.dir`**), команда, аргументы, URL, окружение и заголовки MCP-сервера. Поэтому один процесс **`coddy serve`** обслуживает много рабочих папок, и сессия, открытая в проекте, видит **`${CWD}/.coddy/skills`** этого проекта (или любую запись, которую вы напишете, например **`${CWD}/.agents/skills`**), из какого бы каталога ни был запущен сервер. Только пути уровня процесса (**`sessions.dir`**, **`scheduler.dir`**, **`memory.dir`**, **`logger.file`**) раскрывают **`${CWD}`** при загрузке относительно рабочего каталога по умолчанию (**`CODDY_CWD`**), поскольку ни одной сессии они не принадлежат.

Переменная окружения с именем **`CWD`** заполнитель не заменяет (голый **`$CWD`** без фигурных скобок, как и раньше, остаётся обычной ссылкой на окружение), а **`GET /coddy/config`**, экран настроек и **`config_get`** показывают запись ровно так, как она написана. Заполнитель учитывается только в перечисленных выше полях; в любом другом строковом значении он остаётся как есть (шаблоны промптов вместо него используют **`{{.CWD}}`**). Релизы вплоть до 1.0.5 при чтении файла подставляли вместо **`${CWD}`** каталог процесса, поэтому сохранение настроек в такой версии могло записать абсолютный путь вроде **`/home/you/.agents/skills`** там, где вы написали **`${CWD}/.agents/skills`**; верните заполнитель вручную, чтобы путь снова вычислялся для каждой сессии.

## Справочник по провайдерам моделей

Значения **`type`** провайдера соответствуют **`internal/llm.NewProvider`**, это **`openai`**, **`anthropic`**, **`neuraldeep`**, **`codex`** и **`devin`**.

Разделение в YAML:

- **`providers`** - **`name`** (уникальное), **`type`**, **`api_key`**, необязательный **`api_base`** (переопределение базового URL для SDK провайдера - OpenAI-совместимый эндпоинт или хост Ollama без **`/v1`** для **`type: openai`** либо Anthropic-совместимый шлюз или релей для **`type: anthropic`**; для **`type: neuraldeep`** он выбирает развёртывание, **`https://api.neuraldeep.ru/v1`** или **`https://api.neuraldeep.tech/v1`**, а любое другое значение сводится к первому), необязательный **`proxy`** (маршрут всех запросов строки - по умолчанию **`inherit`**, **`none`** для прямого подключения или URL прокси **`http://`**, **`https://`**, **`socks5://`** или **`socks5h://`**; см. [Прокси провайдера](#прокси-провайдера)), необязательный **`usage_limits_panel`** (булево значение, по умолчанию **`true`**; **`false`** скрывает панель лимитов аккаунта этой строки во всех интерфейсах и прекращает чтение расхода лимитов для неё, сейчас это имеет смысл для **`type: neuraldeep`**, **`type: codex`** и **`type: devin`**);
- **`models`** - **`model`** (строка **`provider_name/api_model_id`**, селектор сессии и значение **`agent.model`**; первый сегмент называет **`providers[].name`**, остаток - id модели в API), **`max_tokens`**, **`temperature`**, необязательный **`max_context_tokens`** (окно контекста модели, относительно которого считают кольцо контекста в веб-интерфейсе, процент контекста в консоли и автоматическое сжатие контекста; 0 берёт значение из списка моделей провайдера, если провайдер его сообщает, иначе 128000 - см. [Сжатие контекста](../features/compaction.md#окно-контекста)), необязательный **`multimodal`** (булево значение, по умолчанию **`false`**; **`true`** означает, что модель принимает изображения и файлы, - интерфейс показывает кнопку прикрепления файла в поле ввода только для этой модели, а [`read`](../reference/tools.md#файлы) показывает такой модели картинку из файла изображения, а не отказывает, см. [Изображения](../features/images.md)), необязательный **`reasoning_levels`** (список строк; переопределяет уровни рассуждения, предлагаемые для этой модели, - если его нет, они определяются автоматически по id модели в API, для **`gpt-5*`** и **`gpt-6*`** это **`minimal,low,medium,high`**, а для серии OpenAI **`o`**, **`gpt-oss*`**, **`qwen3*`** (qwen3, qwen3.5, qwen3.6, qwen3.8, ...) и моделей Claude с расширенным мышлением - **`low,medium,high`**; явный пустой список скрывает выбор уровня рассуждения в поле ввода), необязательный **`reasoning_default`** (уровень, выбранный заранее для новых чатов; должен быть одним из итоговых уровней), необязательный **`allow_reasoning_off`** (булево значение, по умолчанию **`false`**; добавляет **`off`** к вариантам этой модели, только если оператор проверил, что развёртывание провайдера и модели выполняет специфичный для провайдера запрос Coddy на отключение рассуждения) и необязательные **`tools`** и **`disallowed_tools`** (списки строк, которые сужают набор инструментов, предлагаемых, пока сессия работает на этой модели, - для локальной модели с небольшим окном; см. [Сужение набора инструментов модели](#сужение-набора-инструментов-модели)). Уровни рассуждения переводятся в **`reasoning_effort`** OpenAI и **`budget_tokens`** расширенного мышления Anthropic; для моделей **`qwen3*`** у OpenAI-совместимых провайдеров запрос также несёт **`chat_template_kwargs`** **`{"enable_thinking": true}`**, чтобы переключатель мышления в шаблоне чата оставался включённым. Бэкенд Codex отвергает **`max_output_tokens`**, поэтому для провайдеров **`codex`** **`max_tokens`** не отправляется; он отвергает и уровень **`minimal`**, который обычно подразумевают id **`gpt-5*`** и **`gpt-6*`**, поэтому модели на Codex предлагают вместо него **`none`** (в выборе уровня в поле ввода и в **`GET /v1/models`**). Ходы с рассуждением запрашивают сводки (**`summary: auto`**), чтобы рассуждение передавалось потоком, и зашифрованное рассуждение (**`include: reasoning.encrypted_content`**), чтобы цепочка рассуждений воспроизводилась между вызовами инструментов так же, как это делает Codex CLI. О сроке жизни токенов и отчёте об учётных данных при запуске см. [config-reference.md](../reference/config.md).

### Сужение набора инструментов модели

Каждый запрос несёт схему каждого инструмента, который предлагает сессия. Сессия в режиме "Агент" предлагает около 44 инструментов: их массив `tools` занимает около 11 700 токенов, а список инструментов, который печатает системный промпт, - ещё 4 600 (оценка счётчика контекста, её измеряет `make test-perf`). Модель с большим окном этого не замечает. Локальная модель с окном 49k тратит на это треть окна до первого сообщения, и всё на инструменты, которые сессия по работе с кодом никогда не вызывает.

`tools` и `disallowed_tools` в строке `models[]` сужают набор только для этой модели:

```yaml
models:
  - model: "local/qwen3.6-35b"
    max_context_tokens: 49152
    tools: [read, write, edit, apply_patch, glob, grep, print_tree, keep_result, run_command,
            background_output, background_wait, background_stop, compact_context]
  - model: "codex/gpt-5.6-sol"          # no lists: every tool, as before
  - model: "openai/gpt-5"
    disallowed_tools: [ssh_run_command, http_request]   # every tool except these two
```

Тринадцать инструментов первой строки уменьшают массив `tools` запроса с 44 инструментов и около 11 700 токенов до 13 инструментов и около 3 800, а список инструментов в системном промпте - с 4 600 токенов до 1 400: примерно на 11 000 токенов меньше в каждом запросе. Две другие строки отправляют то же, что и раньше.

- **Синтаксис.** `tools` - список разрешённых, `disallowed_tools` - список запрещённых, он применяется после первого; имя из обоих списков убирается. Запись - это точное имя инструмента из [справочника инструментов](../reference/tools.md), одиночная `*` или шаблон `prefix*`. Инструменты MCP называются `server__tool`, поэтому `context7__*` пропускает все инструменты одного сервера, а список разрешённых без шаблона MCP не предлагает ни одного инструмента MCP. Сопоставление то же, каким [определение субагента](../features/subagents.md#файлы-определений) читает собственные `tools` и `disallowed_tools`;
- **Пустое значение - это всё.** Отсутствующий ключ, пустой список и список из пустых строк предлагают все инструменты, которые предложил бы режим. Чтобы не предлагать ни одного, задайте `disallowed_tools: ["*"]`: запрос уйдёт совсем без инструментов, а ход станет обычным чатом;
- **Только сужение.** Инструмент, который уже скрыт режимом (у "Плана" и "Чата" фиксированные наборы), конфигурацией (`tools.background.enable: false`, `subagents.enable: false`) или определением субагента, остаётся скрытым, что бы ни назвал список. Список ничего не добавляет и ничего не навязывает: циклу инструменты не нужны, а сжатие контекста, `/compact` и проверка цели не являются вызовами инструментов;
- **Список следует за моделью.** Запрос получает инструменты той модели, для которой он строится. Переключение (`/model`, селектор модели, `switch_model`) меняет набор со следующего запроса, в том числе посреди хода, и системный промпт собирается заново вместе с ним, если из-за списков набор отличается; переключение между двумя моделями без списков ничего в запросе не меняет. Если провайдер новой модели создать не удалось, ход продолжается на прежней модели с её набором. Для одной модели набор и промпт одинаковы в каждом запросе, поэтому кеш промпта у провайдера продолжает работать;
- **Вызовы проверяются повторно.** Вызов проверяется по набору того запроса, который его породил. Вызов инструмента, которого этот запрос не предлагал, - воспроизведённый из истории, написанной на другой модели, или выдуманный, - получает ответ `error: tool "write" is not available on model local/qwen3.6-35b ...`, отображается как отменённый и не доходит ни до хука, ни до запроса разрешения. `switch_model` посреди пачки вызовов меняет набор со следующего запроса: вызовы после него в той же пачке выполняются по набору, который им предлагали. Запрос разрешения, который ждал ответа в момент смены модели, проверяется по модели, на которой сессия работает теперь, отклоняется так же, если она не предлагает инструмент, и не запоминает ответ "всегда разрешать";
- **Промпт следует за набором.** Список инструментов в системном промпте содержит те же инструменты, что и запрос. Список субагентов убирается, если `spawn_agent` не предлагается, а подсказка вызвать `load_skill` - если не предлагается этот инструмент. Инструкции встроенного промпта по-прежнему называют некоторые инструменты по имени; модель, которой инструмент не предлагают, эти строки игнорирует;
- **Субагенты.** Дочерний агент работает на одной модели - родителя, из `model` определения или названной в вызове - и получает свой действующий набор ([пересечение](../features/subagents.md#как-сужаются-возможности)), суженный списками этой модели. Списки модели родителя ограничивают то, что видит родитель, а не то, что предлагают дочернему агенту на другой модели, поэтому небольшой локальный оркестратор может передать работу дочернему агенту на модели без списков. Запуск, при котором модель не оставляет дочернему агенту ни одного инструмента, отклоняется сообщением с названием модели, а MCP-сервер для дочернего агента подключается, только если списки его модели могут пропустить хотя бы один инструмент этого сервера. Задание планировщика подчиняется тем же правилам, а субагент памяти, чьи инструменты задаёт среда выполнения, не затрагивается;
- **Это не граница безопасности.** Списки определяют, что показывают модели, чтобы сберечь её контекст. Что сессии разрешено делать, определяют режим, `tools.permission_mode`, [хуки](../features/hooks.md) и шлюзы доверия;
- **Опечатки.** `coddy -t` и `coddy --dry-run` предупреждают о записи, которая не подходит ни к одному инструменту, и называют её строку, а сервер пишет то же в журнал при запуске. Это остаётся предупреждением: сборка не содержит инструментов тегов, без которых её собрали (например, планировщика), а инструмент MCP существует, только пока подключён его сервер, поэтому запись с `__` не отмечается никогда.

### Прокси провайдера

**`providers[].proxy`** выбирает маршрут каждого запроса, который делает строка провайдера, - генерации, список моделей, расход лимитов аккаунта, вход через OAuth или по коду устройства, обновление токена и отзыв при выходе.

- без значения или **`inherit`** (по умолчанию) - прокси, который называет окружение процесса Coddy. **`HTTPS_PROXY`** покрывает адреса **`https://`**, **`HTTP_PROXY`** - адреса **`http://`**, **`NO_PROXY`** перечисляет хосты, к которым идут напрямую, а loopback-адрес никогда не проксируется. **`ALL_PROXY`** не читается. Coddy всегда работал так - пустое значение никогда не означало прямого подключения, что бы ни говорили старые описания поля;
- **`none`** - прямое подключение. Строка не учитывает эти переменные, поэтому провайдер, доступный напрямую, продолжает работать, когда прокси машины сломан, устарел или медленный, например корпоративный прокси с инспекцией TLS, локальный форвардер или переменная, оставшаяся от другой настройки;
- URL прокси (**`http://`**, **`https://`**, **`socks5://`**, **`socks5h://`**) - все запросы строки идут через этот прокси, включая хосты из **`NO_PROXY`** и loopback-адреса. С любой из схем SOCKS имена хостов разрешает прокси. Учётные данные указываются в URL (**`http://user:pass@host:3128`**), и **`$`** в них переживает сохранение настроек (см. [Ссылки на переменные окружения](#ссылки-на-переменные-окружения)).

```yaml
providers:
  - name: corp          # needs the machine's proxy: nothing to set
    type: openai
    api_key: "${CORP_API_KEY}"
  - name: local         # reachable directly, HTTPS_PROXY or not
    type: openai
    api_base: http://192.168.1.20:8000/v1
    proxy: none
  - name: remote        # a proxy of its own, whatever the environment says
    type: anthropic
    api_key: "${ANTHROPIC_API_KEY}"
    proxy: socks5h://127.0.0.1:1080
```

Настройка относится только к своей строке, поэтому строки с разными маршрутами уживаются в одном файле. **`gateways.telegram.proxy`** Telegram-шлюза принимает те же значения для собственных запросов бота ([Telegram-шлюз](../surfaces/gateway.md#прокси)). Настройка касается запросов, которые отправляет процесс Coddy; страница, которую вход открывает в вашем браузере, и всё, что делает **`api_key_command`**, в неё не входят. Изменённое значение применяется к запросам, которые строка начинает после изменения; вход, который уже ждёт браузер, завершается по тому маршруту, с которым начался. Переменные окружения читаются один раз за процесс, поэтому после их изменения нужен перезапуск. Ключевые слова принимаются в любом регистре и записываются обратно в нижнем; всё остальное, кроме URL прокси, считается ошибкой конфигурации, и в сообщении перечислены допустимые значения (**`direct`** из вызова **`http_request`** к ним не относится). В веб-интерфейсе над полем **URL прокси** в строке есть переключатель **Игнорировать системный прокси**, который записывает **`none`** ([Веб-интерфейс](../surfaces/web-ui.md#прокси-провайдера-в-настройках)). Если провайдер недоступен, **`coddy --dry-run`** в подсказке называет маршрут, которым шёл запрос (прокси строки, прямое подключение или прокси из окружения), без учётных данных.

### `openai`
Стандартный OpenAI API. Поддерживает актуальные семейства моделей с рассуждением (`gpt-6-astra`, `gpt-5.6-sol`, `gpt-5.6-terra`, `gpt-5.6-luna`, `gpt-5.5`), а также более старые id серии `o` и `gpt-4`.

Провайдеру нужен **`api_key`**. Необязательный **`proxy`** задаёт маршрут запросов этой строки ([Прокси провайдера](#прокси-провайдера)). Строка **`models[].model`** должна начинаться с **`name`** этого провайдера и косой черты, за которыми идёт id модели в OpenAI API, например **`openai/gpt-5.6-terra`**. Задайте также **`max_tokens`**, а для id без рассуждения ещё и **`temperature`**.

### `anthropic`
Anthropic API. Поддерживает `claude-3-5-sonnet-*`, `claude-3-5-haiku-*`, `claude-3-opus-*`.

Провайдеру нужен **`api_key`**. Необязательный **`api_base`** переопределяет базовый URL Anthropic API (по умолчанию **`https://api.anthropic.com`**), например для Anthropic-совместимого шлюза или релея. Необязательный **`proxy`** задаёт маршрут запросов этой строки ([Прокси провайдера](#прокси-провайдера)). Используйте **`models[].model`** вида **`anthropic/claude-3-5-sonnet-20241022`**, а также **`max_tokens`** и **`temperature`**.

### `neuraldeep`
NeuralDeep API через его OpenAI-совместимый эндпоинт.

Учётные данные берутся либо из входа через хаб, либо из обычного ключа. **`coddy providers login neuraldeep`** выполняет вход по коду устройства - печатает короткий код и адрес страницы хаба, где его подтверждают, поэтому браузер может быть на любой машине (ноутбуке, телефоне), пока Coddy работает на сервере, к которому вы подключены по SSH. Если на этой машине есть свой браузер, команда сама открывает эту страницу. **`--browser`** вместо этого включает старый вариант с обратным вызовом на loopback-адрес; он завершается только в браузере, запущенном на этой машине. Вход ждёт подтверждения до истечения пятнадцатиминутного срока хаба, а Ctrl-C завершает его раньше. В обоих случаях выданный хабом ключ сохраняется в **`$CODDY_HOME/providers/<name>/neuraldeep-auth.json`**, а модели тарифа добавляются в конфигурацию; встроенный веб-интерфейс предлагает в строке провайдера кнопку **Войти через NeuralDeep**. Явный **`api_key`** (или **`api_key_command`** / **`NEURALDEEP_API_KEY`**) всегда перекрывает сохранённый вход.

Пока активна модель провайдера с источником данных о лимитах, футер консоли, удалённая консоль и HTTP API показывают расход лимитов аккаунта, который обновляется при старте сессии и после каждого хода; см. **`docs/surfaces/console.md`** (футер, `/usage`) и **`docs/reference/http-api.md`** (**`GET /coddy/providers/{name}/usage`**). Сейчас **`type: neuraldeep`** читает у хаба **`GET /v1/limits`**, маршрут только для чтения (окна сессии и недели в процентах использования со временем сброса, кошелёк в рублях, исчерпанный лимит со временем сброса); **`type: codex`** читает эндпоинт расхода бэкенда Codex с сохранённым OAuth-токеном ChatGPT (тариф и окна в процентах по их исходной длительности, плюс записи по отдельным функциям); **`type: devin`** читает RPC статуса управления местами на сервере Devin API (тариф и дневные или недельные окна квоты в процентах использования для тарифов с квотами либо счётчик ACU для тарифов на ACU). Используются собственные учётные данные строки; суммы в долларах и выдуманные счётчики запросов не показываются никогда. Панель включена по умолчанию; **`usage_limits_panel: false`** в строке (переключатель **Панель лимитов** в разделе Настройки → Провайдеры LLM) скрывает её во всех интерфейсах и прекращает чтение расхода лимитов для этой строки - это нужно для общего экрана или для чужого аккаунта, за расходом которого не вам следить.

Один и тот же API обслуживают два развёртывания - **`https://api.neuraldeep.ru/v1`** для России и **`https://api.neuraldeep.tech/v1`** для остальных стран. **`api_base`** выбирает одно из них - оставьте поле пустым для первого, а любое значение, кроме этих двух, сводится к первому (об этом предупреждает сообщение при запуске). Выбор привязан к учётным данным - вход идёт на соответствующий **`hub.neuraldeep.ru`** или **`hub.neuraldeep.tech`**, поэтому выберите эндпоинт до входа (**`coddy providers login neuraldeep --api-base https://api.neuraldeep.tech/v1`** или выпадающий список эндпоинтов в настройках). Вход с **`--api-base`** также переводит существующую строку провайдера на этот эндпоинт (если не указан **`--no-config`**), чтобы строка и ключ совпадали; в настройках вход следует значению, выбранному в выпадающем списке формы, ещё до сохранения. Ключ, выпущенный одним хабом, другой хаб не принимает; Coddy предупреждает при запуске, если сохранённый вход и выбранный эндпоинт не совпадают, а строка в настройках показывает то же предупреждение сразу. **`CODDY_NEURALDEEP_BASE_URL`** и **`CODDY_NEURALDEEP_HUB_URL`** по-прежнему перенаправляют весь процесс для стендов и тестов и перекрывают конфигурацию. Необязательный **`proxy`** задаёт маршрут запросов этой строки, включая вход через хаб и чтение расхода лимитов ([Прокси провайдера](#прокси-провайдера)). Используйте **`models[].model`** вида **`neuraldeep/qwen3.6-35b-a3b`**, а также **`max_tokens`** и **`temperature`**.

### `devin`
Модели аккаунта Devin (Cognition), к которым Coddy обращается так же, как Devin CLI.

**`coddy providers login devin`** выполняет вход через браузер (PKCE, как **`devin auth login`**) - страница Devin возвращает браузер на loopback-порт этой машины, а при работе по SSH вместо этого вставьте в терминал адрес, на котором браузер остановился. **`--devin-cli`** повторно использует вход, который уже есть у Devin CLI, и браузер не открывает. Токен сессии хранится в **`$CODDY_HOME/providers/<name>/devin-auth.json`**; без него провайдер обращается к **`credentials.toml`** Devin CLI, а явный **`api_key`** (или **`api_key_command`** / **`DEVIN_API_KEY`**) перекрывает оба источника. Вход добавляет по одной модели на семейство, например **`devin/claude-opus-5`**, а варианты семейства становятся её **`reasoning_levels`** - уровень **`high`** отправляется как **`claude-opus-5-high`**. **`api_base`** игнорируется; необязательный **`proxy`** задаёт маршрут для входа, каталога и чата ([Прокси провайдера](#прокси-провайдера)). Полное описание, включая то, как уровни соответствуют вариантам и как выбирается предел вывода, - на странице [Devin](../features/devin.md).

### Несколько профилей одного типа провайдера

Каждая строка служит профилем. Несколько строк могут иметь один тип, каждая под своим именем, и у каждой свой вход, свои модели и свой расход лимитов. Три аккаунта ChatGPT - это три строки **`codex`**, два аккаунта NeuralDeep - две строки **`neuraldeep`**.

```yaml
providers:
  - name: "codex"
    type: "codex"
  - name: "codex-work"
    type: "codex"
  - name: "neuraldeep"
    type: "neuraldeep"
  - name: "nd-tech"
    type: "neuraldeep"
    api_base: "https://api.neuraldeep.tech/v1"
models:
  - model: "codex-work/gpt-5.5"
  - model: "nd-tech/qwen3.6-35b-a3b"
```

Каждая строка входит отдельно - кнопкой входа в её строке в настройках или командой **`coddy providers login <name>`** в терминале. Строку, которой ещё нет в config.yaml, создаёт её вход, если **`--type`** называет тип (**`coddy providers login codex-work --type codex`**). Вход сохраняется в **`$CODDY_HOME/providers/<name>/`**, модель строки называется **`<name>/<model id>`**, а переменная **`<NAME>_API_KEY`** строки (**`ND_TECH_API_KEY`**) принадлежит только этой строке; строка codex вообще не читает переменную с ключом.

Вход Codex CLI (**`~/.codex/auth.json`**, **`CODEX_HOME`**) и вход Devin CLI соответствуют одному аккаунту каждый, поэтому каждый из них заменяет собственный вход только одной строке - единственной строке своего типа или, если их несколько, строке с именем **`codex`** (**`devin`**). Любая другая строка входит сама, а не работает молча на этом аккаунте. Если в конфигурацию, где единственная строка **`chatgpt`** работала на входе Codex CLI, добавить вторую строку codex, то и **`chatgpt`** останется без входа, пока не выполнит вход или не будет переименована в **`codex`**. Журнал запуска, **`coddy --dry-run`**, **`coddy providers list`** и строка в настройках называют такую строку и строку, которую обслуживает вход CLI, а **`--devin-cli`** отказывает строке, которую вход Devin CLI не обслуживает.

Строки различаются и там, где они показываются. Заголовок панели лимитов и **`/usage`** в консоли называют строку рядом с брендом (**`Codex · codex-work`**), если строка не названа по своему типу, а вход в NeuralDeep подписывает свой ключ на хабе именем строки (**`coddy @ host (nd-tech)`**).

### Локальные OpenAI-совместимые серверы (Ollama, llama.cpp, LM Studio)
Используйте **`type: openai`** и задайте в **`api_base`** OpenAI-совместимый базовый URL, который уже включает **`/v1`**, например **`http://localhost:11434/v1`** для Ollama.

<!-- docsgen:source sha256=383df4711c8e01a1 -->
