import { expect, test } from "@playwright/test";

test.setTimeout(150_000);

function required(key: string): string {
  const value = process.env[key]?.trim();
  if (!value) throw new Error(`required E2E environment variable ${key} is missing`);
  return value;
}

test("real node2 local message search pages and jumps through official SDK context", async ({ page }) => {
  const consoleErrors: string[] = [];
  const failedResponses: Array<{ status: number; url: string }> = [];
  page.on("console", (message) => { if (message.type() === "error") consoleErrors.push(message.text()); });
  page.on("response", (response) => { if (response.status() >= 400) failedResponses.push({ status: response.status(), url: response.url() }); });

  await page.goto("/");
  await page.getByRole("button", { name: "企业登录" }).click();
  await page.getByRole("textbox", { name: "Username or email" }).fill(required("OPENIM_E2E_USERNAME"));
  await page.getByRole("textbox", { name: "Password" }).fill(required("OPENIM_E2E_PASSWORD"));
  await page.getByRole("button", { name: "Sign In" }).click();
  await expect(page.getByRole("heading", { name: "消息" })).toBeVisible({ timeout: 60_000 });
  await expect(page.getByTestId("connection-state")).toHaveText("在线");
  const nonce = String(Date.now());
  const keyword = `searchnonce${nonce}`;
  const content = `OpenIM local search ${keyword}`;
  await page.getByTestId("conversation-imAdmin").click();
  await page.getByRole("textbox", { name: "消息内容" }).fill(content);
  await page.getByRole("button", { name: "发送", exact: true }).click();
  await expect(page.getByLabel("单聊消息").locator("article.message-row.outgoing", { hasText: content })).toContainText("已发送", { timeout: 30_000 });

  await page.reload();
  await expect(page.getByTestId("connection-state")).toHaveText("在线", { timeout: 60_000 });
  await page.getByTestId("conversation-imAdmin").click();
  const messages = page.getByLabel("单聊消息");
  await expect(messages.getByText(content, { exact: true })).toBeVisible({ timeout: 30_000 });

  await page.getByRole("button", { name: "搜索当前会话消息" }).click();
  const searchPanel = page.getByRole("complementary", { name: "消息搜索" });
  await expect(searchPanel).toBeVisible();
  await expect(searchPanel.getByRole("button", { name: "搜索", exact: true })).toBeDisabled();
  await searchPanel.getByRole("textbox", { name: "搜索当前会话消息关键词" }).fill(keyword);
  await searchPanel.getByRole("button", { name: "搜索", exact: true }).click();
  await expect(searchPanel.getByText("1 条", { exact: true })).toBeVisible({ timeout: 30_000 });
  const hit = searchPanel.locator("[data-testid^='search-hit-']", { hasText: content });
  await expect(hit).toBeVisible();
  await expect(searchPanel.getByRole("button", { name: "上一页" })).toBeDisabled();
  await expect(searchPanel.getByRole("button", { name: "下一页" })).toBeDisabled();
  await page.screenshot({ path: "test-results/node2/desktop-message-search.png", fullPage: true });

  const hitID = (await hit.getAttribute("data-testid"))?.replace("search-hit-", "");
  expect(hitID).toBeTruthy();
  await hit.click();
  await expect(searchPanel).toHaveCount(0);
  const target = messages.locator(`article.message-row[data-message-id="${hitID}"]`);
  await expect(target).toContainText(content, { timeout: 30_000 });
  await expect(target).toHaveClass(/search-target/);

  await page.reload();
  await expect(page.getByTestId("connection-state")).toHaveText("在线", { timeout: 60_000 });
  await page.getByTestId("conversation-imAdmin").click();
  await page.getByRole("button", { name: "搜索当前会话消息" }).click();
  await searchPanel.getByRole("textbox", { name: "搜索当前会话消息关键词" }).fill(keyword);
  await searchPanel.getByRole("button", { name: "搜索", exact: true }).click();
  await expect(searchPanel.locator("[data-testid^='search-hit-']", { hasText: content })).toBeVisible({ timeout: 30_000 });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  await page.screenshot({ path: "test-results/node2/mobile-message-search.png", fullPage: true });

  const browserState = await page.evaluate(() => ({ local: Object.entries(localStorage), visible: document.body.innerText }));
  expect(browserState.local).toEqual([]);
  expect(browserState.visible).not.toContain("user_token");
  expect(failedResponses).toEqual([]);
  expect(consoleErrors.filter((text) => text !== "updateColumnsConversation no record updated")).toEqual([]);
});
