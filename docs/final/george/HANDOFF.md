# MULTICITY PR HANDOFF

Обновлено 21.10.2026. Рабочая ветка: `final/multicity-catalog`, PR target: **dev**.
Исходный функциональный scope: G1–G5 из `03_GEORGE_MULTICITY_CATALOG.md`.
Порядок Git/миграций/toolchain — по `CODEX_MULTICITY_SYNC_FIX_PR_DEV_TZ.md`.

**Статус публикации: только Draft PR**, по отдельному решению владельца от21.10.
Технические multicity gates прошли; перевод в Ready заблокирован двумя high
advisories в неизменённых frontend-зависимостях dev. Исправление зависимостей
передано Павлу как blocking TODO в описании PR; frontend здесь не меняется.
Подробности и ссылки — `MULTICITY_FIXES_REPORT.md`; фактический URL PR и SHA —
в завершающем сообщении и истории ветки.

## Сохранение и актуализация

- Исходная база: `6d16d08a554d784e5741b6de86959cd1e201c49a`.
- Актуальная база/merge-base: `1a7a8687b5a2e32b172d01d43c930599b902483b`.
- До изменений сохранены binary patch, все 38 изменённых/новых файлов, SHA-256 manifest и local-heads вне продукта: `codex_local/backups/multicity-before-sync-20261021`. Все копии совпали по hash; чужих новых изменений не было.
- Локальный контрольный commit `b017b82` перенесён rebase только собственной неопубликованной ветки. В обновлённой истории checkpoint `de30d66`. Ни dev/main, ни чужие локальные ветки не перемещались.
- Проверенная реализация и исправления объединены в один итоговый feature commit: промежуточная local26 не должна входить даже в публикуемую историю ветки. Оба checkpoint SHA выше — история сохранения; backup остаётся доступен вне продукта.
- Конфликты Timepad tests: сохранены уже исправленные часы Павла и все city tests. Конфликт embed_test: сохранены проверки public_plan/event_submissions и обязательных версий; maximum-only assertion заменён required-version set.
- WIP `fix/backend-final-audit` / `73d9da031cb11477b36021f155670949cc613c1c` не переносился и не менялся.
- Go **1.26.10** штатно выбран через свежий go.mod; go.mod/go.sum не отличаются от dev. Нет обновления зависимостей feature-веткой.

## Реализованный код G1–G5

- **G1:** воспроизводимый read-only fixture CLI, агрегаты полноты/качества, eligibility через существующий PoolBuilder, cap24/series collapse, unknown price отдельно от zero. Owner-operated read-only SQL Q1–Q4 для будущего разрешённого среза. В субботу до22:00 используется текущий вечер; invalid venue UUID не попадает в eligible, raw denominators сохраняются.
- **G2:** city-aware KudaGo/Timepad/Generic transport, normalization и persistence; валидируемое соответствие city metadata/provider IDs; общий механизм профилей новых городов; bindings для manual/scheduled Generic; PostgreSQL guards event/venue/run identity, first-write concurrency, city-scoped reconciliation и replay.
- **G3:** подтверждённые дефекты Generic ends_at<=starts_at и price_to<price_from отклоняются; допустимые nil/zero случаи сохранены. Нет утверждения об улучшении реальных KPI.
- **G4:** миграция metadata второго города; настоящий importer→DB→неизменённый PoolBuilder→room service lifecycle на маркированных synthetic cards. Кода rooms/votes/matches/ranker в feature diff нет.
- **G5:** фиксированные часы двух Timepad tests сохранены из dev, пагинационный timeout-тест стабилизирован через context cancellation; regressions config, normalization, identity, failure/cancel/partial, SQL/migrations и двух workers.

## Что подтверждено только в fixture / изолированной среде

- Московский baseline: 20 искусственных карточек, 14 eligible, 4 unknown-price. Датасет и as-of фиксированы, before/after не измеряют эффект реального импорта.
- Kazan PoC: 4 synthetic events / 4 candidates, isSmall=false, 0 нарушений проверенных city/date/budget constraints. Локальный HTTP mock, запись в isolated PostgreSQL, replay сохраняет IDs, Москва до/после не меняется.
- Room lifecycle: две тестовые identities → create/invite/join/intents/pool/votes/match/ticket. Это service-level E2E, не HTTP/auth/MAX UX.
- Два independent worker loops с разными DB pools и настоящей PostgreSQL advisory lock: busy skip → следующий scheduled tick → successful import обоих городов. Skip не считается succeeded. Не доказана fairness при бесконечно долгих/совпадающих production imports.
- Миграционный маршрут 25→30→31 и сохранность данных проверяются только на заново созданных disposable DB; не на production.

## Миграции и совместимость команды

Павел подтвердил production applied25/30. Номер31 проверен по fetched origin/dev, опубликованным/local branches и открытым PR; коллизий не найдено. Новая миграция: `backend/migrations/000031_multicity_city.sql`. Файл прежней локальной26 в конечный diff не входит. Не создаются пустые26–29; allow-missing и ручное изменение goose history запрещены.

31 добавляет только metadata Казани: UUID `3d3c83de-691b-510e-85e5-79685c4012ee`, Europe/Moscow, 55.78874/49.12214. Поддержан существующий совместимый город; semantic duplicate/different UUID/несовместимые имя,TZ,координаты вызывают transaction rollback. Down сохраняет город и FK, не удаляет события.

Shared migration test сохраняет проверки25/30 и требует31. Изменения SQL25/30, public-plan, event_submissions handlers, OpenAPI, frontend, ranking/analytics отсутствуют. Свежий dev уже фильтрует source_kind=generic в sourceconfig.Get/List; это решение используется. System source `user` не попадает в Generic scheduler/runner, не создаёт sync runs и не reconciles пользовательские события.

