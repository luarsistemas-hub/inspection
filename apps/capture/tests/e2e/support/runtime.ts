import { expect, type Page } from "@playwright/test";

export function installRuntimeGuards(page: Page, expectedFailure?: (failure: string) => boolean) {
  const failures: string[] = [];
  // WebKit logs its own media-control icon loading as console errors for any <video controls>; they are engine noise, not app failures.
  const engineNoise = /^Button failed to load, iconName = /;
  page.on("console", (message) => { if (message.type() === "error" && !engineNoise.test(message.text())) failures.push(`console: ${message.text()}`); });
  page.on("pageerror", (error) => failures.push(`page: ${error.message}`));
  page.on("response", async (response) => {
    if (!response.url().endsWith("/graphql")) return;
    try {
      const body = await response.json() as { errors?: Array<{ message?: string }> };
      for (const error of body.errors ?? []) failures.push(`graphql: ${error.message ?? "unknown error"}`);
    } catch {
      // Non-JSON responses are covered by the transport unit tests.
    }
  });
  return async () => expect(failures.filter((failure) => !expectedFailure?.(failure)), failures.join("\n")).toEqual([]);
}
