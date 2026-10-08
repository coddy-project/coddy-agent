# Docker

Запускайте Coddy как **`coddy serve`** внутри минимального образа **`scratch`**. Образ по умолчанию содержит встроенный веб-интерфейс (тег сборки **`ui`**), OpenAI-совместимый REST (**`http`**), планировщик (**`scheduler`**), долговременную память (**`memory`**), консоль (**`cli`**), шлюзы мессенджеров (**`gateway`**) и релей роя (**`swarm`**).

Связанные файлы:

- [`Dockerfile`](../../../Dockerfile) - многоэтапная сборка (бандл интерфейса на **Node**, бинарник на **Go**, рантайм на **`scratch`**);
- [`docker-compose.yml`](../../../docker-compose.yml) - запуск **`ghcr.io/coddy-project/coddy-agent`** (по умолчанию для **`docker compose`**);
- [`docker-compose.dev.yml`](../../../docker-compose.dev.yml) - сборка из исходников, публикация порта **12345**, тома;
- [`.dockerignore`](../../../.dockerignore) - держит контекст сборки маленьким; никогда не коммитьте **`config.yaml`** с секретами;
- [`examples/httpserver/docker.sh`](../../../examples/httpserver/docker.sh) - автоматический smoke-тест.

Опубликованные образы - **[coddy-agent на GHCR](https://github.com/coddy-project/coddy-agent/pkgs/container/coddy-agent)** (`ghcr.io/coddy-project/coddy-agent`). CI собирает **мультиархитектурные** манифесты (**`linux/amd64`**, **`linux/arm64`**) по тегам SemVer и, когда это уместно, пушит плавающие алиасы (**`latest`**, **`MAJOR.MINOR`**, **`MAJOR`**) - см. [`.github/workflows/docker-build-push.yaml`](../../../.github/workflows/docker-build-push.yaml).

На Apple Silicon и хостах Linux на arm64 скачивайте образ как обычно; Docker сам выбирает **`arm64`**, и в этом варианте лежит бинарник **`aarch64`**, который CI вычитывает из образа перед пушем (см. [Проверка платформ образа](#проверка-платформ-образа)). Платформу можно закрепить и явно.

```bash
docker pull --platform linux/arm64 ghcr.io/coddy-project/coddy-agent:latest
docker pull --platform linux/amd64 ghcr.io/coddy-project/coddy-agent:latest
```

Общие инструкции по сборке без Docker - **[docs/contributing/build.md](../contributing/build.md)**.

## Требования

- **Docker** с **Compose V2** (**`docker compose`**, а не только устаревший **`docker-compose`**);
- **`config.yaml`**, который вы монтируете в контейнер только для чтения (начните с **`config.example.yaml`**). Не коммитьте секреты;
- для веб-интерфейса - браузер на машине, с которой доступен опубликованный порт хоста (по умолчанию **12345**);
- для сборки образа из [`Dockerfile`](../../../Dockerfile) (**`docker-compose.dev.yml`**, **`make check-image`**) - **BuildKit**, то есть Docker Engine **23** или новее с плагином **`buildx`** (команда **`docker buildx version`** отвечает). Он есть в собственных пакетах Docker и в Docker Desktop, а к **`docker.io`** из Debian и Ubuntu нужно доустановить **`docker-buildx`**. Этапы сборки выполняются на **`$BUILDPLATFORM`**, который задаёт только BuildKit, поэтому устаревший сборщик останавливается на первом **`FROM`** с ошибкой **`failed to parse platform`**.

## Docker Compose

Compose - рекомендуемый способ запускать опубликованный образ с GHCR или собранный локально. Оба файла описывают **один сервис** с именем **`coddy`**, без сайдкаров с базой данных или Redis. Процесс - **`coddy serve`**, привязанный внутри контейнера к **`0.0.0.0:12345`**; Compose пробрасывает этот порт на хост.

| Файл | Когда использовать |
|------|-------------|
| [`docker-compose.yml`](../../../docker-compose.yml) | Скачать релиз с GHCR (**`docker compose pull`**) - повседневные и близкие к продакшену запуски без сборки на хосте |
| [`docker-compose.dev.yml`](../../../docker-compose.dev.yml) | **`build:`** из [`Dockerfile`](../../../Dockerfile) репозитория - разработка Coddy, воспроизведение CI или запуск **`examples/httpserver/docker.sh`** |

Compose V2 подмешивает необязательный **`docker-compose.override.yml`** из той же папки (по соглашению его игнорирует git), чтобы вы могли закрепить тег образа, поменять порты или добавить **`environment`**, не правя отслеживаемый файл.

### Что запускает сервис `coddy`

| Параметр | Основной compose | Dev compose |
|---------|-----------------|-------------|
| **Образ** | **`${CODDY_IMAGE:-ghcr.io/coddy-project/coddy-agent:latest}`** | **`coddy-agent:${CODDY_VERSION:-dev}`** (собирается локально) |
| **Команда** | **`CMD`** образа - **`serve -H 0.0.0.0 -P 12345`**, переопределяется через **`CODDY_COMMAND`** | То же |
| **Опубликованный порт** | **`${CODDY_HTTP_PORT:-12345}:12345`** | То же |
| **Рабочая папка** | **`/workspace`** (**`CODDY_CWD`**) | То же |

**Проверка состояния** - **`coddy --version`** каждые 5 с (контейнер становится "healthy", как только бинарник отвечает; готовность HTTP проверяется отдельно - выполните **`curl /v1/models`** после **`up`**).

**Логирование** - драйвер **`json-file`** с **`max-size: 100k`** на файл, чтобы логи не росли без ограничений на долгоживущих хостах.

### Пути хоста и окружение

Bind-монтирования (пути на хосте переопределяются переменными окружения до **`up`**).

| Хост (по умолчанию) | Контейнер | Переменная |
|----------------|-------------|----------|
| **`./config.yaml`** | **`/home/user/.coddy/config.yaml`** (только для чтения) | **`CODDY_CONFIG`** |
| **`./workspace`** | **`/workspace`** | **`CODDY_CWD`** |
| **`./coddy_home`** | **`/home/user/.coddy`** | **`CODDY_HOME`** |

Зафиксировано внутри контейнера (задаётся в **`environment`** compose).

| Переменная | Значение | Назначение |
|----------|-------|------|
| **`CODDY_CONFIG`** | **`/home/user/.coddy/config.yaml`** | Направляет загрузчик на смонтированный файл; сам образ задаёт **`/home/user/.coddy.yaml`**, вне тома домашней папки |
| **`CODDY_HOME`** | **`/home/user/.coddy`** | Сессии, скилы, хранилище планировщика |
| **`CODDY_CWD`** | **`/workspace`** | cwd по умолчанию для новых сессий и путей инструментов |

Ключи провайдеров можно передать из shell хоста (необязательно, если не заданы - пусто):

- **`OPENAI_API_KEY`**, **`ANTHROPIC_API_KEY`**, **`DEEPSEEK_API_KEY`**.

Секреты лучше передавать через конфигурацию или оркестратор; не коммитьте настоящие ключи.

### Пользователь контейнера

Образ запускает Coddy от непривилегированного пользователя с uid и gid **1000** и никогда от root. Это первый пользователь большинства Linux-десктопов, поэтому папки, которые Compose монтирует из текущей папки, остаются доступными на запись и вам, и контейнеру. Если папки принадлежат кому-то другому, задайте для Compose **`CODDY_UID`** и **`CODDY_GID`** (сервис задаёт **`user: "${CODDY_UID:-1000}:${CODDY_GID:-1000}"`**) или передайте **`--user`** в **`docker run`**.

```bash
CODDY_UID=$(id -u) CODDY_GID=$(id -g) docker compose up -d
```

Образы до этого изменения работали от root, поэтому домашняя папка, которую они записали, принадлежит root, и непривилегированный процесс не может в неё писать. Один раз передайте папки пользователю, затем запустите новый образ.

```bash
sudo chown -R 1000:1000 coddy_home workspace
```

Образ проверяет своё состояние так же, как Compose, - запуском бинарника (**`HEALTHCHECK`** **`/bin/coddy --version`**), потому что в образе scratch нет ни shell, ни HTTP-клиента, чтобы опросить сам сервер.

### Команды Compose

**Опубликованный образ** (из корня репозитория или любой папки, где лежат **`config.yaml`**, **`workspace/`**, **`coddy_home/`**).

```bash
docker compose pull
docker compose up -d
docker compose ps
docker compose logs -f coddy
curl -sS http://127.0.0.1:12345/v1/models | head
docker compose restart coddy
docker compose down
```

Тег GHCR закрепляется без правки YAML.

```bash
export CODDY_IMAGE=ghcr.io/coddy-project/coddy-agent:0.2.0
docker compose pull
docker compose up -d
```

Только порт на хосте меняется так.

```bash
export CODDY_HTTP_PORT=8080
docker compose up -d
# UI: http://127.0.0.1:8080/
```

**Сборка из исходников** (dev-файл compose).

```bash
docker compose -f docker-compose.dev.yml build coddy
docker compose -f docker-compose.dev.yml up -d --build
docker compose -f docker-compose.dev.yml logs -f coddy
docker compose -f docker-compose.dev.yml down
```

Необязательные аргументы сборки для dev-файла.

```bash
export CODDY_VERSION="$(git describe --tags --dirty 2>/dev/null || echo dev)"
export CODDY_BUILD_TAGS="http,scheduler,ui,memory,gateway"
docker compose -f docker-compose.dev.yml build coddy
```

**`CODDY_BUILD_TAGS`** записывается через запятую **без пробелов**, как в **`go build -tags=`**. По умолчанию dev-файл использует **`http,scheduler,ui,memory,gateway`**, чтобы собранный образ мог запускать шлюз мессенджеров; уберите **`gateway`**, чтобы облегчить образ. Сам [`Dockerfile`](../../../Dockerfile) собирает полный набор (см. [Что входит в образ по умолчанию](#что-входит-в-образ-по-умолчанию)).

### Добавить шлюз мессенджеров

Бот - не отдельный контейнер и не отдельная команда. **`coddy serve`** запускает каждую подсистему, которую включает смонтированный **`config.yaml`**, поэтому бот включается одной строкой в этом файле.

```yaml
gateways:
  telegram:
    enable: true
```

```bash
# Build from source: the dev image already includes the `gateway` tag.
export TELEGRAM_BOT_TOKEN="<bot-token>"          # or leave in $CODDY_HOME/.env
docker compose -f docker-compose.dev.yml up -d --build
docker compose -f docker-compose.dev.yml logs -f coddy   # expect: "telegram bot connected"
```

После этого бот и веб-интерфейс работают через один менеджер сессий, поэтому диалог в Telegram - это сессия, которую можно открыть на **`http://localhost:12345`** и смотреть, пока она идёт. **`CODDY_COMMAND`** по-прежнему переопределяет командную строку, если нужны другие флаги.

Примечания:

- **опубликованный образ GHCR содержит тег `gateway`** (CI [`docker-build-push.yaml`](../../../.github/workflows/docker-build-push.yaml) задаёт **`BUILD_TAGS=http,scheduler,ui,memory,cli,gateway,swarm`**), поэтому **`docker-compose.yml`** с **`CODDY_COMMAND=gateway`** работает на стандартном образе, и собственный **`CODDY_IMAGE`** не нужен;
- токен бота читается из **`TELEGRAM_BOT_TOKEN`** (его пробрасывают оба файла compose) или из **`$CODDY_HOME/.env`**; не кладите его в git;
- если ваш **`gateways.telegram.proxy`** указывает на прокси на самом хосте (например, **`socks5://127.0.0.1:7890`**), изнутри контейнера он недоступен - используйте **`host.docker.internal`** или добавьте **`network_mode: host`** в **`docker-compose.override.yml`**;
- режим шлюза не использует входящий порт (Telegram long-polling), поэтому проброшенный **`12345`** просто остаётся незанятым.

### Пример override

Создайте **`docker-compose.override.yml`** рядом с **`docker-compose.yml`** (Compose загружает его автоматически).

```yaml
services:
  coddy:
    image: ghcr.io/coddy-project/coddy-agent:0.2.0
    ports:
      - "8080:12345"
    environment:
      OPENAI_API_KEY: ${OPENAI_API_KEY}
```

Задавайте в override порты конкретной машины, закреплённые теги или дополнительный **`environment`**, а секреты держите вне git.

### Обновление и откат

```bash
export CODDY_IMAGE=ghcr.io/coddy-project/coddy-agent:NEW_TAG
docker compose pull
docker compose up -d
```

Сессии и конфигурация на хосте (**`./coddy_home`**, смонтированный **`config.yaml`**) переживают замену образа. Если новый бинарник не проходит проверки состояния, посмотрите **`docker compose logs coddy`** и откатите **`CODDY_IMAGE`** на предыдущий тег.

## Быстрый старт (Compose + веб-интерфейс)

Из checkout этого репозитория (или любой папки, где лежат **`config.yaml`**, **`workspace/`** и **`coddy_home/`**).

**1. Подготовьте конфигурацию и папки**

```bash
cp config.example.yaml config.yaml
mkdir -p workspace coddy_home
```

Отредактируйте **`config.yaml`** - настройте хотя бы одну запись в **`providers`** и **`models`** и задайте в **`agent.model`** идентификатор модели из списка. **`api_key`** можно оставить пустым и вместо этого передать **`OPENAI_API_KEY`** (или **`NAME_API_KEY`** для провайдера **`name`**) через **`environment`** compose.

Необязательно - задайте в YAML **`httpserver.host`** равным **`0.0.0.0`** и **`httpserver.port`** равным **12345**. **`CMD`** контейнера уже запускает **`coddy serve -H 0.0.0.0 -P 12345`**, поэтому флаги действуют, даже если **`httpserver`** в файле нет.

**2. Запустите Coddy** (опубликованный образ с GHCR).

```bash
docker compose pull
docker compose up -d
```

**3. Подключитесь через встроенный интерфейс**

Откройте в браузере на хосте (на той же машине, где работает Docker, если вы не пробросили порт).

```text
http://127.0.0.1:12345/
```

Что вы получите.

| URL | Назначение |
|-----|---------|
| **`/`** | Встроенное SPA (поле ввода чата, сессии, инструменты в транскрипте) |
| **`/#/settings`** | Редактор действующего **`config.yaml`** (**`GET/PUT /coddy/config`**) |
| **`/docs/`** | Swagger UI |
| **`/v1/models`** | Список моделей и режимов (его же использует выбор модели в SPA) |

Обычный первый сценарий в интерфейсе:

1. Убедитесь, что страница загружается (статические файлы из **`go:embed`**);
2. На панели поля ввода выберите **модель бэкенда** (строки с **`owned_by`**, отличным от **`coddy`**, берутся из вашего списка **`models`** в YAML);
3. При необходимости переключитесь между режимами **Агент**, **План** и **Чат** (профили работы сессии; **Чат** только читает);
4. Введите сообщение и отправьте его. Интерфейс вызывает **`POST /v1/responses`** с **`stream: true`** и показывает потоковый ответ ассистента;
5. Файлы, которые инструменты создают или правят, попадают в смонтированную рабочую папку (**`./workspace` на хосте** → **`/workspace`** в контейнере, **`CODDY_CWD`**).

Состояние сессий и скилов сохраняется в **`./coddy_home`** на хосте (**`CODDY_HOME`** в контейнере).

**4. Проверка работоспособности (необязательно)**

```bash
curl -sS http://127.0.0.1:12345/v1/models | head
docker compose logs -f coddy
```

**Безопасность.** Контейнер запускает **`coddy serve -H 0.0.0.0`**, и именно поэтому проброшенный порт доступен; вне контейнера по умолчанию сервер привязывается к loopback. Без **`httpserver.auth_token`** (или **`--auth-token`** / **`CODDY_HTTP_TOKEN`**) у API нет аутентификации. Относитесь к порту **12345** как к любому административному API - привязывайте его к localhost, закрывайте файрволом или для удалённого доступа ставьте перед ним обратный прокси с TLS и аутентификацией.

Для локальной сборки из **`Dockerfile`** используйте **`docker-compose.dev.yml`** - см. [Docker Compose](#docker-compose) выше.

## Что входит в образ по умолчанию

**`ARG BUILD_TAGS`** в **`Dockerfile`** по умолчанию равен **`http,scheduler,ui,memory,gateway,cli,swarm`** (через запятую, в том же значении, что и **`go build -tags=`**) - это набор, который несут бинарники релиза.

- **`http`** - **`coddy serve`** и REST-шлюз (см. **[docs/reference/http-api.md](../reference/http-api.md)**);
- **`ui`** - встроенное SPA на **`/`** (нужен **`http`**);
- **`scheduler`** - подсистема планировщика (**[docs/operate/scheduler.md](../operate/scheduler.md)**);
- **`memory`** - субагент долговременной памяти и REST памяти сессии (**[external/memory/README.md](../../../external/memory/README.md)**); поведение во время работы переключается через **`memory.enable`**;
- **`gateway`** - боты Telegram и Pachca (**[docs/surfaces/gateway.md](../surfaces/gateway.md)**), каждый под своим ключом **`enable`**;
- **`cli`** - интерактивная консоль (**[docs/surfaces/console.md](../surfaces/console.md)**) для **`docker run -it ... coddy`**;
- **`swarm`** - релей, который объединяет узлы (**[docs/operate/swarm.md](../operate/swarm.md)**), под **`swarm.enable`**.

Чтобы собрать образ **без** памяти или встроенного интерфейса, переопределите **`BUILD_TAGS`** (например, **`http,scheduler,ui`** или **`http,scheduler`**) через **`docker compose` `args`** или **`docker build --build-arg`**.

Подробности о томах и окружении для Compose - в разделе [Docker Compose](#docker-compose). При установке на голое железо без **`CODDY_CONFIG`** загрузчик предпочитает **`$CODDY_HOME/config.yaml`** (см. **`docs/getting-started/configuration.md`**).

## Как работают этапы Dockerfile

1. **`ui-builder` (Node)** - запускает **`npm ci`** и **`npm run build:go`** в **`external/ui`** и создаёт статический бандл, который копируется в дерево Go для **`go:embed`**, если **`ui`** входит в **`BUILD_TAGS`**. Этот этап выполняется на платформе сборки (**`FROM --platform=$BUILDPLATFORM`**) - бандл одинаков для всех платформ, поэтому мультиархитектурная сборка создаёт его один раз и нативно;
2. **`build` (Go)** - тоже выполняется на платформе сборки и кросс-компилирует с **`CGO_ENABLED=0`** и **`GOOS`/`GOARCH`** из **`TARGETOS`/`TARGETARCH`** BuildKit (CI собирает **`linux/amd64`** и **`linux/arm64`**), запускает **`go build -tags="$BUILD_TAGS"`** с **`-trimpath`** и **`-ldflags "-s -w -X ...Version=..."`**, записывает **`/out/coddy`** и копирует **`ca-certificates.crt`** для HTTPS-клиентов. **`TARGETOS`** и **`TARGETARCH`** намеренно объявлены без значения по умолчанию - такое значение подменяет то, что передаёт BuildKit, и именно так образ **`linux/arm64`** поставлялся с бинарником **x86-64** ([issue #482](https://github.com/coddy-project/coddy-agent/issues/482));
3. **`scratch`** - только бинарник, набор сертификатов CA и папки, в которые пишет пользователь (**`/home/user`**, **`/workspace`**, **`/tmp`**, владелец **1000:1000**); **`USER 1000:1000`**, **`HEALTHCHECK`** **`/bin/coddy --version`**, **`ENTRYPOINT`** **`/bin/coddy`**, **`CMD`** по умолчанию **`serve -H 0.0.0.0 -P 12345`** ([Пользователь контейнера](#пользователь-контейнера)). Синхронизации маркетплейса и установке плагинов не нужны ни `/tmp`, ни исполняемый файл Git - промежуточные файлы удалённых источников хранятся в доступной на запись папке `${CODDY_HOME}/tmp`, а для источников Git используется go-git. Источники HTTPS и `file://` в этом режиме работают; источникам SSH нужен механизм аутентификации, которым go-git может воспользоваться в контейнере.

## Проверка платформ образа

Docker помечает каждый вариант мультиархитектурного образа платформой, для которой его просили собрать, каким бы ни был бинарник внутри, а хост с **`qemu-user-static`** даже запускает бинарник чужой архитектуры. Для какой платформы бинарник, говорит только его собственный ELF-заголовок, и именно его читает проверка.

```bash
make check-image
make check-image IMAGE_PLATFORMS=linux/arm64
```

**`make check-image`** ([`scripts/check-image.sh`](../../../scripts/check-image.sh)) собирает образ для каждой платформы из **`IMAGE_PLATFORMS`** (по умолчанию **`linux/amd64,linux/arm64`** - то, что пушит релиз) по одной платформе за раз, экспортирует каждую файловую систему в **`dist/image/<os>_<arch>/`** и падает, если **`/bin/coddy`** там не бинарник этой платформы. Бинарник собственной платформы хоста ещё и запускается и должен напечатать версию, с которой собран. Нужен только Docker с **`buildx`** - этапы сборки выполняются нативно и кросс-компилируют, поэтому хост **amd64** собирает вариант **arm64** без эмуляции, и хватает обычного драйвера **`docker`**. Тот же скрипт запускает задание **Docker image** на каждом пул-реквесте, а workflow релиза запускает его перед пушем.

Без Docker то же самое проверяет на самом Dockerfile **`make test`** - [`features/docker_image_platforms.feature`](../../../features/docker_image_platforms.feature) читает его так, как BuildKit разрешает аргументы платформы, и падает, если этап Go компилировал бы не для той платформы, которая собирается, или работал бы под эмуляцией.

## Автоматический smoke-тест

```bash
./examples/httpserver/docker.sh
```

Скрипт создаёт временный **`config.yaml`**, поднимает **`coddy`** через **`docker-compose.dev.yml`**, ждёт **`/v1/models`** и запускает **`examples/httpserver/http_smoke_gateway.py`**.

<!-- docsgen:source sha256=797d9069cc48a10d -->
