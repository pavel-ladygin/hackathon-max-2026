import { afterEach, describe, expect, it, vi } from "vitest";
import { withMinimumDuration } from "./async";

describe("withMinimumDuration", () => {
  afterEach(() => vi.useRealTimers());

  it("holds a fast result until the minimum duration", async () => {
    vi.useFakeTimers();
    const result = withMinimumDuration(Promise.resolve("ok"), 300);
    await vi.advanceTimersByTimeAsync(299);
    expect(await Promise.race([result, Promise.resolve("pending")])).toBe("pending");
    await vi.advanceTimersByTimeAsync(1);
    await expect(result).resolves.toBe("ok");
  });

  it("preserves rejected results after the minimum duration", async () => {
    vi.useFakeTimers();
    const result = withMinimumDuration(Promise.reject(new Error("failed")), 200);
    const assertion = expect(result).rejects.toThrow("failed");
    await vi.advanceTimersByTimeAsync(200);
    await assertion;
  });
});
