# Безопасная синхронизация KudaGo и Timepad

Этот документ предназначен человеку-оператору. Команды ниже требуют отдельного разрешения на production-действия. Наличие кода и mock-тестов не подтверждает доступность API, production city metadata, токен или права использования контента.

## Состояние и предохранители

Оба встроенных API по умолчанию выключены во всех окружениях. Их клиенты не создаются, sync runs не открываются, таймер не добавляет их обратно. Generic sources продолжают работать по собственным настройкам; `source_kind=system` не становится встроенным API.

`docker stop` недостаточен: `deploy/deploy.sh` пересоздаёт `event-sync`. Долговременный выключатель — env-флаг внутри нового контейнера. После изменения env требуется recreation сервиса; горячего применения нет. При 401/403/429 периодический worker блокирует только этот источник до операторского перезапуска. Его исторические данные сохраняются. Перезапуск снимает память о блокировке, поэтому до расследования ошибки оператор должен установить флаг OFF.

| Переменная | Default Go / production Compose | Допустимые значения и эффект |
|---|---|---|
| `EVENT_SYNC_KUDAGO_ENABLED` | `false` / `false` | Только `true` или `false`; выключает также ручной импорт |
| `EVENT_SYNC_TIMEPAD_ENABLED` | `false` / `false` | Только `true` или `false`; при ON обязателен токен; выключает backfill |
| `EVENT_SYNC_UPSERT_ONLY` | `true` / `true` | Полный reconciliation требует явного `false` и отсутствия page cap |
| `KUDAGO_MAX_REQUESTS_PER_MINUTE` | `6` / `6` | Целое 1–30; каждая HTTP-попытка, включая retry |
| `TIMEPAD_MAX_REQUESTS_PER_MINUTE` | `6` / `6` | Целое 1–30; общий для страниц, poster/details, redirect hops, retries данного клиента |
| `KUDAGO_MAX_PAGES_PER_RUN` | `20` / `20` | 0–1000; положительный cap всегда upsert-only |
| `TIMEPAD_MAX_PAGES_PER_RUN` | `20` / `20` | 0–1000; 0 снимает операторский cap, сохраняет hard safety ceiling |
| `EVENT_SYNC_MAX_RUN_DURATION` | `45m` / `45m` | 1s–2h, deadline отдельно для каждого провайдера; backfill также ограничен |
| `EVENT_SYNC_INTERVAL` | `2h` / `24h` | Положительная Go duration; отсчёт после завершения цикла |
| `EVENT_SYNC_RUN_ON_START` | `false` / `false` | Обычный production worker не выполняет импорт при старте |
| `KUDAGO_PAGE_SIZE`, `TIMEPAD_PAGE_SIZE` | 100 / 100 | 1–100; не отменяют пагинацию |
| `KUDAGO_TIMEOUT`, `TIMEPAD_TIMEOUT` | `30s` / `30s` | Положительный HTTP timeout; дополнительно действует общий deadline |
| `EVENT_SYNC_CITY_ID` | Москва / Москва | UUID существующей city metadata; проверяется профиль, имя и timezone |
| `KUDAGO_LOCATION`, `TIMEPAD_CITY_NAME` | `msk`, профиль Москвы / `msk`, `Москва` | Должны соответствовать выбранному городу; дополнительные worker задают явно |
| `EVENT_SYNC_CITY_PROFILES` | встроенные Москва, Казань, СПб | JSON дополнительных профилей без дублирования встроенных идентичностей |
| `SPB_EVENT_SYNC_KUDAGO_ENABLED`, `SPB_EVENT_SYNC_TIMEPAD_ENABLED` | Compose `false` | Отдельные flags только opt-in сервиса `event-sync-spb` |
| `SPB_EVENT_SYNC_UPSERT_ONLY` | Compose `true` | Отдельный режим СПб; остальные budgets наследуются от основного worker |

