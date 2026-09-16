# Вместе — frontend

Мобильное приложение внутри MAX для персональной афиши и приватного совместного выбора события. Текущая версия полностью работает без backend: HTTP-контракт эмулируется через MSW, а состояние комнаты синхронизируется между двумя вкладками одного браузера.

## Стек

- React 19, TypeScript, Vite;
- React Router;
- TanStack Query для server state, polling и инвалидации;
- React Hook Form + Zod для форм и валидации;
- MSW для API-моков;
- MAX UI и адаптер MAX Bridge;
- Motion для переходов и экрана совпадения;
- CSS Modules и дизайн-токены;
- Vitest + Testing Library, Playwright-конфигурация для e2e.

## Запуск

```bash
npm install
cp .env.example .env.local
npm run dev
```

По умолчанию используется mock API. Приложение доступно на адресе, который напечатает Vite.

Для проверки двух пользователей:

1. Откройте `/?resetMock=1&mockUser=ivan`, завершите онбординг и создайте комнату.
2. Откройте показанную ссылку-приглашение во второй вкладке — в ней уже будет `mockUser=anna`.
3. В обеих вкладках сохраните пожелания и поставьте лайк одному событию.
4. Обе вкладки перейдут на экран «Это мэтч!».

Mock-сценарий рассчитан на вкладки одного браузера: он использует общий `localStorage` и `BroadcastChannel`. Для разных устройств нужен backend.

## Режимы API

```dotenv
VITE_API_MODE=mock
VITE_API_BASE_URL=/api/v1
VITE_ALLOW_BROWSER_PREVIEW=true
```

- `VITE_API_MODE=mock` запускает MSW.
- `VITE_API_MODE=http` отключает MSW и отправляет запросы в `VITE_API_BASE_URL`.
- `VITE_ALLOW_BROWSER_PREVIEW=false` показывает вне MAX отдельный экран «Откройте в MAX».

Между режимами нет скрытого fallback: ошибка реального API не подменяется мок-ответом.

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
  mocks/                MSW handlers, fixtures, two-tab state
```

DTO остаются в `snake_case` на границе API, а мапперы преобразуют их в удобные для UI модели. Компоненты не знают, работает приложение через MSW или настоящий HTTP. Токен bootstrap хранится только в памяти.

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

Unit-тест mock state покрывает полный переход: два участника → приватные интенты → независимые лайки → совпадение.

## Что подключать backend-команде

1. Реализовать endpoints, уже описанные в `shared/api/client.ts`.
2. Согласовать DTO из `shared/api/types.ts` с итоговой OpenAPI-схемой.
3. Задать `VITE_API_MODE=http` и URL gateway.
4. Оставить текущий polling комнаты на 1 секунду либо заменить transport внутри feature-слоя на SSE/WebSocket, не меняя страницы.
