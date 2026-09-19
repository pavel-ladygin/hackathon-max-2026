import type {
  InviteSharePayload,
  MaxPlatformAdapter,
  MaxPlatformName,
  MaxPlatformSnapshot,
  MaxViewport,
} from "./types";

const MAX_URL = /^https:\/\/max\.ru(?:\/|$)/i;
const getWebApp = (): MaxWebApp | undefined => (typeof window === "undefined" ? undefined : window.WebApp);

const asPositiveNumber = (value: string | number | undefined): number | null => {
  const number = typeof value === "number" ? value : Number(value);
  return Number.isFinite(number) && number > 0 ? number : null;
};

const safeUrl = (value: string): URL | null => {
  try {
    const url = new URL(value);
    return url.protocol === "https:" ? url : null;
  } catch {
    return null;
  }
};

export class MaxBridgeAdapterImpl implements MaxPlatformAdapter {
  get environment(): "browser" | "max" {
    return getWebApp() ? "max" : "browser";
  }

  get isMax(): boolean {
    return Boolean(getWebApp());
  }

  getInitData(): string {
    return getWebApp()?.initData ?? "";
  }

  getStartParam(): string | null {
    const webApp = getWebApp();
    const hint = webApp?.initDataUnsafe?.start_param;
    if (typeof hint === "string" && hint.length > 0) return hint;
    if (!webApp && typeof window !== "undefined") {
      const params = new URLSearchParams(window.location.search);
      return params.get("startapp") ?? params.get("start_param");
    }
    return null;
  }

  getPlatform(): MaxPlatformName | null {
    const value = getWebApp()?.platform;
    return typeof value === "string" && value.length > 0 ? (value as MaxPlatformName) : null;
  }

  getVersion(): string | null {
    const value = getWebApp()?.version;
    return typeof value === "string" && value.length > 0 ? value : null;
  }

  async getViewport(): Promise<MaxViewport> {
    const bridgeViewport = await getWebApp()?.getViewportSize?.();
    const bridgeWidth = asPositiveNumber(bridgeViewport?.width);
    const bridgeHeight = asPositiveNumber(bridgeViewport?.height);
    if (bridgeWidth && bridgeHeight) return { width: bridgeWidth, height: bridgeHeight };

    const viewport = window.visualViewport;
    return {
      width: Math.round(viewport?.width ?? window.innerWidth),
      height: Math.round(viewport?.height ?? window.innerHeight),
    };
  }

  async shareInvite(payload: InviteSharePayload): Promise<boolean> {
    if (!safeUrl(payload.link)) return false;
    const params = { text: payload.text, link: payload.link };
    const webApp = getWebApp();
    try {
      if (webApp?.shareMaxContent) {
        await webApp.shareMaxContent(params);
        return true;
      }
      if (webApp?.shareContent) {
        await webApp.shareContent(params);
        return true;
      }
      if (typeof navigator !== "undefined" && navigator.share) {
        await navigator.share({ title: payload.text, text: payload.text, url: payload.link });
        return true;
      }
      if (typeof navigator !== "undefined" && navigator.clipboard) {
        await navigator.clipboard.writeText(payload.link);
        return true;
      }
    } catch {
      return false;
    }
    return false;
  }

  async openMaxLink(url: string): Promise<boolean> {
    const parsed = safeUrl(url);
    if (!parsed || !MAX_URL.test(parsed.toString())) return this.openExternalLink(url);
    try {
      if (getWebApp()?.openMaxLink) {
        await getWebApp()!.openMaxLink!(parsed.toString());
        return true;
      }
    } catch {
      return false;
    }
    return this.openExternalLink(parsed.toString());
  }

  async openExternalLink(url: string): Promise<boolean> {
    const parsed = safeUrl(url);
    if (!parsed) return false;
    try {
      if (getWebApp()?.openLink) {
        await getWebApp()!.openLink!(parsed.toString());
        return true;
      }
    } catch {
      return false;
    }
    if (typeof window === "undefined") return false;
    const opened = window.open(parsed.toString(), "_blank", "noopener,noreferrer");
    return opened !== null;
  }

  async requestLocation(): Promise<{ lat: number; lng: number; accuracyM: number | null } | null> {
    if (typeof navigator === "undefined" || !navigator.geolocation) return null;
    return new Promise((resolve) => {
      navigator.geolocation.getCurrentPosition(
        ({ coords }) => resolve({ lat: coords.latitude, lng: coords.longitude, accuracyM: Number.isFinite(coords.accuracy) ? coords.accuracy : null }),
        () => resolve(null),
        { enableHighAccuracy: false, timeout: 10_000, maximumAge: 300_000 },
      );
    });
  }

  async copyText(value: string): Promise<boolean> {
    try {
      if (!navigator.clipboard) return false;
      await navigator.clipboard.writeText(value);
      return true;
    } catch {
      return false;
    }
  }

  setClosingConfirmation(enabled: boolean): void {
    if (typeof window === "undefined") return;
    const webApp = getWebApp();
    const method = enabled ? webApp?.enableClosingConfirmation : webApp?.disableClosingConfirmation;
    if (method) {
      void method.call(webApp);
      return;
    }
    if (enabled) window.addEventListener("beforeunload", preventUnload);
    else window.removeEventListener("beforeunload", preventUnload);
  }

  snapshot(): MaxPlatformSnapshot {
    return {
      environment: this.environment,
      isMax: this.isMax,
      initData: this.getInitData(),
      startParam: this.getStartParam(),
      platform: this.getPlatform(),
      version: this.getVersion(),
      viewport: null,
    };
  }
}

const preventUnload = (event: BeforeUnloadEvent): void => {
  event.preventDefault();
  event.returnValue = "";
};

export const maxPlatform: MaxPlatformAdapter = new MaxBridgeAdapterImpl();
