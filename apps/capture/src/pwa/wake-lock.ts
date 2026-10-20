/** Requests a screen wake lock; absence or refusal never blocks recording and yields a no-op release. */
export async function acquireScreenWakeLock(): Promise<() => Promise<void>> {
  try {
    const lock = await navigator.wakeLock?.request("screen");
    if (!lock) return async () => {};
    return async () => { try { await lock.release(); } catch { /* A lock the browser already dropped needs no release. */ } };
  } catch { return async () => {}; }
}
