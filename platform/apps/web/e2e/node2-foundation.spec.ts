import { execFile } from "node:child_process";
import { resolve } from "node:path";
import { promisify } from "node:util";

import { expect, test } from "@playwright/test";

const execFileAsync = promisify(execFile);

function required(key: string): string {
  const value = process.env[key]?.trim();
  if (!value) throw new Error(`required E2E environment variable ${key} is missing`);
  return value;
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

test("real node2 single chat survives unread, send, receive, and reconnect", async ({ page, context }) => {
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

  await expect(page.getByRole("heading", { name: "消息" })).toBeVisible({ timeout: 60_000 });
  await expect(page.getByTestId("connection-state")).toHaveText("在线");
  expect(page.url()).toBe("http://127.0.0.1:3000/");
  const selfUserID = (await page.getByTestId("self-user-id").innerText()).trim();
  expect(selfUserID).not.toBe("");
  const initialUnread = Number((await page.getByTestId("total-unread").innerText()).trim());
  expect(Number.isInteger(initialUnread)).toBe(true);
  await expect(page.getByTestId("unread-imAdmin")).toHaveCount(0);

  const nonce = `${Date.now()}`;
  const unreadText = `node2 unread ${nonce}`;
  const outgoingText = `web outbound ${nonce}`;
  const realtimeText = `node2 realtime ${nonce}`;
  await sendFromNode2(selfUserID, unreadText);

  const adminConversation = page.getByTestId("conversation-imAdmin");
  await expect(adminConversation).toBeVisible({ timeout: 30_000 });
  await expect(page.getByTestId("unread-imAdmin")).toBeVisible();
  await expect.poll(async () => Number(await page.getByTestId("total-unread").innerText())).toBeGreaterThan(initialUnread);
  await adminConversation.click();
  const messageList = page.getByLabel("单聊消息");
  await expect(messageList.getByText(unreadText, { exact: true })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByTestId("unread-imAdmin")).toHaveCount(0);
  await expect(page.getByTestId("total-unread")).toHaveText(String(initialUnread));

  await page.getByRole("textbox", { name: "消息内容" }).fill(outgoingText);
  await page.getByRole("button", { name: "发送" }).click();
  const outgoing = page.locator("article.message-row", { hasText: outgoingText });
  await expect(outgoing).toContainText("已发送", { timeout: 30_000 });

  await sendFromNode2(selfUserID, realtimeText);
  await expect(messageList.getByText(realtimeText, { exact: true })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByTestId("total-unread")).toHaveText(String(initialUnread));

  await context.setOffline(true);
  await expect(page.getByTestId("connection-state")).toHaveText("连接异常", { timeout: 15_000 });
  await context.setOffline(false);
  await expect(page.getByTestId("connection-state")).toHaveText("在线", { timeout: 45_000 });
  await expect(messageList.getByText(realtimeText, { exact: true })).toBeVisible();

  await page.reload();
  await expect(page.getByRole("heading", { name: "消息" })).toBeVisible({ timeout: 60_000 });
  await expect(page.getByTestId("connection-state")).toHaveText("在线");
  await page.getByTestId("conversation-imAdmin").click();
  await expect(messageList.getByText(unreadText, { exact: true })).toBeVisible({ timeout: 30_000 });
  await expect(messageList.getByText(outgoingText, { exact: true })).toBeVisible();
  await expect(messageList.getByText(realtimeText, { exact: true })).toBeVisible();

  const browserState = await page.evaluate(() => ({
    local: Object.entries(localStorage),
    visibleText: document.body.innerText,
    width: { viewport: window.innerWidth, document: document.documentElement.scrollWidth }
  }));
  expect(browserState.local).toEqual([]);
  expect(browserState.visibleText).not.toContain("user_token");
  expect(browserState.width.document).toBeLessThanOrEqual(browserState.width.viewport);
  expect(failedResponses).toEqual([]);
  expect(consoleErrors).toEqual([]);
  await page.screenshot({ path: "test-results/node2/desktop-single-chat.png", fullPage: true });

  await page.setViewportSize({ width: 390, height: 844 });
  await expect(messageList.getByText(realtimeText, { exact: true })).toBeVisible();
  const mobileWidth = await page.evaluate(() => ({
    viewport: window.innerWidth,
    document: document.documentElement.scrollWidth
  }));
  expect(mobileWidth.document).toBeLessThanOrEqual(mobileWidth.viewport);
  await page.screenshot({ path: "test-results/node2/mobile-single-chat.png", fullPage: true });

  await page.getByRole("button", { name: "退出" }).click();
  await expect(page.getByRole("heading", { name: "企业协作台" })).toBeVisible({ timeout: 30_000 });
});
