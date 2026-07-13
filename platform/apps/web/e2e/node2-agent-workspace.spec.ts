import { expect, test } from "@playwright/test";

function required(key: string): string {
  const value = process.env[key]?.trim();
  if (!value) throw new Error(`required E2E environment variable ${key} is missing`);
  return value;
}

async function signIn(page: import("@playwright/test").Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "企业登录" }).click();
  await page.getByRole("textbox", { name: "Username or email" }).fill(required("OPENIM_E2E_USERNAME"));
  await page.getByRole("textbox", { name: "Password" }).fill(required("OPENIM_E2E_PASSWORD"));
  await page.getByRole("button", { name: "Sign In" }).click();
  await expect(page.getByRole("heading", { name: "消息" })).toBeVisible({ timeout: 60_000 });
}

test("real node2 Agent workspace completes cited answer and approved idempotent action", async ({ page }) => {
  test.setTimeout(210_000);
  const consoleErrors: string[] = [];
  const failedResponses: Array<{ status: number; url: string }> = [];
  page.on("console", (message) => { if (message.type() === "error") consoleErrors.push(message.text()); });
  page.on("response", (response) => { if (response.status() >= 400) failedResponses.push({ status: response.status(), url: response.url() }); });

  await signIn(page);
  await page.getByRole("button", { name: "智能助手" }).click();
  await expect(page.getByRole("heading", { name: "智能助手" })).toBeVisible();
  await expect(page.getByTestId("agent-user-id")).not.toHaveText("正在初始化", { timeout: 30_000 });

  const nonce = Date.now();
  const question = `OpenIM 平台 本机优先开发 PostgreSQL Keycloak ${nonce}`;
  await page.getByRole("textbox", { name: "向 Agent 提问" }).fill(question);
  await page.getByRole("button", { name: "发送给 Agent" }).click();
  const citedRun = page.locator("article.agent-run", { hasText: question });
  await expect(citedRun).toContainText("已完成", { timeout: 120_000 });
  await expect(citedRun).toContainText("Enterprise Agent · v1");
  await expect(citedRun.getByRole("region", { name: "引用来源" })).toBeVisible();
  await expect(citedRun.getByText("[C1]", { exact: true })).toBeVisible();

  const ticketTitle = `OpenIM 平台 本机优先开发 Web Agent E2E ${nonce}`;
  const actionPrompt = `创建工单：${ticketTitle}`;
  await page.getByRole("textbox", { name: "向 Agent 提问" }).fill(actionPrompt);
  await page.getByRole("button", { name: "发送给 Agent" }).click();
  const actionRun = page.locator("article.agent-run", { hasText: actionPrompt });
  await expect(actionRun.getByRole("button", { name: "批准创建" })).toBeVisible({ timeout: 120_000 });
  await expect(actionRun).toContainText("需要你的批准");
  await expect(actionRun).not.toContainText("Ticket ");
  await actionRun.getByRole("button", { name: "批准创建" }).click();
  await expect(actionRun).toContainText("工单已创建", { timeout: 60_000 });
  await expect(actionRun.locator("code", { hasText: "Ticket " })).toBeVisible();

  await page.reload();
  await expect(page.getByRole("heading", { name: "消息" })).toBeVisible({ timeout: 60_000 });
  await page.getByRole("button", { name: "智能助手" }).click();
  const restoredAction = page.locator("article.agent-run", { hasText: actionPrompt });
  await expect(restoredAction).toContainText("工单已创建", { timeout: 30_000 });
  await expect(page.locator("article.agent-run", { hasText: question }).getByText("[C1]", { exact: true })).toBeVisible();

  const browserState = await page.evaluate(() => ({
    local: Object.entries(localStorage),
    width: { viewport: window.innerWidth, document: document.documentElement.scrollWidth }
  }));
  expect(browserState.local).toEqual([]);
  expect(browserState.width.document).toBeLessThanOrEqual(browserState.width.viewport);
  expect(failedResponses).toEqual([]);
  expect(consoleErrors).toEqual([]);
  await page.screenshot({ path: "test-results/node2/desktop-agent-workspace.png", fullPage: true });

  await page.setViewportSize({ width: 390, height: 844 });
  await expect(restoredAction).toBeVisible();
  const mobileWidth = await page.evaluate(() => ({ viewport: window.innerWidth, document: document.documentElement.scrollWidth }));
  expect(mobileWidth.document).toBeLessThanOrEqual(mobileWidth.viewport);
  await page.screenshot({ path: "test-results/node2/mobile-agent-workspace.png", fullPage: true });
});
