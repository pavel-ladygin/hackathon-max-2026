/** Resolve a promise after at least `minimumMs`, preserving its value or error. */
export async function withMinimumDuration<T>(promise: Promise<T>, minimumMs: number): Promise<T> {
  const started = Date.now();
  try {
    const value = await promise;
    const remaining = Math.max(0, minimumMs - (Date.now() - started));
    if (remaining) await new Promise<void>((resolve) => setTimeout(resolve, remaining));
    return value;
  } catch (error) {
    const remaining = Math.max(0, minimumMs - (Date.now() - started));
    if (remaining) await new Promise<void>((resolve) => setTimeout(resolve, remaining));
    throw error;
  }
}
