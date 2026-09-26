/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_API_BASE_URL?: string;
  readonly VITE_MAX_APP_URL?: string;
  readonly VITE_YANDEX_MAPS_API_KEY?: string;
  readonly VITE_TICKET_PROVIDER_ALLOWLIST?: string;
  readonly VITE_APP_VERSION?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
