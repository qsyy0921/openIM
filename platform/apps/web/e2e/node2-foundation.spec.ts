import { expect, test } from "@playwright/test";

function required(key: string): string {
  const value = process.env[key]?.trim();
  if (!value) throw new Error(`required E2E environment variable ${key} is missing`);
  return value;
}

test("enterprise PKCE exchange reaches a real OpenIM WebSocket", async ({ page }) => {
  const consoleErrors: Array<{ text: string; url: string; line: number }> = [];
  const failedResponses: Array<{ status: number; url: string }> = [];
  page.on("console", (message) => {
    if (message.type() === "error") {
      const location = message.location();
      consoleErrors.push({ text: message.text(), url: location.url, line: location.lineNumber });
    }
  });
  page.on("response", (response) => {
    if (response.status() >= 400) failedResponses.push({ status: response.status(), url: response.url() });
  });

  await page.goto("/");
  await page.getByRole("button", { name: "企业登录" }).click();
  await expect(page.getByRole("heading", { name: "Sign in to your account" })).toBeVisible();
  await page.getByRole("textbox", { name: "Username or email" }).fill(required("OPENIM_E2E_USERNAME"));
  await page.getByRole("textbox", { name: "Password" }).fill(required("OPENIM_E2E_PASSWORD"));
  await page.getByRole("button", { name: "Sign In" }).click();

  await expect(page.getByRole("heading", { name: "OpenIM 已连接" })).toBeVisible({ timeout: 60_000 });
  expect(page.url()).toBe("http://127.0.0.1:3000/");
  await expect(page.getByText("OPEN", { exact: true })).toBeVisible();

  const browserState = await page.evaluate(() => ({
    local: Object.entries(localStorage),
    visibleText: document.body.innerText,
    width: { viewport: window.innerWidth, document: document.documentElement.scrollWidth }
  }));
  expect(browserState.local).toEqual([]);
  expect(browserState.local.some(([, value]) => value.startsWith("eyJ") || value.startsWith("sk-"))).toBe(false);
  expect(browserState.visibleText).not.toContain("user_token");
  expect(browserState.width.document).toBeLessThanOrEqual(browserState.width.viewport);
  expect(failedResponses).toEqual([]);
  expect(consoleErrors).toEqual([]);
  await page.screenshot({ path: "test-results/node2/desktop-connected.png", fullPage: true });

  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByRole("heading", { name: "OpenIM 已连接" })).toBeVisible();
  const mobileWidth = await page.evaluate(() => ({
    viewport: window.innerWidth,
    document: document.documentElement.scrollWidth
  }));
  expect(mobileWidth.document).toBeLessThanOrEqual(mobileWidth.viewport);
  await page.screenshot({ path: "test-results/node2/mobile-connected.png", fullPage: true });

  await page.getByRole("button", { name: "退出" }).click();
  await expect(page.getByRole("heading", { name: "企业协作台" })).toBeVisible({ timeout: 30_000 });
});
