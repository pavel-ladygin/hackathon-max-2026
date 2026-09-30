# Дополнительные проверки и развёртывание

Подробности из прежнего README сохранены здесь. Основной локальный запуск и HTTP-сценарий описаны в [README](../README.md) и [LOCAL_HTTP_VERIFICATION](LOCAL_HTTP_VERIFICATION.md). Команды production выполняются только в целевом окружении с его приватной конфигурацией.

CI использует `-p 1`, потому что пакеты backend-тестов работают с общей базой. Сервис Compose `integration-tests` тоже запускает пакеты последовательно. E2E с настоящим каталогом включаются отдельно: `RUN_KUDAGO_REAL_E2E=1` требует импортированных подходящих событий KudaGo; `RUN_REAL_PROVIDER_ROOM_E2E=1` требует подходящих будущих событий и из KudaGo, и из Timepad. Для локальной проверки на fixture они не нужны.

`npm run check` запускает проверку политики поставщиков, typecheck, lint, unit-тесты и production-сборку. `npm run test:motion` — дополнительный существующий набор проверок анимации, вне стандартного CI. CI также задаёт `RUN_GENERIC_BROWSER_E2E=1` для `go test ./internal/eventsources -run '^TestGenericEventSourcesBrowserAgainstLiveHandler$' -count=1`.

Docker/release workflow дополнительно проверяет обе Compose-конфигурации, собирает образы backend/frontend, запускает smoke-стенд с миграциями, fixture, API и frontend, проверяет доступность API/frontend, выполняет `deploy/test-internal-nginx.sh`, сканирует образы через Trivy и репозиторий через Trufflehog. Публикация и deploy запускаются при push в `main` и при merge PR с меткой `deploy-dev` в ветку `dev` этого же репозитория; обычные push в `dev` запускают CI, но не публикуют и не развёртывают проект. Откат запускается вручную и использует полный commit SHA опубликованной версии.

`deploy/deploy.sh <40-character-commit-sha>` создаёт резервную копию PostgreSQL, применяет миграции, обновляет образы и запускает внешние smoke-проверки; при ошибке возвращает прежние образы. Скрипт развёртывает backend, frontend, `event-sync` и `daily-notifications`; восстановление постеров остаётся отдельной операцией. Обязательные секреты GitHub для production: `DEPLOY_HOST`, `DEPLOY_USER`, `DEPLOY_SSH_KEY`, `DEPLOY_HOST_KEY`; `YANDEX_MAPS_API_KEY` необязателен. Настройте production env в закрытом файле на сервере и разрешите Docker скачивать образы релиза из GHCR.

Если после прежнего релиза остался постоянно работающий recovery-контейнер, его удаление — отдельное ручное действие на хосте; обычный deploy-скрипт его не удаляет. Сначала проверьте соответствие Compose-проекта и сервиса, затем остановите и удалите только найденные контейнеры:

```sh
legacy_recovery_ids=$(docker ps -q --filter label=com.docker.compose.project=max-together-production --filter label=com.docker.compose.service=timepad-image-recovery)
if [ -n "$legacy_recovery_ids" ]; then
  docker stop $legacy_recovery_ids
  docker rm $legacy_recovery_ids
fi
```

