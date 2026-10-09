# Coddy как модель в VS Code Copilot

VS Code Copilot подключает любой OpenAI-совместимый эндпоинт как чат-модель через `chatLanguageModels.json` (Manage Models, затем custom endpoint). Если направить его на работающий `coddy serve`, модели coddy и агент coddy появятся в выборе моделей Copilot Chat. Copilot читает строгий поток, описанный в разделе [Два диалекта потока](../reference/http-api.md#два-диалекта-потока); id моделей - те, что перечисляет `GET /v1/models` ([HTTP API](../reference/http-api.md)).

1. **Запустите сервер на адресе, доступном VS Code.** По умолчанию сервер слушает loopback на порту 12345, и этого хватает для VS Code на той же машине. Для VS Code на другой машине привяжите и защитите сервер так, как в рецепте [Сервер, которым управляют с ноутбука](server-driven-from-a-laptop.md), и передайте токен в VS Code как `apiKey`.

   ```bash
   coddy serve
   curl -s http://127.0.0.1:12345/v1/models | jq -r '.data[].id'   # the ids step 2 takes
   ```

2. **Зарегистрируйте эндпоинт.** По одной записи на каждый id модели. Id из `models[].model` означает, что coddy стоит на месте провайдера. Собственные инструменты Copilot предлагаются модели, а её вызовы возвращаются в Copilot, который выполняет их в редакторе, поэтому режим Agent в VS Code работает так же, как с провайдером напрямую. Включите `toolCalling`, а `vision` - когда у записи в `config.yaml` задано `multimodal: true`, потому что именно это пропускает картинки. Id `agent` - ReAct-агент coddy в роли модели. Он читает и правит своими инструментами рабочую папку, в которой запущен `coddy serve`, игнорирует инструменты, которые предлагает Copilot, а Copilot видит только ответ, поэтому оставьте у этой записи `toolCalling` выключенным и пользуйтесь ею из режима Ask в VS Code.

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
         "toolCalling": true,
         "vision": true,
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

   Copilot отправляет с каждым запросом свою `temperature`, и модель, вызванная напрямую, использует её вместо настроенной. Модель с рассуждением может её отклонить. Модель `codex` отклоняет всегда (coddy отвечает `400`, не вызывая её), модели OpenAI с рассуждением и расширенное мышление (extended thinking) Anthropic тоже отклоняют значение, которое шлёт Copilot, а Qwen3 thinking на vLLM его принимает. Добавьте в запись каждой модели с рассуждением `"thinking": true` (это модель `codex` или та, чья строка в `GET /v1/models` содержит `reasoning_levels`), и Copilot перестанет отправлять `temperature`, как для любой модели с рассуждением.

   Bearer-аутентификация по умолчанию выключена, и форма VS Code примет любую строку в `apiKey`; если задан `httpserver.auth_token`, в `apiKey` нужен этот токен. Copilot отправляет весь диалог с каждым запросом, поэтому каждый запрос становится на сервере отдельной сессией, если только запись `requestHeaders` у модели не задаёт `X-Coddy-Session-ID` с фиксированным id `sess_<hex>`, - тогда ходы попадают в один транскрипт.

3. **Помните о проверке разрешений у агента.** Модель `agent` выполняет инструменты в режиме `tools.permission_mode`, а на запрос разрешения из Copilot ответить нельзя - ход ждёт ответа, который никогда не придёт. Задайте в конфигурации сервера `bypass`, если доверяете агенту эту рабочую папку, или оставьте `ask` и отвечайте на запросы в веб-интерфейсе, где ход идёт вживую в своей сессии.

   ```yaml
   tools:
     permission_mode: bypass
   ```

4. **Проверьте, что всё работает.** Выберите "Coddy · Qwen 3.8 27B" в Copilot Chat и спросите что угодно - ответ придёт потоком, а у модели с рассуждением рассуждения покажутся в собственном раскрывающемся блоке Copilot, потому что Copilot читает поле `reasoning_content`. Тот же запрос из терминала показывает поток, который получил Copilot, - один choice, завершённый `stop` перед `[DONE]`.

   ```bash
   curl -sN http://127.0.0.1:12345/v1/chat/completions -H 'Content-Type: application/json' \
     -d '{"model":"neuraldeep/qwen3.8-27b","messages":[{"role":"user","content":"hello"}],"stream":true}' | tail -4
   ```

   Сообщение "Response contained no choices." в Copilot значит, что сервер не завершил choice. Проверьте, что `coddy serve` той версии, которая передаёт в потоке `finish_reason` (`coddy -v`, 1.1.5 или новее), и что ничто между ними не переписывает поток.

<!-- docsgen:source sha256=bb41d5bb4dc459c8 -->