Операторские 6/min — стартовый запас, а не обещание провайдерной квоты. [Timepad описывает максимум 60 запросов/min на IP](https://dev.timepad.ru/api/what-api-can/). Advisory lock сериализует один provider между городами и ручными командами при общей БД. Он не ограничивает внешние клиенты и другие БД с того же IP. Между последовательными процессами limiter начинается заново; не запускать их плотной очередью, оставлять минутный запас и учитывать весь egress VPS.

## Поведение импорта и диагностика

Timepad отправляет одно `cities` на каждой странице через URL encoding, сохраняет `limit`, `skip`, `sort=+starts_at`, диапазон и `fields`, затем проверяет город каждой записи. Первая `total` фиксируется. Drift, повторные ID, несогласованная короткая/пустая страница и невалидные поля завершают run без reconciliation. Вторичная сортировка API не подтверждена: одинаковые timestamps и изменяемый каталог могут сдвигать offset даже при неизменном total. Дедупликация ловит повторы, но не доказывает отсутствие невидимых пропусков; первое включение `cities` выполняется только upsert-only с отдельной сверкой.

401/403 не повторяются. 429 учитывает `Retry-After` в пределах конечного бюджета; длинная пауза прекращает run. 5xx/transport/read failures имеют ограниченные попытки и backoff. Redirect KudaGo не следует автоматически; pagination не может сменить origin/city/window. Redirects страниц Timepad проверяются по HTTPS allowlist и проходят тот же limiter.

`scope=limited_upsert_only` отличает намеренно ограниченный импорт от `scope=full`; `state=succeeded` у limited-run означает завершение ограниченной задачи. Смотрите `limit_reason`, `http_attempts`, `initial_total`, `final_offset`, `pages_fetched`, `fetched`, `matched`, `normalized`, `inserted`, `updated`, `reject_city`, `errors`, `reconciled`, `duration`, `city_id` и `final_status` в structured logs. Новые HTTP/cursor/scope counters находятся в логах, существующие persistence counters — также в `provider_sync_runs`; миграция audit-схемы не требуется.

Сохраняются только allowlisted коды: `provider_http_401/403/429/5xx`, `provider_timeout`, `provider_cancelled`, `provider_transport_error`, `provider_decode_error`, `provider_pagination_error`, `provider_city_mismatch`, `provider_empty_result`, `provider_persistence_error`, `provider_finalization_error`, `provider_coverage_drop`, `timepad_normalization_empty`. Raw URL/query, Authorization, payload и HTML ошибок в sync audit не сохраняются.

Полный reconciliation разрешён только успешному полному run без ошибок, после явного включения оператором. Область — собственный provider/city/window. Дополнительно, если в окне есть активные записи и текущим run замечено меньше половины, фиксируется failed `provider_coverage_drop`, деактивации нет. Это консервативный предохранитель, а не доказательство полноты каталога. Ошибка финализации не объявляется успехом; после жёсткого kill run может остаться `running`, что также не разрешает reconciliation.

## Порядок включения на VPS — только оператором

1. До релиза установить в приватном env оба flags `false`, SPB flags `false`, `EVENT_SYNC_RUN_ON_START=false`, `EVENT_SYNC_UPSERT_ONLY=true`, budgets 20 страниц/45m/6 requests/min. Сохранить предыдущие flags и image SHA в приватном месте. Не печатать env, DSN и полную `docker compose config`.
2. Согласовать backup и применение миграций. `000033` добавляет только city metadata СПб: `19096037-cba9-55e0-8a99-322ce61554a4`, `Europe/Moscow`, `spb`, `Санкт-Петербург`. Миграция fail-closed при другом UUID существующего СПб или конфликте зарезервированного UUID. Не переименовывать/не удалять существующий город ради обхода ошибки: остановить rollout и согласовать identity mapping. `Down` сохраняет metadata и ссылки на неё. Миграции 25/30/31/32 не менялись.
3. После согласованного релиза пересоздать обычный worker с OFF flags и проверить только безопасные логи. `deploy/deploy.sh` не запускает opt-in SPB service. Отсутствие built-in runs при timer tick и startup — условие продолжения.

```bash
# Выполняет человек на VPS после отдельного разрешения релиза.
dc=(docker compose --env-file .env.production -f compose.production.yaml)
"${dc[@]}" config --quiet
"${dc[@]}" up -d --no-deps --force-recreate event-sync
"${dc[@]}" logs --since=10m --tail=100 event-sync
```

4. Подтвердить действительность Timepad token, право доступа и разрешение на один low-volume read-only probe с VPS. Пробу выполняет оператор своим утверждённым инструментом без вывода токена; проверить 200, `cities=Москва`, `total`, `location.city`. До разрешения этот шаг не выполнять. KudaGo остаётся OFF.
5. Для первого Timepad Moscow run оставить постоянный сервис с OFF flags и провести один ручной импорт с временным flag только данного контейнера. Это предотвращает автоматические повторы и не меняет env остальных сервисов. Нельзя запускать параллельно poster recovery или другой city-worker.

```bash
# Только после разрешённого probe. Не меняет постоянный flag.
"${dc[@]}" run --rm --no-deps \
  -e EVENT_SYNC_KUDAGO_ENABLED=false -e EVENT_SYNC_TIMEPAD_ENABLED=true \
  -e EVENT_SYNC_UPSERT_ONLY=true -e TIMEPAD_MAX_PAGES_PER_RUN=20 \
  -e TIMEPAD_MAX_REQUESTS_PER_MINUTE=6 -e EVENT_SYNC_MAX_RUN_DURATION=45m \
  event-sync /app/import-events --provider=timepad --city=moscow --max-pages=20
```

6. Сверить fetched/matched/normalized/errors/reconciled, duration и poster attempts, затем active/eligible по PoolBuilder, ссылки, постеры, цены и качество. Исторические 10 088 Timepad и 363 KudaGo future active — наблюдавшийся baseline, не норматив и не гарантия текущего состояния. `reconciled` limited-run должен быть 0. Ошибка на N-й странице не разрешает full reconciliation.
7. Только после проверки baseline/фильтра и отдельного решения согласовать full-run: `TIMEPAD_MAX_PAGES_PER_RUN=0` и `EVENT_SYNC_UPSERT_ONLY=false`, достаточный конечный deadline, операторский контроль. Если данные не помещаются в budget, считать run неполным. Для расписания отдельно согласовать 24h, включить только Timepad flag в приватном env, сохранить `RUN_ON_START=false` и пересоздать сервис. Проверить фактическое отсутствие пересечений всех клиентов данного IP.
8. СПб: после успешной migration/city сверки — один отдельный ограниченный run, пока основной периодический worker выключен/вне окна. Профиль не включается стандартным deploy.

```bash
"${dc[@]}" --profile spb run --rm --no-deps \
  -e EVENT_SYNC_KUDAGO_ENABLED=false -e EVENT_SYNC_TIMEPAD_ENABLED=true \
  -e EVENT_SYNC_UPSERT_ONLY=true -e TIMEPAD_MAX_PAGES_PER_RUN=20 \
  event-sync-spb /app/import-events --provider=timepad --city=configured --max-pages=20
```

После проверки СПб отдельно согласовать full и затем расписание; для постоянного opt-in сервиса используются `SPB_EVENT_SYNC_TIMEPAD_ENABLED=true` и `--profile spb up ... event-sync-spb`. Одновременные регулярные workers будут конкурировать за provider-wide lock; их интервалы запуска необходимо развести. UI выбора города — отдельная задача после наполнения и E2E с двумя аккаунтами.

9. KudaGo: оставлять OFF до официального выяснения 403 и подтверждения разрешённого доступа. Затем отдельно санкционированные probe и ограниченный upsert-only run по той же схеме. Не менять IP, UA, proxy/VPN, headers или токены для обхода 403.
10. No-go/rollback: 401/403/429, ошибки pagination/city/decode, внезапное падение eligible/coverage или риск деактивации → выключить только проблемный flag в приватном env и пересоздать соответствующий worker. Для SPB изменить SPB flag. Сохранить run ID, safe code, дату и counters; данные не удалять. Не включать full для «исправления» failed-run.

```bash
# После установки проблемного flag=false в приватном env.
"${dc[@]}" up -d --no-deps --force-recreate event-sync
# Для уже созданного SPB worker:
"${dc[@]}" --profile spb up -d --no-deps --force-recreate event-sync-spb
```

## Права и остающиеся неизвестными

[Лицензия KudaGo](https://docs.kudago.com/api/) требует прямой ссылки на конкретный первоисточник и соблюдения ограничений использования. Текущую атрибуцию в продукте оператор должен проверить до включения; API-доступ не заменяет проверку прав на тексты и изображения. [Timepad описывает cities и пагинацию](https://dev.timepad.ru/api/first-request/), а [токен наследует права пользователя](https://dev.timepad.ru/api/oauth/). Наличие документации не подтверждает права на массовую републикацию; условия использования контента и token scope требуют подтверждения владельцем интеграции. Документация описывает контракт и ограничения; фактический доступ с VPS требует отдельной разрешённой проверки.

UNKNOWN: причина KudaGo 403, причина реального Timepad failed после 117–119 страниц, фактический total с cities, текущая афиша СПб, актуальные права/token scope и атрибуция. Mock-тесты не устанавливают эти production-факты. Возможные локально воспроизведённые ошибки классифицируются независимо, без утверждения об исторической первопричине.

`description_short` остаётся источником subtitle/description. `cities` не расширяет описания. Массовый HTML enrichment не реализован: нужен отдельный бюджет, санитарная обработка и оценка прав.
