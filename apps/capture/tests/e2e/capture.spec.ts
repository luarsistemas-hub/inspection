import { expect, test } from "@playwright/test";

test("E2E-053 Capture owns only its invitation route", async ({ page }) => {
  await page.route("**/graphql", async (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ data: { requestInvitationOtp: { userErrors: [] } } }) }));
  await page.goto("/capture/invalid-link");
  await expect(page.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
});
