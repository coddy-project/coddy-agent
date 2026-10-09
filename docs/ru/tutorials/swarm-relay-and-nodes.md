# Релей и его узлы в Docker

**Цель.** Несколько узлов Coddy в контейнерах, один релей перед ними, одно место, где видны все сессии всех узлов, и доступ к любому узлу с вашего ноутбука. Наружу открыт только порт релея. Узлы сами подключаются к нему, поэтому им не нужны ни опубликованный порт, ни входящий маршрут.

**Окружение.** Docker с Compose v2 на одной машине (те же файлы работают и на нескольких машинах, если `RELAY_URL` указывает на настоящий адрес), опубликованный образ `ghcr.io/coddy-project/coddy-agent` (он собран с тегом `swarm`) и модель, до которой узлы могут достучаться. В примере узлы получают ключ OpenAI. Любой провайдер из раздела [Конфигурация](../getting-started/configuration.md) работает так же, в том числе локальный OpenAI-совместимый сервер. Устройство роя описано на странице [Рой](../operate/swarm.md), а эта страница - кратчайший путь к работающему рою.

## 1. Файлы

Четыре файла в одной папке. Вся защита релея держится на токенах ([Безопасность](../operate/swarm.md#безопасность)), поэтому замените три значения в `.env`, прежде чем что-либо покинет вашу машину.

Файл `.env`.

```bash
SWARM_CLIENT_TOKEN=change-me-client      # what a person or a client presents to the relay
SWARM_PAIRING_TOKEN=change-me-pairing    # what a node presents to register
NODE_A_TOKEN=change-me-node-a            # each node's own bearer, handed to the relay on join
NODE_B_TOKEN=change-me-node-b
WORKER_TOKEN=change-me-worker
OPENAI_API_KEY=sk-...
```

`relay.yaml` - конфигурация релея. С ней процесс запускает только релей и больше ничего.

```yaml
httpserver:
  enable: false

swarm:
  enable: true
  host: "0.0.0.0"
  port: 12346
  name: "outer"
  auth_token: "${SWARM_CLIENT_TOKEN}"
  pairing_tokens: ["${SWARM_PAIRING_TOKEN}"]
```

`node.yaml` - общий файл всех узлов. Узел - обычный `coddy serve` с моделью, bearer-токеном на собственном API и записью в `swarm.join`. Параметра `advertise_url` нет, поэтому узел сам открывает соединение с релеем, и релей управляет узлом через это же соединение (туннельный транспорт).

```yaml
providers:
  - name: openai
    type: openai
    api_key: "${OPENAI_API_KEY}"

models:
  - model: "openai/gpt-5.6-terra"
    max_tokens: 8192

agent:
  model: "openai/gpt-5.6-terra"

httpserver:
  enable: true
  auth_token: "${NODE_TOKEN}"

swarm:
  join:
    - url: "${RELAY_URL}"
      name: "${NODE_NAME}"
      pairing_token: "${SWARM_PAIRING_TOKEN}"
      token: "${NODE_TOKEN}"
```

Файл `docker-compose.yml`.

```yaml
x-node: &node
  image: ghcr.io/coddy-project/coddy-agent:latest
  command: serve -H 0.0.0.0 -P 12345
  working_dir: /workspace
  depends_on: [relay]

services:
  relay:
    image: ghcr.io/coddy-project/coddy-agent:latest
    command: serve
    environment:
      CODDY_HOME: /home/user/.coddy
      CODDY_CONFIG: /home/user/.coddy/config.yaml
      SWARM_CLIENT_TOKEN: ${SWARM_CLIENT_TOKEN}
      SWARM_PAIRING_TOKEN: ${SWARM_PAIRING_TOKEN}
    ports: ["12346:12346"]
    volumes:
      - ./relay.yaml:/home/user/.coddy/config.yaml:ro
      - relay_home:/home/user/.coddy

  node-a:
    <<: *node
    environment:
      CODDY_HOME: /home/user/.coddy
      CODDY_CONFIG: /home/user/.coddy/config.yaml
      CODDY_CWD: /workspace
      RELAY_URL: http://relay:12346
      NODE_NAME: node-a
      NODE_TOKEN: ${NODE_A_TOKEN}
      SWARM_PAIRING_TOKEN: ${SWARM_PAIRING_TOKEN}
      OPENAI_API_KEY: ${OPENAI_API_KEY-}
    volumes:
      - ./node.yaml:/home/user/.coddy/config.yaml:ro
      - ./workspace/node-a:/workspace
      - node_a_home:/home/user/.coddy

  node-b:
    <<: *node
    environment:
      CODDY_HOME: /home/user/.coddy
      CODDY_CONFIG: /home/user/.coddy/config.yaml
      CODDY_CWD: /workspace
      RELAY_URL: http://relay:12346
      NODE_NAME: node-b
      NODE_TOKEN: ${NODE_B_TOKEN}
      SWARM_PAIRING_TOKEN: ${SWARM_PAIRING_TOKEN}
      OPENAI_API_KEY: ${OPENAI_API_KEY-}
    volumes:
      - ./node.yaml:/home/user/.coddy/config.yaml:ro
      - ./workspace/node-b:/workspace
      - node_b_home:/home/user/.coddy

volumes:
  relay_home:
  node_a_home:
  node_b_home:
```

Здесь важны две детали. Файл конфигурации монтируется только для чтения поверх именованного тома с домашним каталогом, потому что в домашнем каталоге узел хранит свои сессии и секрет аренды, по которому он после перезапуска возвращает себе имя. Кроме того, узлы не публикуют портов, всё доходит до них через релей.

## 2. Запуск

```bash
mkdir -p workspace/node-a workspace/node-b
docker compose up -d
docker compose logs relay
```

Через несколько секунд после запуска `serve` на узлах журнал релея заканчивается строками `swarm node registered` и `swarm tunnel established`, по одной каждого вида на узел.

## 3. Проверка

`/swarm/info` открыт, всё остальное на релее требует клиентского токена.

```bash
export T=change-me-client
curl -s http://127.0.0.1:12346/swarm/info
curl -s -H "Authorization: Bearer $T" http://127.0.0.1:12346/swarm/nodes
curl -s -H "Authorization: Bearer $T" http://127.0.0.1:12346/swarm/topology
```

`node_count` равен 2, в списке узлов есть `node-a` и `node-b` с `"transport": "tunnel"` и `"online": true`, а топология рисует оба узла под релеем.

Узел смонтирован под `/swarm/nodes/<name>/`, так что до собственного API узла один префикс пути, и обычные клиенты работают без изменений.

```bash
curl -s -H "Authorization: Bearer $T" http://127.0.0.1:12346/swarm/nodes/node-a/v1/models
coddy --dry-run --remote http://127.0.0.1:12346/swarm/nodes/node-a --remote-token "$T"
coddy -p "Say hello in one line." --remote http://127.0.0.1:12346/swarm/nodes/node-a --remote-token "$T"
```

Пробный запуск показывает модели, которые предлагает узел, а разовый промпт получает ответ от модели `node-a`. После этого общий список показывает эту сессию вместе с её маршрутом.

```bash
curl -s -H "Authorization: Bearer $T" 'http://127.0.0.1:12346/swarm/sessions'
```

В каждой строке рядом с id сессии есть `node_path` (здесь `["node-a"]`), потому что id выбирает каждый узел сам, и совпадения случаются.

В браузере по адресу `http://localhost:12346/` открывается собственный веб-интерфейс релея. Он запрашивает клиентский токен, а затем показывает карту роя с обоими узлами. Если нажать на узел, панель "История", поле ввода и настройки будут принадлежать этому узлу. Релей стоит посередине, и ни одна из сторон этого не замечает ([Интерфейс](../operate/swarm.md#интерфейс)).

## 4. Больше узлов

Узел без `name` регистрируется под именем хоста, а в Compose это id контейнера, поэтому сервис без `NODE_NAME` можно масштабировать.

```yaml
  worker:
    <<: *node
    environment:
      CODDY_HOME: /home/user/.coddy
      CODDY_CONFIG: /home/user/.coddy/config.yaml
      CODDY_CWD: /workspace
      RELAY_URL: http://relay:12346
      NODE_TOKEN: ${WORKER_TOKEN}
      SWARM_PAIRING_TOKEN: ${SWARM_PAIRING_TOKEN}
      OPENAI_API_KEY: ${OPENAI_API_KEY-}
    volumes:
      - ./worker.yaml:/home/user/.coddy/config.yaml:ro
      - ./workspace/workers:/workspace
```

`worker.yaml` совпадает с `node.yaml`, только без строки `name:`. Затем выполните команды.

```bash
docker compose up -d --scale worker=3
curl -s -H "Authorization: Bearer $T" http://127.0.0.1:12346/swarm/nodes
```

Появляются ещё три агента, их имена совпадают с id контейнеров. Воркеры с общим токеном и общей рабочей папкой взаимозаменяемы, ради этого сервис и масштабируют. Узел, к которому вы обращаетесь по имени, остаётся отдельным сервисом.

Узел на другой машине использует тот же `node.yaml`, только `RELAY_URL` указывает на публичный адрес релея. На этой машине ничего открывать не нужно, туннель открывается с её стороны.

## Что обычно идёт не так

- **Релей завершается при запуске с сообщением о привязке не к loopback без клиентского токена.** `SWARM_CLIENT_TOKEN` пуст. Релей на `0.0.0.0` отказывается работать открытым, `swarm.allow_insecure` снимает этот запрет для стенда и больше ни для чего;
- **Узел так и не появляется.** Его токен присоединения не совпадает ни с одним из `pairing_tokens` релея, в журнале узла написано, что регистрация отклонена. Токен сравнивается точно, вместе с пробелами;
- **Узел появляется, но запрос через его точку монтирования получает 401.** `token` в записи `swarm.join` узла не совпадает с собственным `httpserver.auth_token` узла. Релей предъявляет этот токен, когда проксирует запрос, поэтому оба значения должны быть одинаковыми;
- **За `swarm node registered` так и не следует `swarm tunnel established`.** Что-то перед релеем переупаковывает HTTP, например прокси уровня 7 или терминатор, который умеет только HTTP/2. Туннелю нужно прямое соединение из конца в конец ([Два транспорта](../operate/swarm.md#два-транспорта)). Поставьте релей за TCP passthrough или дайте ему собственный TLS через `swarm.tls`;
- **Два контейнера претендуют на одно имя.** Имя остаётся только за первым, потому что имя подтверждается секретом аренды, который выдаёт релей, а не общим токеном присоединения. Дайте каждому именованному сервису свой `NODE_NAME` или уберите имя, и тогда узел возьмёт имя хоста;
- **Узел с `advertise_url` получает отказ.** В Docker имя сервиса разрешается в частный диапазон адресов, а релей не подключается к таким адресам, если хост не указан в `swarm.allow_private_upstreams`. Внутри сети Compose всё равно правильнее использовать туннель;
- **После `docker compose restart relay` список какое-то время пуст.** Реестр хранится в памяти, и `/swarm/info` сообщает `registry_warming: true`, пока узлы не отметятся заново, а они делают это в пределах трети срока аренды (по умолчанию 30 секунд);
- **Список моделей пуст, и промпт завершается ошибкой.** У узла нет доступного провайдера. Проверьте `OPENAI_API_KEY` в его окружении или адрес локального сервера изнутри контейнера.

<!-- docsgen:source sha256=ac82d5d0485a9b74 -->
