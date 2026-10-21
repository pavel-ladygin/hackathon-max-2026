# Kazan PoC — isolated fixture evidence

Актуализировано 21.10.2026 после переноса на dev1a7a8687b5a2e32b172d01d43c930599b902483b.
Go1.26.10, PostgreSQL17.5. **Synthetic fixture, not live Kazan coverage.**
Первоначальный опыт20.10 остаётся историческим; текущая проверка использует новую
БД с миграциями1…25,30,31. Старый кластер с local26 не переделывался.

## Стенд и данные

Новый disposable cluster вне продукта: codex_local/multicity-fix-20261021/pgdata,
literal127.0.0.1:65428, multicity_fixture. DSN/пароли не хранятся в Git.
Pre-connect guard проверяет effective pgx target, service/query/env overrides
запрещены. Production/системный PostgreSQL и внешние API не использовались.
Статусы полного актуального прогона — MULTICITY_FIXES_REPORT.md.

`backend/cmd/catalog-quality/kazan_poc_test.go` создаёт localhost HTTP server
с четырьмя provider-shaped карточками для каждого из `msk` и `kzn`.
Title/description явно содержат SYNTHETIC/fixture. Набор не добавлен в seed
или миграцию. Запись проходит реальный KudaGo importer и repository.

Поля: четыре отдельных venue/provider event IDs на город; category concerts;
будущая ближайшая суббота, 18:00 Europe/Moscow, длительность час;
координаты соответствующего города; provider explicit free=true → price=0;
card poster и синтетические HTTPS ticket URLs kudago.com.
URL изображений/билетов не скачиваются и не доказывают реальную доступность.
В тестовой БД `is_demo=false`, чтобы пройти обычный production ingestion path;
изоляция достигается отдельной БД и строгим opt-in, а не выдачей fixture за live.

Время берётся один раз для test cutoff/PoolBuilder, следующая суббота вычисляется
динамически, чтобы тест не протухал. На указанном запуске это
2026-10-24 18:00+03:00. Реальные provider timestamps преобразуются без ручного
сдвига, тест проверяет абсолютное время и timezone.

## Воспроизведение

Из `backend`, только при уже подготовленном изолированном стенде:

```powershell
$env:GOCACHE = 'C:\study\ХАКАТОН МАКС осень 2026\codex_local\cache\go-build-multicity'
# MULTICITY_TEST_DATABASE_URL must already be supplied by the disposable
# fixture provisioner: literal 127.0.0.1, port 65428, database multicity_fixture.
# It must contain only sslmode=disable as its query; never copy production env.
go test ./cmd/catalog-quality -run '^TestKazanFixturePoC$' -count=1 -v
go test ./internal/providers -run '^TestMulticity' -count=1
go test ./migrations -run '^TestKazanMetadataMigrationIsIdempotentAndRejectsConflicts$' -count=1
```

Не подставлять production DSN. Без переменной новые DB-тесты пропускаются;
неподходящие host/port/database отвергаются. Записи fixture сохраняются
в изолированном кластере, broad cleanup/reset отсутствует.

## Наблюдаемый результат

Исторический контрольный запуск 20.10; повторный PASS 21.10 зафиксирован
в backend-tests-final.jsonl / MULTICITY_FIXES_REPORT.md:

```text
FIXTURE ONLY as_of=2026-10-20T14:37:48Z
city=3d3c83de-691b-510e-85e5-79685c4012ee
imported=4 replay_updated=4 candidates=4 isSmall=false
hard_constraint_violations=0 Moscow_snapshot_unchanged=true
room_lifecycle=passed
PASS TestKazanFixturePoC (0.31s)
```

| Проверка | Результат |
|---|---|
| Казань в cities | UUID и метаданные из 000031 |
| События Казани | 4, каждое venue.city_id=Казань |
| Нормализация | category, timezone, start, price=0, ticket, images проверены |
| Повторный импорт | 0 новых, 4 обновлены, те же IDs |
| Два независимых intent | разные user UUID, одна дата, вечер, budget 300000 |
| Неизменённый PoolBuilder | 4 кандидата, isSmall=false, не zero |
| Hard constraints | 0 нарушений city/date/budget в контролируемом наборе |
| Москва до/после Kazan import | snapshots полностью равны |
| Lifecycle | Create → invite → Join → own intents ×2 → GetEvents ×2 → votes ×2 → match → ticket click |
| Match/ticket persistence | ровно 1 room_match и 1 ticket_click для тестовой комнаты |
| Изменения кода rooms/vote/match/recommendations | **0** |

Lifecycle использует настоящий PoolBuilder и существующие services.
Прямые SQL-записи в room_matches, room pool или votes отсутствуют: тест
вставляет только независимых fixture users, остальное создают сервисы.
Локальные principals заданы тестом; auth bootstrap, middleware/router,
HTTP sessions, браузер/MAX и внешняя страница билета не проверяются.
Следовательно, это **service-level fixture E2E**, а не HTTP/live/MAX E2E.

До расширения теста лог содержал `room_writes=0` (только PoolBuilder).
Итоговый lifecycle закономерно создаёт комнату через штатный сервис;
нулевым остаётся число изменений продуктового room-кода.

Межгородские DB-тесты дополнительно проверяют конфликты event/shared venue,
гонку первой вставки, run provider/city mismatch, idempotency финализации,
failure/cancel/partial/upsert-only и изоляцию stale reconciliation.
Race detector на `TestMulticity*` — PASS.
Существующие московские/provider/regression suites также PASS (см. HANDOFF).

## Что не доказано

- Реальное число будущих событий Казани, last live success и свежесть: **N/D**.
- Валидность live ticket offers, загрузка постеров и правильность WebView: **N/D**.
- Работа с разрешённым Timepad token в Казани: не проверена.
- HTTP/auth/MAX путь двух реальных аккаунтов: integration gate после merge.
- Отсутствие semantic duplicate в общей БД: требуется owner-operated Q4
  из `catalog-readonly.sql`; migration обязана остановиться при конфликте.

Казань сохранена как предпочтительный второй город. Нет оснований объявлять
её live-ready или менять город без данных о покрытии. Если разрешённый live
срез позже окажется недостаточным, согласовать альтернативу с владельцем.
