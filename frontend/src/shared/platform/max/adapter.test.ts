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
  const providerRules = ["kudago.com", "*.kudago.com", "timepad.ru", "*.timepad.ru"];

  it("accepts the two ticket providers and rejects unrelated Russian hosts", () => {
    expect(isAllowedTicketUrl("https://kudago.com/msk/event/1", providerRules)).toBe(true);
    expect(isAllowedTicketUrl("https://www.kudago.com/msk/event/1", providerRules)).toBe(true);
    expect(isAllowedTicketUrl("https://timepad.ru/event/1", providerRules)).toBe(true);
    expect(isAllowedTicketUrl("https://club.timepad.ru/event/1", providerRules)).toBe(true);
    expect(isAllowedTicketUrl("https://tickets.example.ru/event/1", providerRules)).toBe(false);
    expect(isAllowedTicketUrl("https://timepad.ru.attacker.example/event/1", providerRules)).toBe(false);
    expect(isAllowedTicketUrl("http://timepad.ru/event/1", providerRules)).toBe(false);
  });

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
    vi.stubEnv("VITE_TICKET_PROVIDER_ALLOWLIST", "kudago.com,*.kudago.com,timepad.ru,*.timepad.ru");
    const openLink = vi.fn().mockResolvedValue(undefined);
    window.WebApp = bridge({ openLink });

    await expect(new MaxBridgeAdapterImpl().openTicketLink("https://timepad.ru/order/1")).resolves.toBe(true);
    expect(openLink).toHaveBeenCalledWith("https://timepad.ru/order/1");
  });

  it("opens an allowed ticket URL in a browser when MAX is unavailable", async () => {
    vi.stubEnv("VITE_TICKET_PROVIDER_ALLOWLIST", "kudago.com,*.kudago.com,timepad.ru,*.timepad.ru");
    const open = vi.spyOn(window, "open").mockReturnValue({} as Window);

    await expect(new MaxBridgeAdapterImpl().openTicketLink("https://kudago.com/order/1")).resolves.toBe(true);
    expect(open).toHaveBeenCalledWith("https://kudago.com/order/1", "_blank", "noopener,noreferrer");
  });

  it("does not pass rejected ticket URLs to the bridge or window", async () => {
    vi.stubEnv("VITE_TICKET_PROVIDER_ALLOWLIST", "kudago.com,*.kudago.com,timepad.ru,*.timepad.ru");
    const openLink = vi.fn();
    window.WebApp = bridge({ openLink });
    const open = vi.spyOn(window, "open");

    await expect(new MaxBridgeAdapterImpl().openTicketLink("https://tickets.example.ru/order/1")).resolves.toBe(false);
    expect(openLink).not.toHaveBeenCalled();
    expect(open).not.toHaveBeenCalled();
  });

  it("opens only the exact same-origin Generic ticket route", async () => {
    const eventId = "550e8400-e29b-41d4-a716-446655440000";
    const open = vi.spyOn(window, "open").mockReturnValue({} as Window);
    const adapter = new MaxBridgeAdapterImpl();

    await expect(adapter.openTicketLink(`/api/v1/events/${eventId}/ticket`)).resolves.toBe(true);
    expect(open).toHaveBeenCalledWith(`${window.location.origin}/api/v1/events/${eventId}/ticket`, "_blank", "noopener,noreferrer");
    for (const value of [
      `https://attacker.example/api/v1/events/${eventId}/ticket`,
      `/api/v1/events/${eventId}/ticket?url=https://attacker.example`,
      `/api/v1/events/${eventId}/ticket/extra`,
      "/api/v1/events/not-a-uuid/ticket",
    ]) await expect(adapter.openTicketLink(value)).resolves.toBe(false);
    expect(open).toHaveBeenCalledTimes(1);
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
    "maps browser location error code %s and retains diagnostics",
    async (code, reason) => {
      const getCurrentPosition = vi.fn((_success: PositionCallback, error?: PositionErrorCallback | null) => {
        error?.({ code, message: "location unavailable", PERMISSION_DENIED: 1, POSITION_UNAVAILABLE: 2, TIMEOUT: 3 });
      });
      setGeolocation(getCurrentPosition);
      const result = await new MaxBridgeAdapterImpl().requestLocation();
      expect(result).toMatchObject({
        ok: false,
        reason,
        diagnostics: { code, message: "location unavailable", environment: "browser", attempt: code === 1 ? 1 : 2 },
      });
      expect(getCurrentPosition).toHaveBeenCalledTimes(code === 1 ? 1 : 2);
    },
  );

  it("converts synchronous API exceptions into diagnostic failures", async () => {
    setGeolocation(() => {
      throw new Error("geolocation API unavailable");
    });
    const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);

    const result = await new MaxBridgeAdapterImpl().requestLocation();

    expect(result).toMatchObject({
      ok: false,
      reason: "position_unavailable",
      diagnostics: { code: null, message: "geolocation API unavailable", environment: "browser", attempt: 2 },
    });
    expect(warn).toHaveBeenCalledTimes(2);
    expect(JSON.stringify(warn.mock.calls)).not.toContain("55.75");
  });

  it("retries transient failures once with higher accuracy and a short timeout", async () => {
    const getCurrentPosition = vi.fn((success: PositionCallback, error?: PositionErrorCallback | null, ...options: [PositionOptions?]) => {
      void options;
      if (getCurrentPosition.mock.calls.length === 1) {
        error?.({ code: 2, message: "no signal", PERMISSION_DENIED: 1, POSITION_UNAVAILABLE: 2, TIMEOUT: 3 });
      } else {
        success({
          coords: { latitude: 55.75, longitude: 37.61, accuracy: 50, altitude: null, altitudeAccuracy: null, heading: null, speed: null, toJSON: () => ({}) },
          timestamp: Date.now(), toJSON: () => ({}),
        });
      }
    });
    setGeolocation(getCurrentPosition);

    const result = await new MaxBridgeAdapterImpl().requestLocation();

    expect(result).toMatchObject({ ok: true, position: { lat: 55.75, lng: 37.61 } });
    expect(getCurrentPosition).toHaveBeenCalledTimes(2);
    expect(getCurrentPosition.mock.calls[0]?.[2]).toMatchObject({ enableHighAccuracy: false, timeout: 15_000 });
    expect(getCurrentPosition.mock.calls[1]?.[2]).toMatchObject({ enableHighAccuracy: true, timeout: 5_000, maximumAge: 0 });
  });

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
