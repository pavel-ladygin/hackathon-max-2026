import { afterEach, describe, expect, it, vi } from "vitest";
import { isAllowedTicketUrl, MaxBridgeAdapterImpl } from "./adapter";

const bridge = (overrides: Partial<MaxWebApp> = {}): MaxWebApp => ({
  openLink: vi.fn(),
  openMaxLink: vi.fn(),
  ...overrides,
});

afterEach(() => {
  delete window.WebApp;
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: undefined });
  Object.defineProperty(navigator, "geolocation", { configurable: true, value: undefined });
  vi.restoreAllMocks();
  vi.unstubAllEnvs();
});

describe("isAllowedTicketUrl", () => {
  it("accepts exact and wildcard host rules with DNS label boundaries", () => {
    expect(isAllowedTicketUrl("https://tickets.example.ru/show/1", ["tickets.example.ru"])).toBe(true);
    expect(isAllowedTicketUrl("https://tickets.example.ru/show/1", ["*.example.ru"])).toBe(true);
    expect(isAllowedTicketUrl("https://example.ru/show/1", ["*.example.ru"])).toBe(false);
    expect(isAllowedTicketUrl("https://example.ru.attacker.com/show/1", ["*.ru"])).toBe(false);
    expect(isAllowedTicketUrl("https://tickets.example.com/show/1", ["*.ru"])).toBe(false);
  });

  it("accepts Unicode hostnames after URL normalization", () => {
    expect(isAllowedTicketUrl("https://билеты.рф/show/1", ["*.xn--p1ai"])).toBe(true);
    expect(isAllowedTicketUrl("https://tickets.example.ru/show/1", ["*.RU"])).toBe(true);
  });

  it("rejects unsafe URL forms", () => {
    const rules = ["*.ru"];
    expect(isAllowedTicketUrl("http://tickets.example.ru/show/1", rules)).toBe(false);
    expect(isAllowedTicketUrl("javascript:alert(1)", rules)).toBe(false);
    expect(isAllowedTicketUrl("data:text/html,hello", rules)).toBe(false);
    expect(isAllowedTicketUrl("https://user:pass@tickets.example.ru/show/1", rules)).toBe(false);
    expect(isAllowedTicketUrl("https://tickets.example.ru:8443/show/1", rules)).toBe(false);
    expect(isAllowedTicketUrl("https://127.0.0.1/show/1", rules)).toBe(false);
    expect(isAllowedTicketUrl("not a URL", rules)).toBe(false);
  });
});

describe("MaxBridgeAdapterImpl navigation", () => {
  it("does not report clipboard copying as successful invite sharing", async () => {
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText: vi.fn().mockResolvedValue(undefined) } });
    await expect(new MaxBridgeAdapterImpl().shareInvite({ link: "https://max.ru/startapp/token", text: "Join" })).resolves.toBe(false);
    expect(navigator.clipboard.writeText).not.toHaveBeenCalled();
  });

  it("opens an allowed ticket URL through the MAX bridge", async () => {
    vi.stubEnv("VITE_TICKET_PROVIDER_ALLOWLIST", "*.ru");
    const openLink = vi.fn().mockResolvedValue(undefined);
    window.WebApp = bridge({ openLink });

    await expect(new MaxBridgeAdapterImpl().openTicketLink("https://tickets.example.ru/order/1")).resolves.toBe(true);
    expect(openLink).toHaveBeenCalledWith("https://tickets.example.ru/order/1");
  });

  it("opens an allowed ticket URL in a browser when MAX is unavailable", async () => {
    vi.stubEnv("VITE_TICKET_PROVIDER_ALLOWLIST", "*.ru");
    const open = vi.spyOn(window, "open").mockReturnValue({} as Window);

    await expect(new MaxBridgeAdapterImpl().openTicketLink("https://tickets.example.ru/order/1")).resolves.toBe(true);
    expect(open).toHaveBeenCalledWith("https://tickets.example.ru/order/1", "_blank", "noopener,noreferrer");
  });

  it("does not pass rejected ticket URLs to the bridge or window", async () => {
    vi.stubEnv("VITE_TICKET_PROVIDER_ALLOWLIST", "*.ru");
    const openLink = vi.fn();
    window.WebApp = bridge({ openLink });
    const open = vi.spyOn(window, "open");

    await expect(new MaxBridgeAdapterImpl().openTicketLink("https://tickets.example.com/order/1")).resolves.toBe(false);
    expect(openLink).not.toHaveBeenCalled();
    expect(open).not.toHaveBeenCalled();
  });

  it("opens only exact max.ru links and rejects every other host without fallback", async () => {
    const openMaxLink = vi.fn().mockResolvedValue(undefined);
    const openLink = vi.fn();
    window.WebApp = bridge({ openMaxLink, openLink });
    const open = vi.spyOn(window, "open");
    const adapter = new MaxBridgeAdapterImpl();

    await expect(adapter.openMaxLink("https://max.ru/startapp/token")).resolves.toBe(true);
    expect(openMaxLink).toHaveBeenCalledWith("https://max.ru/startapp/token");
    await expect(adapter.openMaxLink("https://max.ru.attacker.com/startapp/token")).resolves.toBe(false);
    await expect(adapter.openMaxLink("https://example.ru/startapp/token")).resolves.toBe(false);
    expect(openLink).not.toHaveBeenCalled();
    expect(open).not.toHaveBeenCalled();
  });

  it("does not fall back to arbitrary navigation when max.ru bridge method is absent", async () => {
    const open = vi.spyOn(window, "open").mockReturnValue({} as Window);
    window.WebApp = bridge({ openMaxLink: undefined, openLink: undefined });

    await expect(new MaxBridgeAdapterImpl().openMaxLink("https://max.ru/startapp/token")).resolves.toBe(true);
    expect(open).toHaveBeenCalledWith("https://max.ru/startapp/token", "_blank", "noopener,noreferrer");
  });
});

describe("MaxBridgeAdapterImpl location", () => {
  const setGeolocation = (getCurrentPosition: PositionCallback | ((success: PositionCallback, error?: PositionErrorCallback | null, options?: PositionOptions) => void)) => {
    Object.defineProperty(navigator, "geolocation", { configurable: true, value: { getCurrentPosition } });
  };

  it.each([[1, "permission_denied"], [2, "position_unavailable"], [3, "timeout"]] as const)(
    "preserves the browser location error code %s",
    async (code, reason) => {
      setGeolocation((_success, error) => error?.({ code, message: "location unavailable", PERMISSION_DENIED: 1, POSITION_UNAVAILABLE: 2, TIMEOUT: 3 }));
      await expect(new MaxBridgeAdapterImpl().requestLocation()).resolves.toEqual({ ok: false, reason });
    },
  );

  it("returns coordinates on success without logging them", async () => {
    const getCurrentPosition = vi.fn((success: PositionCallback, ...rest: [PositionErrorCallback | null | undefined, PositionOptions | undefined]) => {
      void rest;
      success({
        coords: { latitude: 55.75, longitude: 37.61, accuracy: 50, altitude: null, altitudeAccuracy: null, heading: null, speed: null, toJSON: () => ({}) },
        timestamp: Date.now(), toJSON: () => ({}),
      });
    });
    setGeolocation(getCurrentPosition);
    await expect(new MaxBridgeAdapterImpl().requestLocation()).resolves.toEqual({ ok: true, position: { lat: 55.75, lng: 37.61, accuracyM: 50 } });
    expect(getCurrentPosition.mock.calls[0]?.[2]).toMatchObject({ timeout: 15_000, maximumAge: 300_000 });
  });
});