Mapping lock теперь учитывает legacy whitespace так же, как strings.TrimSpace. Создание canonical-дубликатов сериализовано транзакционной блокировкой; существующие неоднозначные ключи не допускаются к Run/Start до решения владельца. Система не переименовывает ключи и не правит существующие данные массово.

## Исправления защиты и конфигурации

`internal/testdbguard.Parse` проверяет до соединения URL и effective pgx config: literal127.0.0.1, port65428, точное disposable имя, sslmode=disable, explicit user; query overrides, keyword DSN, service/PG* env, fallback/DNS/socket targets отвергаются. Unit negative tests не открывают сеть. Реальные пароли/DSN не сохраняются в Git.

Москва/Казань имеют проверенные startup profiles; неправильные пары (`Kazan+msk/spb`, `Moscow+kzn`, `Kazan+Timepad Москва`) отвергаются до run/writes. Проверяются name/timezone из cities. Для будущего города требуется **EVENT_SYNC_CITY_PROFILES** — JSON array дополнительных объектов:

```json
[{"city_id":"<approved UUID>","name":"<name matching cities>","timezone":"<IANA timezone>","kudago_location":"<verified provider slug>","timepad_city_name":"<verified provider name>"}]
```

Это declaration интегратора, не автоматическая геолокация. UUID/name/location/Timepad-name collisions с известными профилями запрещены. City metadata должно предварительно существовать; arbitrary location без profile не принимается. Для Москвы сохраняется legacy default. Для второго города Timepad без token или explicit city name отключён; не имитируется пустой успешный Timepad sync.

EVENT_SOURCE_CITY_BINDINGS — JSON `generic:<source_key>`→cityUUID; одинаковое значение для backend и всех workers. Unbound sources остаются московскими. Bound source проверяет наличие city и совпадение source.Defaults.Timezone с DB до import. Смена binding не переносит существующие event/venue identities: persistence отвергает конфликт. Неизвестные/устаревшие sources не запускаются через профиль; список источников берётся из DB.

## Тесты

Фактическая матрица, первоначальные ошибки окружения и окончательные статусы: `MULTICITY_FIXES_REPORT.md`. Go1.26.10/PostgreSQL17.5. Build/vet, полный suite (36 пакетов, 1134 PASS test/subtest records, 5 отдельных SKIP), providers race и M01–M10 прошли. Frontend check на копии dev: 41 файл, 261 тест. Новые DB-тесты opt-in через MULTICITY_TEST_DATABASE_URL; legacy integration через TEST_DATABASE_URL. Обе должны указывать на **один проверенный disposable cluster**, не на общую/production DB. Имя основной fixture DB: multicity_fixture; migration tests создают только уникальные multicity_migration_* DB и очищают только созданные ими DB.

```powershell
go version
go build ./...
go vet ./...
go test -p 1 ./... -count=1
go test -race ./internal/providers -run '^TestMulticity' -count=1
go test ./migrations -run '^TestMulticityMigrationUpgradeMatrix$' -v -count=1
```

Не считайте PASS пакета доказательством DB coverage без проверки SKIP и env. Не запускайте старый тестовый кластер с прежней local26 для этой матрицы и не редактируйте его историю.

## Что ещё не проверено на реальном каталоге

- Число будущих/пригодных событий Москвы и Казани, свежесть последнего live sync, реальные доли missing fields/дублей/normalization failures.
- Качество реального top24, достаточность категорий/цен/дат для разных intents и наличие событий в нужных районах.
- Доступность и содержимое image/ticket URLs, корректность offer/цены и MAX WebView.
- Актуальный live KudaGo kzn response и разрешённый Timepad coverage. Документация provider slug не заменяет эти проверки.

Без этого второй город **NOT PRODUCTION-READY**. Fixture не копировать в release seed или production. Kazan.ready в UI Павла оставляется false до live+UX gates. Если разрешённый live-срез окажется недостаточным, сначала согласовать альтернативу.

## После merge: integration-only шаги

1. Интегратор сверяет actual applied migrations read-only; собирает единый release1…25,30,31. Применяет31 отдельным штатным migrate, не запуская sync одновременно со старым writer.
2. Останавливает старые ingestion binaries; mixed old/new writers не поддерживаются. Старый dev binary отвергает schema31 по CheckCurrent, новый отвергает30 — нужен согласованный artifact/schema rollout.
3. В обоих Compose передаёт backend EVENT_SOURCE_CITY_BINDINGS, worker EVENT_SYNC_CITY_ID/KUDAGO_LOCATION/TIMEPAD_CITY_NAME/EVENT_SOURCE_CITY_BINDINGS. Для дополнительных профилей — EVENT_SYNC_CITY_PROFILES. Compose здесь не менялись. KUDAGO_LOCATION уже есть, сохранить.
4. Сохраняет московский worker; добавляет отдельный второй worker с правильной application DB, KazanUUID/kzn/Казань. Общие provider locks/rate limits сохраняются. Разнести расписания и наблюдать skipped/succeeded/last-success/counts; второй worker включать после проверки данных.
5. Выполняет разрешённый provider/live-quality smoke и read-only Q1–Q4, проверяет сохранность Москвы. Затем Павел выполняет два аккаунта MAX/full UX, Сергей — smoke существующего CityID ranker. Только после этого включается UI города.
6. Rollback feature: выключить worker/доступность города без удаления catalog rows. Down31 сохраняет данные, но не заменяет согласованный schema/binary rollback. Down25/30 не предлагать как безопасный rollback пользовательских данных.

Production DB/sync/deploy и merge PR в этой задаче не разрешены и не выполняются. После создания PR работа останавливается.
