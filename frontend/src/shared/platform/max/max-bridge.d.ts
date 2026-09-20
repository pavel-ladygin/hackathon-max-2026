export {};

declare global {
  interface MaxWebAppInitDataUnsafe {
    start_param?: string;
    [key: string]: unknown;
  }

  interface MaxWebApp {
    /** Signed, URL-encoded WebAppData. Must be verified by the server. */
    readonly initData?: string;
    /** Parsed convenience data. Never use this object for authentication. */
    readonly initDataUnsafe?: MaxWebAppInitDataUnsafe;
    readonly platform?: string;
    readonly version?: string;
    readonly viewportHeight?: number;
    readonly viewportStableHeight?: number;
    ready?: () => void;
    getViewportSize?: () => Promise<{ height: string; width: string }>;
    shareContent?: (params: { text?: string; link?: string }) => void | Promise<void>;
    shareMaxContent?: (params: { text?: string; link?: string }) => void | Promise<void>;
    openLink?: (url: string) => void | Promise<void>;
    openMaxLink?: (url: string) => void | Promise<void>;
    enableClosingConfirmation?: () => void | Promise<void>;
    disableClosingConfirmation?: () => void | Promise<void>;
  }

  interface Window {
    WebApp?: MaxWebApp;
  }
}
