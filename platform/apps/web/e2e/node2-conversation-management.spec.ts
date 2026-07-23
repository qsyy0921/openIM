import { execFile } from "node:child_process";
import { resolve } from "node:path";
import { promisify } from "node:util";

import { expect, test, type Page } from "@playwright/test";

const execFileAsync = promisify(execFile);
test.setTimeout(120_000);

function required(key: string): string {
  const value = process.env[key]?.trim();
  if (!value) throw new Error(`required E2E environment variable ${key} is missing`);
  return value;
}

async function login(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "企业登录" }).click();
  await page.getByRole("textbox", { name: "Username or email" }).fill(required("OPENIM_E2E_USERNAME"));
  await page.getByRole("textbox", { name: "Password" }).fill(required("OPENIM_E2E_PASSWORD"));
  await page.getByRole("button", { name: "Sign In" }).click();
  await expect(page.getByRole("heading", { name: "消息" })).toBeVisible({ timeout: 60_000 });
  await expect(page.getByTestId("connection-state")).toHaveText("在线");
}

async function sendFromNode2(targetUserID: string, content: string): Promise<void> {
  const script = resolve(process.cwd(), "../../../ops/send-node2-web-e2e-message.ps1");
  await execFileAsync("powershell.exe", [
    "-NoProfile",
    "-NonInteractive",
    "-File",
    script,
    "-TargetUserID",
    targetUserID,
    "-Content",
    content,
    "-SshHost",
    required("OPENIM_E2E_SSH_HOST")
  ], { timeout: 60_000, windowsHide: true });
}

async function openAdminMenu(page: Page): Promise<void> {
  const menu = page.getByRole("menu", { name: /管理会话 .*imAdmin/i });
  if (!await menu.isVisible()) await page.getByRole("button", { name: /会话操作 .*imAdmin/i }).click();
  await expect(menu).toBeVisible();
}

async function normalizeAdminConversation(page: Page): Promise<void> {
  const item = page.getByTestId("conversation-item-imAdmin");
  await expect(item).toBeVisible({ timeout: 30_000 });
  if (await item.getAttribute("data-muted") === "true") {
    await openAdminMenu(page);
    await page.getByRole("menuitem", { name: "关闭免打扰" }).click();
    await expect(item).toHaveAttribute("data-muted", "false", { timeout: 30_000 });
  }
  if (await item.getAttribute("data-pinned") === "true") {
    await openAdminMenu(page);
    await page.getByRole("menuitem", { name: "取消置顶" }).click();
    await expect(item).toHaveAttribute("data-pinned", "false", { timeout: 30_000 });
  }
}

test.afterEach(async ({ page }) => {
  if (await page.getByTestId("conversation-item-imAdmin").count()) await normalizeAdminConversation(page);
});

test("real node2 conversation search, pin, and do-not-disturb persist without blocking messages", async ({ page }) => {
  const consoleErrors: string[] = [];
  const failedResponses: Array<{ status: number; url: string }> = [];
  page.on("console", (message) => { if (message.type() === "error") consoleErrors.push(message.text()); });
  page.on("response", (response) => { if (response.status() >= 400) failedResponses.push({ status: response.status(), url: response.url() }); });

  await login(page);
  const selfUserID = (await page.getByTestId("self-user-id").innerText()).trim();
  expect(selfUserID).not.toBe("");
  await normalizeAdminConversation(page);

  const search = page.getByRole("textbox", { name: "搜索会话" });
  await search.fill("  IMADMIN  ");
  await expect(page.getByTestId("conversation-imAdmin")).toBeVisible();
  await expect(page.locator(".conversation-item:visible")).toHaveCount(1);
  await page.getByRole("button", { name: "清除会话搜索" }).click();
  await expect.poll(async () => page.locator(".conversation-item:visible").count()).toBeGreaterThan(1);

  const item = page.getByTestId("conversation-item-imAdmin");
  await openAdminMenu(page);
  await page.getByRole("menuitem", { name: "置顶会话" }).click();
  await expect(item).toHaveAttribute("data-pinned", "true", { timeout: 30_000 });
  await expect(page.locator(".conversation-list .conversation-item").first()).toHaveAttribute("data-testid", "conversation-item-imAdmin");

  await page.reload();
  await expect(page.getByTestId("connection-state")).toHaveText("在线", { timeout: 60_000 });
  await expect(page.getByTestId("conversation-item-imAdmin")).toHaveAttribute("data-pinned", "true", { timeout: 30_000 });

  await openAdminMenu(page);
  await page.getByRole("menuitem", { name: "开启免打扰" }).click();
  await expect(page.getByTestId("conversation-item-imAdmin")).toHaveAttribute("data-muted", "true", { timeout: 30_000 });
  await openAdminMenu(page);
  await page.screenshot({ path: "test-results/node2/desktop-conversation-menu.png", fullPage: true });
  await page.keyboard.press("Escape");

  await page.getByTestId("conversation-imAdmin").click();
  const content = `node2 muted delivery ${Date.now()}`;
  await sendFromNode2(selfUserID, content);
  await expect(page.getByLabel("单聊消息").getByText(content, { exact: true })).toBeVisible({ timeout: 30_000 });

  await page.reload();
  await expect(page.getByTestId("connection-state")).toHaveText("在线", { timeout: 60_000 });
  await expect(page.getByTestId("conversation-item-imAdmin")).toHaveAttribute("data-muted", "true", { timeout: 30_000 });
  await page.getByTestId("conversation-imAdmin").click();
  await expect(page.getByLabel("单聊消息").getByText(content, { exact: true })).toBeVisible({ timeout: 30_000 });

  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole("button", { name: "返回会话列表" }).click();
  await openAdminMenu(page);
  await page.screenshot({ path: "test-results/node2/mobile-conversation-menu.png", fullPage: true });
  await page.keyboard.press("Escape");
  const width = await page.evaluate(() => ({ viewport: window.innerWidth, document: document.documentElement.scrollWidth }));
  expect(width.document).toBeLessThanOrEqual(width.viewport);

  await normalizeAdminConversation(page);
  await page.reload();
  await expect(page.getByTestId("connection-state")).toHaveText("在线", { timeout: 60_000 });
  await expect(page.getByTestId("conversation-item-imAdmin")).toHaveAttribute("data-muted", "false");
  await expect(page.getByTestId("conversation-item-imAdmin")).toHaveAttribute("data-pinned", "false");

  const browserState = await page.evaluate(() => ({ local: Object.entries(localStorage), visibleText: document.body.innerText }));
  expect(browserState.local).toEqual([]);
  expect(browserState.visibleText).not.toContain("user_token");
  expect(failedResponses).toEqual([]);
  expect(consoleErrors.filter((text) => text !== "updateColumnsConversation no record updated")).toEqual([]);
});
