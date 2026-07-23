import { execFile } from "node:child_process";
import { resolve } from "node:path";
import { promisify } from "node:util";

import { expect, test, type Locator, type Page } from "@playwright/test";

const execFileAsync = promisify(execFile);
const createdGroupIDs: string[] = [];
const peerUserID = "lifecyclePeer";
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

async function lifecycleAction(action: "ensure-peer" | "transfer-owner", groupID = "", oldOwnerUserID = ""): Promise<void> {
  const script = resolve(process.cwd(), "../../../ops/manage-node2-web-e2e-group-lifecycle.ps1");
  const args = [
    "-NoProfile", "-NonInteractive", "-File", script,
    "-Action", action,
    "-PeerUserID", peerUserID,
    "-SshHost", required("OPENIM_E2E_SSH_HOST")
  ];
  if (action === "transfer-owner") args.push("-GroupID", groupID, "-OldOwnerUserID", oldOwnerUserID);
  await execFileAsync("powershell.exe", args, { timeout: 60_000, windowsHide: true });
}

async function cleanupGroup(groupID: string): Promise<void> {
  const script = resolve(process.cwd(), "../../../ops/cleanup-node2-web-e2e-group.ps1");
  await execFileAsync("powershell.exe", [
    "-NoProfile", "-NonInteractive", "-File", script,
    "-GroupID", groupID,
    "-SshHost", required("OPENIM_E2E_SSH_HOST")
  ], { timeout: 60_000, windowsHide: true });
}

function markGroupCleaned(groupID: string): void {
  const index = createdGroupIDs.indexOf(groupID);
  if (index >= 0) createdGroupIDs.splice(index, 1);
}

async function selectMember(dialog: Locator, userID: string): Promise<void> {
  const candidate = dialog.locator("label.member-candidate", { hasText: userID });
  if (!await candidate.isVisible()) {
    await dialog.getByRole("textbox", { name: "查找群成员" }).fill(userID);
    await dialog.getByRole("button", { name: "查找用户" }).click();
  }
  await expect(candidate).toBeVisible({ timeout: 30_000 });
  await candidate.click();
}

async function createGroup(page: Page, name: string): Promise<string> {
  await page.getByRole("button", { name: "创建群聊" }).click();
  const dialog = page.getByRole("dialog", { name: "创建群聊" });
  await dialog.getByLabel("群名称").fill(name);
  await selectMember(dialog, "imAdmin");
  await dialog.getByRole("button", { name: "创建", exact: true }).click();
  await expect(page.getByTestId("active-group-id")).toBeVisible({ timeout: 30_000 });
  const groupID = (await page.getByTestId("active-group-id").innerText()).trim();
  expect(groupID).not.toBe("");
  createdGroupIDs.push(groupID);
  return groupID;
}

test.afterEach(async () => {
  const failures: Error[] = [];
  while (createdGroupIDs.length) {
    const groupID = createdGroupIDs.pop()!;
    try { await cleanupGroup(groupID); } catch (error) { failures.push(error as Error); }
  }
  if (failures.length) throw failures[0];
});

test("real node2 owner invite/remove/dismiss and transferred-owner member leave converge", async ({ page }) => {
  const consoleErrors: string[] = [];
  const failedResponses: Array<{ status: number; url: string }> = [];
  page.on("console", (message) => { if (message.type() === "error") consoleErrors.push(message.text()); });
  page.on("response", (response) => { if (response.status() >= 400) failedResponses.push({ status: response.status(), url: response.url() }); });

  await lifecycleAction("ensure-peer");
  const selfUserID = await login(page);
  expect(selfUserID).not.toBe("");
  const nonce = Date.now();

  const ownerGroupID = await createGroup(page, `Lifecycle Owner ${nonce}`);
  await page.getByRole("button", { name: "查看群成员" }).click();
  const panel = page.getByRole("complementary", { name: "群成员" });
  await expect(panel.locator(".group-member-row", { hasText: selfUserID })).toContainText("群主", { timeout: 30_000 });
  await panel.getByRole("button", { name: "邀请群成员" }).click();
  const inviteDialog = page.getByRole("dialog", { name: "邀请群成员" });
  await expect(inviteDialog.locator("label.member-candidate", { hasText: "imAdmin" })).toHaveCount(0);
  await selectMember(inviteDialog, peerUserID);
  await page.screenshot({ path: "test-results/node2/desktop-group-invite.png", fullPage: true });
  await inviteDialog.getByRole("button", { name: "邀请", exact: true }).click();
  await expect(panel.locator(".group-member-row", { hasText: peerUserID })).toBeVisible({ timeout: 30_000 });

  await panel.getByRole("button", { name: /移除群成员 .*Lifecycle Peer/i }).click();
  const removeDialog = page.getByRole("alertdialog", { name: "确认移除群成员" });
  await expect(removeDialog).toContainText("Lifecycle Peer");
  await page.screenshot({ path: "test-results/node2/desktop-group-remove-confirm.png", fullPage: true });
  await removeDialog.getByRole("button", { name: "确认" }).click();
  await expect(panel.locator(".group-member-row", { hasText: peerUserID })).toHaveCount(0, { timeout: 30_000 });

  await panel.getByRole("button", { name: "解散群聊" }).click();
  const dismissDialog = page.getByRole("alertdialog", { name: "确认解散群聊" });
  await expect(dismissDialog).toContainText("操作不可撤销");
  await dismissDialog.getByRole("button", { name: "确认" }).click();
  await expect(page.getByTestId("active-group-id")).toHaveCount(0, { timeout: 30_000 });
  await expect(page.getByTestId(`conversation-${ownerGroupID}`)).toHaveCount(0);
  markGroupCleaned(ownerGroupID);

  const leaveGroupID = await createGroup(page, `Lifecycle Leave ${nonce}`);
  await page.getByRole("button", { name: "查看群成员" }).click();
  await lifecycleAction("transfer-owner", leaveGroupID, selfUserID);
  const leavePanel = page.getByRole("complementary", { name: "群成员" });
  await leavePanel.getByRole("button", { name: "刷新群成员" }).click();
  await expect(leavePanel.locator(".group-member-row", { hasText: "imAdmin" })).toContainText("群主", { timeout: 30_000 });
  await expect(leavePanel.getByRole("button", { name: "退出群聊" })).toBeVisible();

  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: "test-results/node2/mobile-group-lifecycle.png", fullPage: true });
  await leavePanel.getByRole("button", { name: "退出群聊" }).click();
  const leaveDialog = page.getByRole("alertdialog", { name: "确认退出群聊" });
  await expect(leaveDialog).toContainText("不再接收该群消息");
  await leaveDialog.getByRole("button", { name: "确认" }).click();
  await expect(page.getByTestId("active-group-id")).toHaveCount(0, { timeout: 30_000 });
  await expect(page.getByTestId(`conversation-${leaveGroupID}`)).toHaveCount(0);

  const browserState = await page.evaluate(() => ({
    local: Object.entries(localStorage),
    visibleText: document.body.innerText,
    width: { viewport: window.innerWidth, document: document.documentElement.scrollWidth }
  }));
  expect(browserState.local).toEqual([]);
  expect(browserState.visibleText).not.toContain("user_token");
  expect(browserState.width.document).toBeLessThanOrEqual(browserState.width.viewport);
  expect(failedResponses).toEqual([]);
  expect(consoleErrors.filter((text) => text !== "updateColumnsConversation no record updated")).toEqual([]);
});
