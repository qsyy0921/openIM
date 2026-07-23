import { execFile } from "node:child_process";
import { resolve } from "node:path";
import { promisify } from "node:util";

import { expect, test, type BrowserContext, type Page } from "@playwright/test";

const execFileAsync = promisify(execFile);
const temporaryDeviceID = "node2-e2e-windows-platform";
let enrollmentActive = false;
let peerContext: BrowserContext | null = null;
let peerKicked = false;
test.setTimeout(180_000);

function required(key: string): string {
  const value = process.env[key]?.trim();
  if (!value) throw new Error(`required E2E environment variable ${key} is missing`);
  return value;
}

async function login(page: Page): Promise<string> {
  await page.goto("/");
  await page.getByRole("button", { name: "企业登录" }).click();
  await page.getByRole("textbox", { name: "Username or email" }).fill(required("OPENIM_E2E_USERNAME"));
  await page.getByRole("textbox", { name: "Password" }).fill(required("OPENIM_E2E_PASSWORD"));
  await page.getByRole("button", { name: "Sign In" }).click();
  await expect(page.getByRole("heading", { name: "消息" })).toBeVisible({ timeout: 60_000 });
  await expect(page.getByTestId("connection-state")).toHaveText("在线");
  return (await page.getByTestId("self-user-id").innerText()).trim();
}

async function manageEnrollment(action: "add" | "remove"): Promise<void> {
  await execFileAsync("powershell.exe", [
    "-NoProfile", "-NonInteractive", "-File", resolve(process.cwd(), "../../../ops/manage-node2-web-e2e-device.ps1"),
    "-Action", action, "-DeviceID", temporaryDeviceID, "-PlatformID", "3", "-SshHost", required("OPENIM_E2E_SSH_HOST")
  ], { timeout: 60_000, windowsHide: true });
  enrollmentActive = action === "add";
}

async function issueWindowsToken(userID: string): Promise<string> {
  const result = await execFileAsync("powershell.exe", [
    "-NoProfile", "-NonInteractive", "-File", resolve(process.cwd(), "../../../ops/issue-node2-web-e2e-peer-token.ps1"),
    "-PeerUserID", userID, "-PlatformID", "3", "-SshHost", required("OPENIM_E2E_SSH_HOST")
  ], { timeout: 60_000, windowsHide: true });
  const token = result.stdout.match(/user_token=([^\s]+)/)?.[1];
  if (!token) throw new Error("Node2 peer-token helper returned no token");
  return token;
}

test.afterEach(async () => {
  const failures: Error[] = [];
  if (peerContext) {
    const context = peerContext;
    peerContext = null;
    if (!peerKicked) {
      try {
        const page = context.pages()[0];
        if (page) await page.evaluate(async () => {
          const moduleURL = "/e2e/peer-openim.ts";
          const peer = await import(/* @vite-ignore */ moduleURL);
          await peer.logoutPeer();
        });
      } catch (error) { failures.push(error as Error); }
    }
    try { await context.close(); } catch (error) { failures.push(error as Error); }
  }
  peerKicked = false;
  if (enrollmentActive) {
    try { await manageEnrollment("remove"); } catch (error) { failures.push(error as Error); }
  }
  if (failures.length) throw failures[0];
});

test("real node2 member can inspect and remotely log out another OpenIM platform", async ({ page, browser }) => {
  const consoleErrors: string[] = [];
  const failedResponses: Array<{ status: number; url: string }> = [];
  page.on("console", (message) => { if (message.type() === "error") consoleErrors.push(message.text()); });
  page.on("response", (response) => { if (response.status() >= 400) failedResponses.push({ status: response.status(), url: response.url() }); });

  const userID = await login(page);
  expect(userID).not.toBe("");
  await manageEnrollment("remove");
  await manageEnrollment("add");
  const token = await issueWindowsToken(userID);

  peerContext = await browser.newContext();
  const peerPage = await peerContext.newPage();
  await peerPage.goto("/");
  await peerPage.evaluate(async ({ openIMUserID, openIMToken }) => {
    const moduleURL = "/e2e/peer-openim.ts";
    const peer = await import(/* @vite-ignore */ moduleURL);
    await peer.loginPeer(openIMUserID, openIMToken, 3);
  }, { openIMUserID: userID, openIMToken: token });

  await page.getByRole("button", { name: "设备", exact: true }).click();
  await expect(page.getByRole("heading", { name: "设备与登录" })).toBeVisible();
  const webRow = page.locator(".device-row", { hasText: "Web" });
  const windowsRow = page.locator(".device-row", { hasText: "Windows" });
  await expect(webRow).toContainText("当前");
  await expect(webRow).toContainText("平台在线");
  await expect(windowsRow).toContainText(temporaryDeviceID);
  await expect(windowsRow).toContainText("平台在线", { timeout: 30_000 });
  await page.screenshot({ path: "test-results/node2/desktop-device-management.png", fullPage: true });

  await windowsRow.getByRole("button", { name: "注销" }).click();
  const dialog = page.getByRole("dialog", { name: "注销 Windows 平台" });
  await expect(dialog).toContainText("全部 OpenIM 连接退出");
  await dialog.getByRole("button", { name: "确认注销" }).click();
  await expect(dialog).toHaveCount(0, { timeout: 30_000 });
  await peerPage.evaluate(async () => {
    const moduleURL = "/e2e/peer-openim.ts";
    const peer = await import(/* @vite-ignore */ moduleURL);
    await peer.waitForPeerKicked();
  });
  peerKicked = true;

  await page.getByRole("button", { name: "刷新设备状态" }).click();
  await expect(windowsRow).toContainText("平台离线", { timeout: 30_000 });
  await expect(page.getByText(/最近操作关联 ID/)).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByRole("heading", { name: "设备与登录" })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  await page.screenshot({ path: "test-results/node2/mobile-device-management.png", fullPage: true });

  const browserState = await page.evaluate(() => ({
    local: Object.entries(localStorage),
    visibleText: document.body.innerText
  }));
  expect(browserState.local).toEqual([]);
  expect(browserState.visibleText).not.toContain("user_token");
  expect(browserState.visibleText).not.toContain(token);
  expect(failedResponses).toEqual([]);
  expect(consoleErrors.filter((text) => text !== "updateColumnsConversation no record updated")).toEqual([]);

  await manageEnrollment("remove");
});
