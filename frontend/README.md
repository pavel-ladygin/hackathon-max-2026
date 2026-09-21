# Вместе — frontend

Мобильное приложение внутри MAX для персональной афиши и приватного совместного выбора события. Авторизация MAX, bootstrap и предпочтения работают через backend. Пока каталог событий работает на локальном адаптере discovery, чтобы интерфейс можно было отлаживать без готового каталожного API. Комнаты временно отключены.

## Стек

- React 19, TypeScript, Vite;
- React Router;
- TanStack Query для server state, polling и инвалидации;
- React Hook Form + Zod для форм и валидации;
- локальный discovery-адаптер для демо-афиши;
- MAX UI и адаптер MAX Bridge;
- Motion для переходов и экрана совпадения;
- Leaflet + OpenStreetMap для отложенно загружаемой карты;
- CSS Modules и дизайн-токены;
- Vitest + Testing Library, Playwright-конфигурация для e2e.

## Запуск

```bash
npm install
cp .env.example .env.local
npm run dev
```

Приложение использует реальную авторизацию и discovery API. Вне MAX реальный bootstrap ожидаемо отклонит пустой `initData`, поэтому полноценную ручную проверку выполняйте из клиента MAX.

Онбординг, афиша, поиск, карточки событий, избранное, behavior, билеты и комнаты используют backend через `VITE_API_BASE_URL`.

## Режимы API

```dotenv
VITE_API_BASE_URL=/api/v1
```

- Авторизация, bootstrap, preferences и health всегда отправляются в `VITE_API_BASE_URL`.

Между источниками нет скрытого fallback: ошибка реального backend не подменяется мок-ответом. Локальный discovery mock хранит избранное и дедупликацию behavior в `localStorage`.

## OpenAPI

`openapi/openapi.yaml` — источник транспортного контракта. Типы генерируются командой:

```bash
npm run generate:api
```

Результат находится в `src/shared/api/generated/schema.ts`. Ручные модели используются только как UI/domain boundary; URL, DTO и коды ошибок должны оставаться совместимыми с generated contract.

## Архитектура

```text
src/
  app/                 маршрутизация, providers, bootstrap
  features/            запросы и UI отдельных возможностей
  pages/               экранные сценарии
  shared/
    api/                DTO, мапперы, HTTP-клиент
    platform/max/       адаптер MAX Bridge с browser fallback
    ui/                 базовые компоненты и дизайн-токены
  mocks/                discovery fixtures and test-only mock helpers
```

DTO остаются в `snake_case` на границе API, а мапперы преобразуют их в удобные для UI модели. Компоненты не знают, выбран ли HTTP или локальный discovery-адаптер. Токен bootstrap хранится только в памяти.

## Проверки

```bash
npm run typecheck
npm run lint
npm test
npm run build
```

Или одной командой:

```bash
npm run check
```

Unit-тесты discovery-адаптера покрывают афишу, поиск, детали, избранное, behavior и ticket-click. Room API не используется в активном приложении.

Полный browser flow для desktop и mobile:

```bash
npx playwright install chromium
npm run test:e2e
```

Карта использует публичные tiles OpenStreetMap и при отсутствии сети не блокирует списковый режим.

## Что подключать backend-команде

1. Реализовать discovery endpoints, описанные в `shared/api/client.ts`.
2. Согласовать DTO из `shared/api/types.ts` с итоговой OpenAPI-схемой.
3. Задать URL gateway через `VITE_API_BASE_URL` (по умолчанию `/api/v1`).
4. После реализации join, snapshot, intent, events и vote вернуть room-навигацию отдельным этапом.
