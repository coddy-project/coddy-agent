# Переменные окружения

Здесь собраны все переменные, которые читает бинарник `coddy`, сгруппированные по областям, с указанием файла, который их читает, и страницы, где описана связанная с ними функция. Приоритет везде одинаков - флаг командной строки перекрывает окружение, а окружение перекрывает `config.yaml` и встроенное значение по умолчанию. Прежде чем Coddy обращается к переменным ниже, окружение пополняют два механизма. `$CODDY_HOME/.env` читается при запуске (`internal/config/dotenv.go`) и задаёт только те переменные, которые ещё не заданы, поэтому оболочка, Docker или systemd всегда важнее файла. Ссылки `${VAR}` внутри `config.yaml` раскрываются из окружения процесса при загрузке файла (`internal/config/expand.go`); `$$` обозначает буквальный знак доллара, а `${CWD}` - заполнитель сессии, который из окружения не берётся. Переменные, которые существуют только для набора тестов, харнесов из примеров и CI, перечислены в конце и в сам продукт не входят.

## Пути и конфигурация

| Переменная | Кто читает | Значение | Где описано |
|---|---|---|---|
| `CODDY_HOME` | `internal/config/paths.go` | Каталог состояния, в нём `config.yaml`, `sessions/`, `skills/`, учётные данные провайдеров в `providers/`, расписки о доверии и `.env`. По умолчанию `~/.coddy`; флаг `--home` перекрывает переменную. | [Конфигурация](../getting-started/configuration.md#расположение-файла-конфигурации-и-пути) |
| `CODDY_CWD` | `internal/config/paths.go` | Рабочий каталог сессии, клиент которой его не передаёт; при запуске приводится к абсолютному пути. По умолчанию рабочий каталог процесса; флаг `--cwd` перекрывает переменную. | [Конфигурация](../getting-started/configuration.md#расположение-файла-конфигурации-и-пути) |
| `CODDY_CONFIG` | `internal/config/paths.go` | Файл конфигурации, который нужно загрузить. По умолчанию `$CODDY_HOME/config.yaml`; флаг `--config` перекрывает переменную. | [Конфигурация](../getting-started/configuration.md#расположение-файла-конфигурации-и-пути) |

`${CODDY_HOME}` внутри `config.yaml` при чтении файла заменяется на итоговый каталог состояния (`internal/config/expand.go`), что бы ни содержала сама переменная.

## Провайдеры и ключи

Ключ провайдера берётся по порядку из литерала `api_key`, из stdout команды `api_key_command`, затем из переменной `NAME_API_KEY`, где `NAME` - имя провайдера в верхнем регистре с дефисами, заменёнными на подчёркивания (`rpa` превращается в `RPA_API_KEY`, `my-lab` - в `MY_LAB_API_KEY`). Имя строит `config.ProviderAPIKeyEnvVarName` в `internal/config/providers.go`, и все интерфейсы, которые сообщают, откуда взяты учётные данные (`coddy providers list`, вход в NeuralDeep по HTTP, кэш расхода лимитов), вызывают эту же функцию. Строка `codex` ключ не ищет - она входит через ChatGPT, не читает ни один из трёх источников и не запускает `api_key_command`.

| Переменная | Кто читает | Значение | Где описано |
|---|---|---|---|
| `NAME_API_KEY` | `internal/config/providers.go` (`EffectiveAPIKey`) | Запасной ключ провайдера `name`, если `api_key` и `api_key_command` ничего не дали. Читается при сборке запроса, а не при загрузке файла. Проверка безопасности команд читает переменную своей строки neuraldeep, а без такой строки `NEURALDEEP_API_KEY`. | [Конфигурация](../getting-started/configuration.md#полная-схема-конфигурации), [Безопасность команд](../features/decisions.md#учётные-данные) |
| `OPENAI_API_KEY` | `internal/config/config.go` | Кроме правила `NAME_API_KEY` для провайдера с именем `openai`, работает и так - если в файле совсем нет ни `providers`, ни `models`, загрузчик сам создаёт из этого ключа провайдер `openai` с моделью `openai/gpt-5.4`. | [Конфигурация](../getting-started/configuration.md#полная-схема-конфигурации), [Docker](../getting-started/docker.md#docker-compose) |
| `ANTHROPIC_API_KEY` | `internal/config/config.go` | Такая же начальная настройка для провайдера `anthropic` с моделью `anthropic/claude-sonnet-4-6`; применяется, когда `OPENAI_API_KEY` пуст. | [Конфигурация](../getting-started/configuration.md#полная-схема-конфигурации) |
| `BRAVE_API_KEY` | `internal/config/tools.go` (`ToolSettings`) | Ключ Brave Search API, если `tools.websearch.brave_api_key` пуст; с ключом движок `brave` обращается к официальному API вместо публичной страницы результатов. Читается, когда ход собирает настройки инструментов, поэтому в `config.yaml` ничего записывать не нужно. | [Веб-поиск](../features/web-search.md#ключ-brave-search-api) |
| `TELEGRAM_BOT_TOKEN` | `internal/config/gateway.go` (`EffectiveToken`) | Токен бота, если `gateways.telegram.token` пуст. | [Telegram-шлюз](../surfaces/gateway.md#быстрый-старт-telegram) |
| `PACHCA_BOT_TOKEN` | `internal/config/gateway.go` (`PachcaGatewayConfig.EffectiveToken`) | Токен бота Pachca, если `gateways.pachca.token` пуст. | [Шлюз Pachca](../surfaces/pachca.md#конфигурация) |
| `CODEX_HOME` | `internal/llm/codex_auth.go` | Каталог состояния Codex CLI, к `auth.json` которого обращается провайдер `type: codex`, если собственного входа Coddy нет. По умолчанию `~/.codex`. | [Конфигурация](../getting-started/configuration.md#расположение-файла-конфигурации-и-пути) |
| `CODDY_CODEX_BASE_URL` | `internal/llm/codex_auth.go` | Переопределяет эндпоинт бэкенда Codex для всего процесса; `api_base` для `type: codex` намеренно игнорируется, чтобы файл конфигурации никогда не смог перенаправить OAuth-токен. Чтение расхода лимитов аккаунта берёт хост из этого же эндпоинта. | [Конфигурация](../getting-started/configuration.md#расположение-файла-конфигурации-и-пути) |
| `CODDY_NEURALDEEP_HUB_URL` | `internal/llm/neuraldeep_auth.go` | Хаб, который выдаёт ключи при `coddy providers login neuraldeep` и отдаёт данные панели лимитов; перекрывает развёртывание, выбранное по `api_base`. | [Конфигурация](../getting-started/configuration.md) |
| `CODDY_NEURALDEEP_BASE_URL` | `internal/llm/neuraldeep_auth.go` | Базовый адрес API, куда идут все запросы NeuralDeep; перекрывает `api_base`. Предназначен для стендов и тестов. | [Консоль](../surfaces/console.md#тестирование) |
| `CODDY_DEVIN_API_SERVER_URL` | `internal/llm/devin_auth.go` | Сервер Devin API, куда идут все запросы `type: devin` (пользовательский JWT, каталог моделей, чат, чтение расхода лимитов аккаунта), а также запасной адрес для обмена кода входа; действует на весь процесс. `api_base` для `type: devin` игнорируется, поэтому файл конфигурации не может перенаправить токен сессии. Предназначен для стендов и тестов. | [Devin](../features/devin.md#стенды-и-тесты) |
| `CODDY_DEVIN_WEBAPP_URL`, `CODDY_DEVIN_API_URL` | `internal/llm/devin_auth.go` | Хост страницы входа (`https://app.devin.ai`) и Devin API, который при `coddy providers login devin` обменивает код входа на токен сессии (`https://api.devin.ai`). Предназначены для стендов и тестов. | [Devin](../features/devin.md#стенды-и-тесты) |
| `CODDY_DEVIN_CLI_CREDENTIALS` | `internal/llm/devin_auth.go` | Файл `credentials.toml` Devin CLI, к которому обращается провайдер `type: devin`, если собственного входа Coddy нет. По умолчанию `$XDG_DATA_HOME/devin/credentials.toml` или `~/.local/share/devin/credentials.toml`, смотря какой из них существует. | [Devin](../features/devin.md#откуда-берётся-токен-сессии) |
| `SSH_AUTH_SOCK` | `internal/tools/ssh/agent.go` | Сокет SSH-агента, который `ssh_run_command` пробует раньше файлов ключей; если переменная не задана или сокет недоступен, используются только файлы ключей. | [Конфигурация](../getting-started/configuration.md#удалённое-выполнение-по-ssh) |
| `BROWSER`, `NO_BROWSER` | `internal/platform/browser.go` | Может ли `coddy providers login` открыть браузер и чем. `BROWSER` называет программу, которая откроет страницу (`none` отключает открытие; шаблон с `%` или список через `:` игнорируются, и вместо них перебираются программы из `PATH`); заданная `NO_BROWSER` тоже отключает открытие. В Linux должна быть задана `DISPLAY` или `WAYLAND_DISPLAY`, а SSH-сессия (задана `SSH_CONNECTION`, `SSH_TTY` или `SSH_CLIENT`) на любой платформе считается сессией без браузера. | [Безопасность и доверие](../operate/security.md#секреты) |
| `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` | `internal/netx/dial.go` (`http.ProxyFromEnvironment` из Go) | Прокси для HTTP-транспортов, которые строит `internal/netx`, если раздел, который они обслуживают, не задаёт свой `proxy`; явный `proxy` их заменяет. Их учитывают и `http_request` с `webfetch`, если вызов не передаёт свой `proxy` (или `direct`), и все запросы строки LLM-провайдера и Telegram-бота, если `providers[].proxy` или `gateways.telegram.proxy` не равен `none` и не содержит URL прокси. Читаются и написания в нижнем регистре (`http_proxy`, `https_proxy`, `no_proxy`); если заданы оба написания одной переменной, побеждает верхний регистр, причём для туннеля роя, который устанавливает `internal/netx`, так же, как для запросов рядом с ним. `ALL_PROXY` не читается. | [Рой](../operate/swarm.md#шифрование-и-прокси), [Прокси провайдера](../getting-started/configuration.md#прокси-провайдера), [Telegram-шлюз](../surfaces/gateway.md#прокси) |

## Удалённый режим, HTTP и рой

| Переменная | Кто читает | Значение | Где описано |
|---|---|---|---|
| `CODDY_HTTP_TOKEN` | `cmd/coddy/serve.go` | Bearer-токен, который `coddy serve` требует на `/v1/*` и `/coddy/*`, если не задан `--auth-token`; `httpserver.auth_token` идёт после обоих. Токен из флага или окружения переживает горячую перезагрузку и в файл не записывается. | [Удалённый режим](../operate/remote.md#токен), [HTTP API](http-api.md#аутентификация-необязательно) |
| `CODDY_HTTP_USER` | `external/httpserver/serve.go` | Имя учётной записи для формы входа веб-интерфейса. Вместе с `CODDY_HTTP_PASSWORD` она сама включает форму, так же как `CODDY_HTTP_TOKEN` включает проверку bearer-токена, и перекрывает `httpserver.login.user` из файла. Явный `httpserver.login.enable: false` всё равно выключает форму. | [Удалённый режим](../operate/remote.md#форма-входа), [HTTP API](http-api.md#вход-в-веб-интерфейс-необязательно) |
| `CODDY_HTTP_PASSWORD` | `external/httpserver/serve.go` | Пароль к `CODDY_HTTP_USER` открытым текстом. Сервер хеширует его argon2id при запуске и никогда не записывает в `config.yaml`, поэтому естественное место для него - `$CODDY_HOME/.env`. Флага для него нет, потому что пароль в командной строке виден в `ps`. | [Удалённый режим](../operate/remote.md#форма-входа), [HTTP API](http-api.md#вход-в-веб-интерфейс-необязательно) |
| `CODDY_REMOTE_TOKEN` | `internal/remote/resolve.go` | Токен, который консоль и `coddy acp` отправляют с `--remote`, если его не дают ни `--remote-token`, ни `token` подходящей записи `httpserver.remotes`. | [Удалённый режим](../operate/remote.md#токен) |
| `CODDY_SWARM_TOKEN` | `external/swarm/serve.go`, читается в `cmd/coddy/serve.go` | Bearer-токен, который клиенты предъявляют релею, если не задан `--swarm-auth-token`; `swarm.auth_token` идёт после обоих. | [Рой](../operate/swarm.md#безопасность), [Справочник config.yaml](config.md#связанные-переменные-окружения) |
| `CODDY_SWARM_PAIRING_TOKEN` | `external/swarm/serve.go`, читается в `cmd/coddy/serve.go` | Секрет, который узел предъявляет при регистрации, если не задан `--swarm-pairing-token`; при каждой перезагрузке добавляется к `swarm.pairing_tokens`. | [Рой](../operate/swarm.md#безопасность), [Справочник config.yaml](config.md#связанные-переменные-окружения) |
| `CODDY_PACHCA_API_BASE` | `internal/config/gateway.go`, читается в `external/gateway/pachca/bot.go` и `internal/dryrun/network.go` | Базовый адрес API, к которому шлюз Pachca и проверка `--dry-run` обращаются вместо `https://api.pachca.com/api/shared/v1`, например тестовый сервер или стенд на этой машине. | [Шлюз Pachca](../surfaces/pachca.md#запуск) |
| `CODDY_TELEGRAM_API_BASE` | `internal/config/gateway.go`, читается в `external/gateway/telegram/bot.go` и `internal/dryrun/network.go` | Адрес Bot API, к которому Telegram-шлюз и проверка `--dry-run` обращаются вместо `https://api.telegram.org`, например собственный сервер Bot API или офлайн-стенд [tgfake](../contributing/tgfake.md). | [Telegram-шлюз](../surfaces/gateway.md#отладка-на-поддельном-bot-api), [Конфигурация](../getting-started/configuration.md#пробный-запуск---проверка-того-на-что-указывает-файл) |
| `CODDY_SERVE_ROLE` | `internal/serve/daemon.go` | `coddy serve --daemon` задаёт её процессам, которые запускает заново, со значением `dispatcher` или `worker`, чтобы каждая из двух половин знала свою роль. Значение `service` сообщает `coddy serve` на переднем плане, что его снова запустит менеджер сервисов, поэтому изменение конфигурации, которое меняет адрес прослушивания, завершает процесс с кодом 75, а не ждёт ручного перезапуска; его задаёт systemd-юнит из `coddy serve install`, может задать и ваш собственный супервизор. Не задавайте `dispatcher` или `worker` вручную. | [Самоперезапуск](../operate/serve.md#самоперезапуск) |

## Обновления

| Переменная | Кто читает | Значение | Где описано |
|---|---|---|---|
| `CODDY_UPDATE_NOTES` | `internal/update/notes.go` | `0`, `false`, `no` или `off` запрещают `coddy update` выводить релизы между старой и новой версией; действует так же, как `--no-notes`. | [Обновление](../getting-started/update.md#что-изменилось) |

## Переменные, передаваемые хукам

Процесс хука наследует всё окружение Coddy и дополнительно получает переменные ниже (`internal/hooks/runner.go`). Их задают для хука, сам Coddy их не читает.

| Переменная | Кто читает | Значение | Где описано |
|---|---|---|---|
| `CODDY_PROJECT_DIR`, `CLAUDE_PROJECT_DIR` | процесс хука | Рабочий каталог сессии, под обоими именами, чтобы хук Claude Code работал без изменений. | [Хуки](../features/hooks.md#что-получает-хук) |
| `CODDY_SESSION_ID` | процесс хука | Сессия, к которой относится событие. | [Хуки](../features/hooks.md#что-получает-хук) |
| `CODDY_HOOK_EVENT` | процесс хука | Имя события, например `PreToolUse`. | [Хуки](../features/hooks.md#что-получает-хук) |
| `CODDY_HOME` | процесс хука | Итоговый каталог состояния, если он известен. | [Хуки](../features/hooks.md#что-получает-хук) |

MCP-серверы, запущенные через stdio, получают окружение Coddy плюс словарь `env` из своего описания (`internal/mcp/client.go`); его значения проходят то же раскрытие `${VAR}`, что и остальная конфигурация.

## Консоль и терминал

| Переменная | Кто читает | Значение | Где описано |
|---|---|---|---|
| `CODDY_LANG` | `internal/docs/lang.go` (`LangFromEnv`) | Язык документации, которую читают справка по F1 в консоли, `coddy docs` и ходы консоли (`ru` или `en`, любое написание локали); читается раньше переменных локали ниже. Так язык выбирают в системе, где ни одна из них не задана, в том числе в Windows. `coddy docs --lang` важнее. | [Встроенная документация](../features/built-in-docs.md#языки) |
| `LC_ALL`, `LC_MESSAGES`, `LANG` | `internal/docs/lang.go` (`LangFromEnv`) | Локаль терминала, читается в этом порядке, если `CODDY_LANG` не задана: первая заданная выбирает язык документации (`ru_RU.UTF-8` - русский; `C`, язык без перевода или ничего - английский). | [Встроенная документация](../features/built-in-docs.md#языки) |
| `COLORFGBG` | `external/cli/run.go` | При `--theme auto` фон `7` или `15` выбирает светлую тему, любое другое значение - тёмную. | [Консоль](../surfaces/console.md#флаги) |
| `COLORTERM` | `external/cli/theme.go` | `truecolor` или `24bit` переключают палитру консоли на 24-битный цвет. | [Консоль](../surfaces/console.md) |
| `SSH_CONNECTION`, `SSH_TTY` | `external/cli/tui/terminal.go` | Одиночное нажатие Escape распознаётся через 100 мс по SSH и через 10 мс локально. | [Клавиатура](keyboard.md) |
| `COLUMNS` | `internal/skills/commands.go` | Ширина таблицы `coddy skills list`; по умолчанию 100, значения меньше 40 игнорируются. | [Скилы](../features/skills.md) |

## Android (Termux)

Их читает только сборка для Android (`internal/platform/android.go`, `android_init.go`), где их экспортирует Termux. Ни одну из них не нужно задавать вручную.

| Переменная | Кто читает | Значение | Где описано |
|---|---|---|---|
| `TERMUX__PREFIX`, `PREFIX` | `internal/platform/android.go` | Префикс Termux, взятый из первой из двух переменных, где записан абсолютный путь; по умолчанию `/data/data/com.termux/files/usr`. Coddy берёт из него `etc/tls/cert.pem` и `tmp`, а для пути `/bin/...` или `/usr/bin/...` - программы из его `bin`. | [Android (Termux)](../getting-started/android.md#что-coddy-подстраивает-на-android) |
| `TERMUX_APP__DATA_DIR`, `TERMUX_APP__LEGACY_DATA_DIR` | `internal/platform/android.go` | Каталог данных приложения под обоими его именами. Программа внутри него запускается через `/system/bin/linker64`, если так был запущен сам Coddy; без этих переменных его заменяет каталог, в котором лежит префикс. | [Android (Termux)](../getting-started/android.md#что-coddy-подстраивает-на-android) |
| `SSL_CERT_FILE` | `crypto/x509` из Go | Если переменная не задана, а файл существует, ей присваивается `$PREFIX/etc/tls/cert.pem`, чтобы корневые сертификаты Termux считались доверенными наравне с хранилищем Android. | [Android (Termux)](../getting-started/android.md#что-coddy-подстраивает-на-android) |
| `TMPDIR` | `os.TempDir` из Go | Если переменная не задана, ей присваивается `$PREFIX/tmp`, потому что значение Go по умолчанию для Android, `/data/local/tmp`, недоступно приложению для записи. | [Android (Termux)](../getting-started/android.md#что-coddy-подстраивает-на-android) |
| `TERMUX_EXEC__PROC_SELF_EXE` | дочерний процесс | Задаётся программе, которую Coddy запускает через системный компоновщик, и содержит путь к этой программе, потому что `/proc/self/exe` в этом случае указывает не на неё; у всех остальных дочерних процессов удаляется. | [Android (Termux)](../getting-started/android.md#что-coddy-подстраивает-на-android) |

## Docker и compose

Эти переменные читают compose-файлы и `Dockerfile`, а не бинарник. Внутри контейнера образ задаёт `CODDY_HOME=/home/user/.coddy`, `CODDY_CWD=/workspace` и `CODDY_CONFIG=/home/user/.coddy.yaml`, а оба compose-файла переопределяют `CODDY_CONFIG` смонтированным `/home/user/.coddy/config.yaml`.

| Переменная | Кто читает | Значение | Где описано |
|---|---|---|---|
| `CODDY_IMAGE` | `docker-compose.yml` | Образ для запуска; по умолчанию `ghcr.io/coddy-project/coddy-agent:latest`. | [Docker](../getting-started/docker.md#docker-compose) |
| `CODDY_COMMAND` | `docker-compose.yml`, `docker-compose.dev.yml` | Командная строка контейнера; по умолчанию `serve -H 0.0.0.0 -P 12345`. | [Docker](../getting-started/docker.md#docker-compose) |
| `CODDY_HTTP_PORT` | оба compose-файла | Порт хоста, который пробрасывается на порт 12345 контейнера. | [Docker](../getting-started/docker.md#docker-compose) |
| `CODDY_CONFIG`, `CODDY_CWD`, `CODDY_HOME` | оба compose-файла | Пути на хосте, которые монтируются как файл конфигурации, рабочая папка и каталог состояния (по умолчанию `./config.yaml`, `./workspace`, `./coddy_home`). | [Docker](../getting-started/docker.md#docker-compose) |
| `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `DEEPSEEK_API_KEY`, `TELEGRAM_BOT_TOKEN` | оба compose-файла | Передаются с хоста в контейнер; пустые, если не заданы. | [Docker](../getting-started/docker.md#docker-compose) |
| `CODDY_VERSION`, `CODDY_BUILD_TAGS` | `docker-compose.dev.yml` | Тег и список тегов сборки через запятую для локально собранного образа. | [Docker](../getting-started/docker.md#docker-compose) |

## Разработка, тесты и CI

Ни одну из этих переменных не читает релизный бинарник. Они нужны участникам разработки и перечислены здесь, чтобы поиск grep по дереву не находил недокументированных имён.

| Переменная | Кто читает | Значение | Где описано |
|---|---|---|---|
| `CODDY_HOOK_LINT`, `CODDY_HOOK_FORMAT`, `CODDY_HOOK_DOCS`, `CODDY_HOOK_TESTS`, `CODDY_HOOK_SKIP` | `scripts/checks.sh`, который запускает `.githooks/pre-commit` | Проверка перед коммитом. `CODDY_HOOK_LINT=0` пропускает линтер, `CODDY_HOOK_FORMAT=0` - проверку SPA через Prettier (она запускается, когда в коммит добавлены файлы SPA), `CODDY_HOOK_DOCS=0` - проверку документации, `CODDY_HOOK_TESTS=fast|full|matrix` добавляет тесты, `CODDY_HOOK_SKIP=1` обходит проверку целиком. | [Заметки для агентов](../../../AGENTS.md) |
| `CODDY_UI_BACKEND` | `external/ui/vite.config.ts` | Адрес `coddy serve`, на который dev-сервер Vite проксирует `/v1`, `/coddy`, `/swarm`, `/docs` и `/openapi.*` во время работы над SPA. | [Указатель ассетов](../../assets/INDEX.md) |
| `CODDY_LIVE_MODEL`, `NEURALDEEP_API_KEY` | `internal/agent/live_neuraldeep_test.go` | Включают живую проверку против эндпоинта NeuralDeep; без обеих переменных тест пропускается. | заголовок файла |
| `NEURALDEEP_API_KEY` | `internal/llm/decisions_live_test.go`, `internal/agent/decisions_live_e2e_test.go` | Включает живые пробы против реального decisions-эндпоинта, транспортную и сквозную через полный ход; обе пропускаются без ключа. | [Безопасность команд](../features/decisions.md#тестирование) |
| `CODDY_BIN`, `CODDY_CONFIG`, `CODDY_HOME`, `CODDY_CWD`, `SESSION_ROOT`, `SESSION_ID`, `BASE_URL`, `MODEL`, `WORK_DIR`, `CODDY_CHAT_PROFILE`, `NEURALDEEP_API_KEY`, `RPA_API_KEY` | скрипты-харнесы в `examples/` | Какой бинарник, конфигурацию, сервер и модель использует сквозной скрипт; docstring каждого скрипта перечисляет его собственные переменные. | [Примеры](../../../examples/README.md) |
| `CODDY_TGFAKE_BIN` | `examples/gateway/tg_e2e_offline.sh`, `examples/shared/wake_e2e_common.py`, `examples/cli/capture_wake.py`, `external/ui/scripts/telegram-mini-app-check.mjs`; задаёт CI-джоба **Telegram stand (tgfake)** | Готовый бинарник tgfake, который запускается вместо сборки `github.com/EvilFreelancer/tgfake/cmd/tgfake` версии, закреплённой в `go.mod`. | [Интеграционные тесты с tgfake](../contributing/tgfake.md) |
| `GITHUB_TOKEN` | `.github/workflows/*.yaml` | Токен GitHub Actions, которым пользуются джобы релиза, образа и тега при слиянии. Бинарник его не читает, `coddy update` обращается к API релизов анонимно. | файлы workflow |
| `GO_WANT_MCP_HELPER`, `GO_WANT_MCP_MARKER`, `GO_WANT_TRUST_MARKER`, `GO_WANT_CONFIG_RELOAD_MCP`, `CODDY_MCP_MARKER_FILE`, `MCP_HELPER_TOKEN`, `TRUST_MARKER_FILE` | тесты MCP и доверия в `internal/mcp`, `internal/session` и `external/httpserver` | Заставляют тестовый бинарник перезапустить себя как MCP-сервер-заглушку и сообщить, где он запустился. | файлы тестов |
| `CODDY_NETX_PROXY_PARITY` | тесты `internal/netx` | Заставляет тестовый бинарник перезапустить себя дочерним процессом, который сравнивает прокси, через который `netx` устанавливает туннель, с прокси, через который `net/http` отправляет соседние запросы, при обоих заданных написаниях каждой переменной прокси. | файл теста |
| `CODDY_TEST_DOTENV_A`, `CODDY_TEST_DOTENV_B`, `CODDY_TEST_DOTENV_C`, `CODDY_EXPAND_TEST`, `CODDY_CHECK_TEST_PORT` | тесты `internal/config` | Фикстуры для загрузчика `.env`, раскрытия `${VAR}` и проверки `-t`. | файлы тестов |
| `CODDY_TEST_HOME`, `CODDY_TEST_WORKSPACE`, `CODDY_TEST_SESSIONS_ROOT`, `CODDY_TEST_SKILLS_DIR`, `CODDY_TEST_PREFERRED_SESSION_ID`, `CODDY_TEST_ACP_HELPER`, `CODDY_TEST_MCP_HELPER`, `CODDY_TEST_HTTP_SETTINGS_MCP_HELPER`, `CODDY_TEST_SLOW_MCP_STARTED`, `CODDY_PROJECT_MCP_STARTED` | BDD-харнесы и вспомогательные заглушки в `cmd/coddy`, `internal/agent`, `internal/mcp` и `external/httpserver` | Указывают перезапускающему себя тестовому бинарнику временный домашний каталог, рабочую папку и корень сессий сценария и дают MCP-серверу-заглушке сообщить о своём запуске. | файлы тестов |
| `SEC_SCANNERS`, `SEC_FAIL_TRIVY`, `SEC_FAIL_SEMGREP`, `SEC_FAIL_GOVULNCHECK`, `GOVULNCHECK_TAGS`, `SEC_DOCKER`, `SEMGREP_CONFIGS` | `scripts/security-scan.sh` | Какие сканеры AppSec запускает `make security`, где проходит порог (`CRITICAL` для находок trivy об уязвимостях и секретах, выключен для semgrep, вызов уязвимой функции для govulncheck), с какими тегами сборки govulncheck загружает код, разрешён ли закреплённый запасной вариант через docker и какие наборы правил из реестра загружает semgrep. | [Сканирование AppSec](../contributing/security-scanning.md) |
| `SEMGREP_APP_TOKEN` | `scripts/security-scan.sh`, передаётся semgrep | Необязательный токен реестра Semgrep. Снимает ограничения частоты для анонимных запросов и включает правила Pro. Пул-реквестам из форков он недоступен. | [Сканирование AppSec](../contributing/security-scanning.md) |

<!-- docsgen:source sha256=d6cd8cf602784fc0 -->
