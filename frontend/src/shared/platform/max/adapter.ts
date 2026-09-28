import providerPolicy from "../../api/generated/provider-policy.json";
import type {
  InviteSharePayload,
  MaxPlatformAdapter,
  MaxPlatformName,
  MaxPlatformSnapshot,
  MaxViewport,
  GeoLocationDiagnostics,
  GeoLocationResult,
} from "./types";

const MAX_HOST = "max.ru";
const getWebApp = (): MaxWebApp | undefined => (typeof window === "undefined" ? undefined : window.WebApp);

const now = (): number => (typeof performance !== "undefined" ? performance.now() : Date.now());

const getGeolocationErrorCode = (error: unknown): number | null => {
  if (typeof error !== "object" || error === null || !("code" in error)) return null;
  return typeof error.code === "number" ? error.code : null;
};

const getErrorMessage = (error: unknown): string | null => {
  if (error instanceof Error) return error.message;
  if (typeof error === "object" && error !== null && "message" in error && typeof error.message === "string") {
    return error.message;
  }
  return null;
};

const getLocationPermissionState = async (): Promise<PermissionState | null> => {
  try {
    if (typeof navigator === "undefined" || !navigator.permissions?.query) return null;
    const permissionQuery = navigator.permissions.query({ name: "geolocation" as PermissionName });
    const timeout = new Promise<null>((resolve) => globalThis.setTimeout(() => resolve(null), 100));
    return (await Promise.race([permissionQuery.then(({ state }) => state), timeout])) as PermissionState | null;
  } catch {
    return null;
  }
};

const reportLocationFailure = (reason: string, diagnostics: object): void => {
  try {
    console.warn("Geolocation attempt failed", { reason, ...diagnostics });
  } catch {
    // Logging is best-effort and must not affect the location flow.
  }
};

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

const isIpAddress = (hostname: string): boolean => {
  const normalized = hostname.toLowerCase();
  if (normalized.startsWith("[") && normalized.endsWith("]")) return true;
  if (normalized.includes(":")) return true;
  const octets = normalized.split(".");
  return octets.length === 4 && octets.every((octet) => /^(?:0|[1-9]\d{0,2})$/.test(octet) && Number(octet) <= 255);
};

const validExternalUrl = (value: string): URL | null => {
  const url = safeUrl(value);
  if (!url || url.username || url.password || url.port || isIpAddress(url.hostname)) return null;
  return url;
};

const configuredTicketProviders = (): string[] =>
  [...providerPolicy.tickets, ...(import.meta.env.VITE_TICKET_PROVIDER_ALLOWLIST ?? "").split(",")].join(",")
    .split(",")
    .map((rule) => rule.trim().toLowerCase().replace(/^\.+/, "."))
    .filter(Boolean);

const matchesHostRule = (hostname: string, rule: string): boolean => {
  if (rule.startsWith("*.")) {
    const suffix = rule.slice(1);
    return hostname.endsWith(suffix) && hostname.length > suffix.length;
  }
  return hostname === rule;
};

export const isAllowedTicketUrl = (value: string, rules = configuredTicketProviders()): boolean => {
  const url = validExternalUrl(value);
  return Boolean(
    url &&
      rules
        .map((rule) => rule.trim().toLowerCase())
        .filter(Boolean)
        .some((rule) => matchesHostRule(url.hostname.toLowerCase(), rule)),
  );
};

const isControlledTicketPath = (value: string): boolean =>
  /^\/api\/v1\/events\/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\/ticket$/i.test(value);

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

  ready(): void {
    getWebApp()?.ready?.();
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
    } catch {
      return false;
    }
    return false;
  }

  async openMaxLink(url: string): Promise<boolean> {
    const parsed = validExternalUrl(url);
    if (!parsed || parsed.hostname.toLowerCase() !== MAX_HOST) return false;
    try {
      if (getWebApp()?.openMaxLink) {
        await getWebApp()!.openMaxLink!(parsed.toString());
        return true;
      }
    } catch {
      return false;
    }
    if (getWebApp()?.openLink) {
      try {
        await getWebApp()!.openLink!(parsed.toString());
        return true;
      } catch {
        return false;
      }
    }
    if (typeof window === "undefined") return false;
    const opened = window.open(parsed.toString(), "_blank", "noopener,noreferrer");
    return opened !== null;
  }

  async openTicketLink(url: string): Promise<boolean> {
    if (isControlledTicketPath(url)) {
      if (typeof window === "undefined") return false;
      const destination = `${window.location.origin}${url}`;
      try {
        if (getWebApp()?.openLink) {
          await getWebApp()!.openLink!(destination);
          return true;
        }
      } catch {
        return false;
      }
      return window.open(destination, "_blank", "noopener,noreferrer") !== null;
    }
    if (!isAllowedTicketUrl(url)) return false;
    const parsed = validExternalUrl(url);
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

  async requestLocation(): Promise<GeoLocationResult> {
    const startedAt = now();
    const geolocation = typeof navigator === "undefined" ? undefined : navigator.geolocation;
    if (!geolocation) {
      return { ok: false, reason: "unsupported", diagnostics: this.locationDiagnostics(startedAt, null, null, 0) };
    }

    const attempt = (options: PositionOptions): Promise<GeolocationPosition | GeolocationPositionError> =>
      new Promise((resolve, reject) => {
        try {
          geolocation.getCurrentPosition(
            (position) => resolve(position),
            (error) => resolve(error),
            options,
          );
        } catch (error) {
          reject(error);
        }
      });

    let result: GeolocationPosition | GeolocationPositionError | null = null;
    let thrown: unknown;
    for (let attemptNumber = 1; attemptNumber <= 2; attemptNumber += 1) {
      try {
        result = await attempt(
          attemptNumber === 1
            ? { enableHighAccuracy: false, timeout: 15_000, maximumAge: 300_000 }
            : { enableHighAccuracy: true, timeout: 5_000, maximumAge: 0 },
        );
      } catch (error) {
        thrown = error;
      }

      if (result && "coords" in result) {
        const { coords } = result;
        return {
          ok: true,
          position: {
            lat: coords.latitude,
            lng: coords.longitude,
            accuracyM: Number.isFinite(coords.accuracy) ? coords.accuracy : null,
          },
        };
      }

      const error = result && "code" in result ? result : thrown;
      const code = getGeolocationErrorCode(error);
      const message = getErrorMessage(error);
      const reason = code === 1 ? "permission_denied" : code === 3 ? "timeout" : "position_unavailable";
      const diagnostics = this.locationDiagnostics(startedAt, code, message, attemptNumber);
      const permissionState = await getLocationPermissionState();
      diagnostics.permissionState = permissionState;
      reportLocationFailure(reason, diagnostics);

      if (reason === "permission_denied" || attemptNumber === 2) {
        return { ok: false, reason, diagnostics };
      }
      result = null;
      thrown = undefined;
    }
    return { ok: false, reason: "position_unavailable", diagnostics: this.locationDiagnostics(startedAt, null, null, 2) };
  }

  private locationDiagnostics(
    startedAt: number,
    code: number | null,
    message: string | null,
    attempt: number,
  ): GeoLocationDiagnostics {
    let environment: "browser" | "max" = "browser";
    let platform: MaxPlatformName | null = null;
    try {
      environment = this.environment;
      platform = this.getPlatform();
    } catch {
      // Diagnostic collection must never turn a location failure into a rejection.
    }
    return {
      code,
      message,
      elapsedMs: Math.max(0, Math.round(now() - startedAt)),
      environment,
      platform,
      secureContext: typeof isSecureContext === "boolean" ? isSecureContext : null,
      attempt,
    };
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
