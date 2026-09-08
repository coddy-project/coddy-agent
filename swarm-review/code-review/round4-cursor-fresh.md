## Verdict: APPROVE_WITH_CHANGES

Прошлые девять пунктов по коду в целом держатся (SSRF+pin, DELETE на mount, VerifyLease до hijack, tunnel/TTL, dial options, hop budget, `access_token`, httpx). Ниже то, что три раунда всё ещё не закрыли.

## Findings

1. [major] `external/swarm/registry.go:223` - при renewal для `direct` старый transport закрывается до успешного `newDirectTransport`; при ошибке сборки (например CA) lease остаётся с `generation++`, обновлённым advertise и `transport == nil`, то есть владелец сам выбивает себе маршрут до следующего удачного heartbeat - собирать новый transport, затем атомарно подменять; при ошибке не трогать живой transport и не двигать generation

2. [major] `external/swarm/topology.go:111` - кратчайший `Path` по BFS верный, но "полезные" alternates неполные: узел расширяется только при первом достижении, поэтому equal-cost вход в mid (diamond) не порождает alternate для потомков mid (есть `left/mid/target`, нет `right/mid/target`) - при equal-length alternate либо дорасширять из второго parent, либо явно документировать и тестировать, что failover только от дерева кратчайших путей плюс cross-edges в сам узел

3. [minor] `external/swarm/topology.go:58` - godoc обещает "equally reachable alternatives", а `TestComputeRoutesTakesTheShortWayRoundARing` и код намеренно оставляют более длинный обход как failover - привести комментарий в соответствие с поведением (или наоборот отфильтровать по длине, если контракт другой)

4. [minor] `external/swarm/swarm_test.go:228` - `secretOf` лезет в `r.nodes` под mutex; это assertion на устройство registry, а не на публичное поведение - возвращать secret только через уже известный RegisterResponse / тестовый seam без доступа к map

5. [minor] `internal/netx/dial.go:231` - выбор proxy из окружения всегда идёт через `Scheme: "https"`, поэтому для raw dial на http-адрес может выбраться `HTTPS_PROXY` вместо `HTTP_PROXY` - строить probe URL со схемой из вызывающего dial (или тестировать оба)

6. [minor] `internal/netx/egress.go:47` - RFC6598 `100.64.0.0/10` (CGNAT/Tailscale и т.п.) не `IsPrivate` и не в deny-list; default policy их пускает - решить явно (refuse или документ) и покрыть тестом рядом с metadata

7. [minor] `external/swarm/sessions.go:29` vs `internal/config/swarm.go:23` - два независимых `*MaxHops = 4`; mount/fan-out не используют config-константу - один источник правды

## What three rounds of review still have not covered

Состояние lease при неудачном rebuild transport (build-then-swap) и полнота alternate-маршрутов за diamond merge. Поведение env-proxy dialer (`HTTP_PROXY`/`HTTPS_PROXY`) почти без поведенческих тестов; join `http.Client` по-прежнему с дефолтными redirect без явной политики.
