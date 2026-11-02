import { expect, test, type Page } from "@playwright/test";

test.use({ serviceWorkers: "allow" });

async function shellCacheEntries(page: Page): Promise<string[]> {
  return page.evaluate(async () => {
    const cacheNames = (await caches.keys()).filter((name) => name.startsWith("inspection-capture-shell-"));
    return (await Promise.all(cacheNames.map(async (name) => {
      const cache = await caches.open(name);
      return (await cache.keys()).map((request) => new URL(request.url).pathname);
    }))).flat();
  });
}

test("E2E-064 service worker caches only public shell assets and Next static files", async ({ page, browserName }) => {
  // WebKit does not apply Playwright route mocks to requests of a page controlled by a service worker, so the probe responses would be real 404s and the cache-policy check would be vacuous there. The worker code is engine-independent and is exercised in Chromium.
  test.skip(browserName === "webkit", "Playwright routing is bypassed by service-worker-controlled pages in WebKit.");
  await page.route("**/graphql", (route) => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify({ data: { requestInvitationOtp: { status: "SENT", userErrors: [], clientMutationId: "sw-test" } } }),
  }));
  await page.route("**/media/sw-private-proof.jpg", (route) => route.fulfill({ status: 200, contentType: "image/jpeg", body: "private-media" }));

  const token = "sw-policy-test-token";
  await page.goto(`/capture/${token}`);
  await page.evaluate(() => navigator.serviceWorker.ready);
  await page.reload();
  await expect.poll(() => page.evaluate(() => Boolean(navigator.serviceWorker.controller))).toBe(true);

  const statuses = await page.evaluate(async (capturePath) => {
    const graphql = await fetch("/graphql", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ query: "query CachePolicyProbe { __typename }" }),
    });
    const capture = await fetch(capturePath);
    const media = await fetch("/media/sw-private-proof.jpg");
    return [graphql.status, capture.status, media.status];
  }, `/capture/${token}`);
  expect(statuses).toEqual([200, 200, 200]);

  // The worker stores static files asynchronously; inspect the cache only after it settled.
  await expect.poll(async () => (await shellCacheEntries(page)).some((path) => path.startsWith("/_next/static/"))).toBe(true);
  const entries = await shellCacheEntries(page);

  expect(entries).toContain("/manifest.webmanifest");
  expect(entries).toContain("/icon.svg");
  expect(entries.every((path) => path.startsWith("/_next/static/") || ["/manifest.webmanifest", "/icon.svg"].includes(path))).toBe(true);
  expect(entries).not.toContain("/graphql");
  expect(entries).not.toContain(`/capture/${token}`);
  expect(entries).not.toContain("/media/sw-private-proof.jpg");
});
