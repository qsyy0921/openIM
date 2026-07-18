import { execFile } from "node:child_process";
import { resolve } from "node:path";
import { promisify } from "node:util";

import { expect, test, type BrowserContext, type Locator } from "@playwright/test";

const execFileAsync = promisify(execFile);
const peerUserID = "lifecyclePeer";
let createdGroupID = "";
let webUserID = "";
let peerContext: BrowserContext | null = null;
test.setTimeout(240_000);

function required(key: string): string {
  const value = process.env[key]?.trim();
  if (!value) throw new Error(`required E2E environment variable ${key} is missing`);
  return value;
}

async function ensurePeer(): Promise<void> {
  await execFileAsync("powershell.exe", [
    "-NoProfile", "-NonInteractive", "-File", resolve(process.cwd(), "../../../ops/manage-node2-web-e2e-group-lifecycle.ps1"),
    "-Action", "ensure-peer", "-PeerUserID", peerUserID, "-SshHost", required("OPENIM_E2E_SSH_HOST")
  ], { timeout: 60_000, windowsHide: true });
}

async function issuePeerToken(): Promise<string> {
  const result = await execFileAsync("powershell.exe", [
    "-NoProfile", "-NonInteractive", "-File", resolve(process.cwd(), "../../../ops/issue-node2-web-e2e-peer-token.ps1"),
    "-PeerUserID", peerUserID, "-SshHost", required("OPENIM_E2E_SSH_HOST")
  ], { timeout: 60_000, windowsHide: true });
  const token = result.stdout.match(/user_token=([^\s]+)/)?.[1];
  if (!token) throw new Error("Node2 peer-token helper returned no token");
  return token;
}

async function manageFriend(action: "accept" | "reset", userID: string): Promise<void> {
  await execFileAsync("powershell.exe", [
    "-NoProfile", "-NonInteractive", "-File", resolve(process.cwd(), "../../../ops/manage-node2-web-e2e-friend.ps1"),
    "-Action", action, "-WebUserID", userID, "-SshHost", required("OPENIM_E2E_SSH_HOST")
  ], { timeout: 60_000, windowsHide: true });
}

async function cleanupGroup(groupID: string): Promise<void> {
  await execFileAsync("powershell.exe", [
    "-NoProfile", "-NonInteractive", "-File", resolve(process.cwd(), "../../../ops/cleanup-node2-web-e2e-group.ps1"),
    "-GroupID", groupID, "-SshHost", required("OPENIM_E2E_SSH_HOST")
  ], { timeout: 60_000, windowsHide: true });
}

async function openMessageMenu(row: Locator): Promise<void> {
  await row.getByRole("button", { name: /^消息操作 / }).click();
}

test.afterEach(async () => {
  const failures: Error[] = [];
  if (peerContext) {
    const context = peerContext;
    peerContext = null;
    try {
      const peerPage = context.pages()[0];
      if (peerPage) await peerPage.evaluate(async () => { const moduleURL = "/e2e/peer-openim.ts"; const peer = await import(/* @vite-ignore */ moduleURL); await peer.logoutPeer(); });
    } catch (error) { failures.push(error as Error); }
    try { await context.close(); } catch (error) { failures.push(error as Error); }
  }
  if (createdGroupID) {
    const groupID = createdGroupID;
    createdGroupID = "";
    try { await cleanupGroup(groupID); } catch (error) { failures.push(error as Error); }
  }
  if (webUserID) {
    const userID = webUserID;
    webUserID = "";
    try { await manageFriend("reset", userID); } catch (error) { failures.push(error as Error); }
  }
  if (failures.length > 0) throw failures[0];
});

test("real node2 quote, forward, revoke, and C2C receipt actions survive history restore", async ({ page, browser }) => {
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
  webUserID = (await page.getByTestId("self-user-id").innerText()).trim();
  await manageFriend("reset", webUserID);

  const nonce = String(Date.now());
  const incomingText = `peer quote source ${nonce}`;
  const webReply = `web official reply ${nonce}`;
  const outgoingText = `web read source ${nonce}`;
  const peerReply = `peer official reply ${nonce}`;
  const groupName = `Message Actions ${nonce}`;
  const groupForwardText = `group forward source ${nonce}`;
  await ensurePeer();
  const peerToken = await issuePeerToken();
  peerContext = await browser.newContext();
  const peerPage = await peerContext.newPage();
  await peerPage.goto("/");
  await peerPage.evaluate(async ({ userID, token }) => {
    const moduleURL = "/e2e/peer-openim.ts";
    const peer = await import(/* @vite-ignore */ moduleURL);
    await peer.loginPeer(userID, token);
  }, { userID: peerUserID, token: peerToken });
  await peerPage.evaluate(async ({ targetUserID, text }) => {
    const moduleURL = "/e2e/peer-openim.ts";
    const peer = await import(/* @vite-ignore */ moduleURL);
    await peer.sendPeerText(targetUserID, text);
  }, { targetUserID: webUserID, text: incomingText });
  await page.getByTestId(`conversation-${peerUserID}`).click();
  const singleMessages = page.getByLabel("单聊消息");
  const incomingRow = singleMessages.locator("article.message-row.incoming", { hasText: incomingText });
  await page.reload();
  await expect(page.getByTestId("connection-state")).toHaveText("在线", { timeout: 60_000 });
  await page.getByTestId(`conversation-${peerUserID}`).click();
  await expect(incomingRow).toBeVisible({ timeout: 30_000 });

  await openMessageMenu(incomingRow);
  await incomingRow.getByRole("menuitem", { name: "回复" }).click();
  await expect(page.getByLabel("引用回复", { exact: true })).toContainText(incomingText);
  await page.getByRole("textbox", { name: "消息内容" }).fill(webReply);
  await page.getByRole("button", { name: "发送", exact: true }).click();
  const webQuoteRow = singleMessages.locator("article.message-row.outgoing", { hasText: webReply });
  await expect(webQuoteRow).toContainText(incomingText, { timeout: 30_000 });

  await page.getByRole("textbox", { name: "消息内容" }).fill(outgoingText);
  await page.getByRole("button", { name: "发送", exact: true }).click();
  const outgoingRow = singleMessages.locator("article.message-row.outgoing", { hasText: outgoingText });
  await expect(outgoingRow).toContainText("已发送", { timeout: 30_000 });
  await page.reload();
  await expect(page.getByTestId("connection-state")).toHaveText("在线", { timeout: 60_000 });
  await page.getByTestId(`conversation-${peerUserID}`).click();
  await expect(outgoingRow).toBeVisible({ timeout: 30_000 });
  const sourceClientMsgID = await outgoingRow.getAttribute("data-message-id");
  expect(sourceClientMsgID).toBeTruthy();
  const sourceRow = singleMessages.locator(`article.message-row[data-message-id="${sourceClientMsgID}"]`);
  const peerSource = await peerPage.evaluate(async ({ userID, targetUserID, sourceText }) => {
    const moduleURL = "/e2e/peer-openim.ts";
    const peer = await import(/* @vite-ignore */ moduleURL);
    return peer.findPeerMessage(userID, targetUserID, sourceText);
  }, { userID: peerUserID, targetUserID: webUserID, sourceText: outgoingText });
  expect(peerSource.sourceClientMsgID).toBe(sourceClientMsgID);
  expect(peerSource.sourceSeq).toBeGreaterThan(0);
  const peerConversationID = `si_${[webUserID, peerUserID].sort().join("_")}`;
  const readResponse = await peerContext.request.post("http://127.0.0.1:3000/openim-api/msg/mark_msgs_as_read", {
    headers: { token: peerToken, operationID: `node2-web-message-read-${nonce}` },
    data: { conversationID: peerConversationID, userID: peerUserID, seqs: [peerSource.sourceSeq] }
  });
  expect(readResponse.ok()).toBe(true);
  expect((await readResponse.json() as { errCode: number }).errCode).toBe(0);
  await page.reload();
  await expect(page.getByTestId("connection-state")).toHaveText("在线", { timeout: 60_000 });
  await page.getByTestId(`conversation-${peerUserID}`).click();
  await expect(outgoingRow).toBeVisible({ timeout: 30_000 });
  await expect(outgoingRow).toContainText("已读", { timeout: 30_000 });
  await peerPage.evaluate(async ({ userID, targetUserID, sourceText, replyText }) => {
    const moduleURL = "/e2e/peer-openim.ts";
    const peer = await import(/* @vite-ignore */ moduleURL);
    await peer.quotePeerMessage(userID, targetUserID, sourceText, replyText);
  }, { userID: peerUserID, targetUserID: webUserID, sourceText: outgoingText, replyText: peerReply });
  await page.reload();
  await expect(page.getByTestId("connection-state")).toHaveText("在线", { timeout: 60_000 });
  await page.getByTestId(`conversation-${peerUserID}`).click();
  const peerQuoteRow = singleMessages.locator("article.message-row.incoming", { hasText: peerReply });
  await expect(peerQuoteRow).toContainText(outgoingText, { timeout: 30_000 });

  await openMessageMenu(sourceRow);
  await sourceRow.getByRole("menuitem", { name: "撤回" }).click();
  const revokeDialog = page.getByRole("alertdialog", { name: "确认撤回消息" });
  await page.screenshot({ path: "test-results/node2/desktop-message-revoke.png", fullPage: true });
  await revokeDialog.getByRole("button", { name: "确认撤回" }).click();
  await expect(sourceRow).toContainText("你撤回了一条消息", { timeout: 30_000 });

  await openMessageMenu(incomingRow);
  await incomingRow.getByRole("menuitem", { name: "撤回" }).click();
  await revokeDialog.getByRole("button", { name: "确认撤回" }).click();
  await expect(revokeDialog.locator(".inline-error")).toContainText("only send by yourself", { timeout: 30_000 });
  await expect(incomingRow).toContainText(incomingText);
  await revokeDialog.locator(".inline-error").getByRole("button", { name: "关闭" }).click();
  await revokeDialog.getByRole("button", { name: "取消" }).click();

  await page.getByRole("button", { name: "通讯录" }).click();
  await page.getByRole("button", { name: "查找用户" }).click();
  await page.getByRole("textbox", { name: "查找 OpenIM 用户" }).fill("imAdmin");
  await page.getByRole("button", { name: "查找", exact: true }).click();
  await page.getByTestId("lookup-imAdmin").getByRole("button", { name: "添加好友" }).click();
  await manageFriend("accept", webUserID);
  await page.getByRole("button", { name: /^好友/ }).click();
  await expect(page.getByTestId("friend-imAdmin")).toBeVisible({ timeout: 30_000 });
  await page.getByRole("button", { name: "消息", exact: true }).click();
  await page.getByRole("button", { name: "创建群聊" }).click();
  const createGroup = page.getByRole("dialog", { name: "创建群聊" });
  await createGroup.getByLabel("群名称").fill(groupName);
  await createGroup.locator("label.member-candidate", { hasText: "imAdmin" }).click();
  await createGroup.getByRole("button", { name: "创建", exact: true }).click();
  await expect(page.getByTestId("active-group-id")).toBeVisible({ timeout: 30_000 });
  createdGroupID = (await page.getByTestId("active-group-id").innerText()).trim();

  await page.getByTestId(`conversation-${peerUserID}`).click();
  const restoredIncoming = singleMessages.locator("article.message-row.incoming", { hasText: incomingText });
  await openMessageMenu(restoredIncoming);
  await restoredIncoming.getByRole("menuitem", { name: "转发" }).click();
  const forwardDialog = page.getByRole("dialog", { name: "转发消息" });
  await forwardDialog.getByRole("radio", { name: new RegExp(groupName) }).click();
  await page.screenshot({ path: "test-results/node2/desktop-message-forward.png", fullPage: true });
  await forwardDialog.getByRole("button", { name: "转发", exact: true }).click();
  await expect(forwardDialog).toHaveCount(0, { timeout: 30_000 });
  await page.getByTestId(`conversation-${createdGroupID}`).click();
  const groupMessages = page.getByLabel("群聊消息");
  await expect(groupMessages.locator("article.message-row", { hasText: incomingText })).toBeVisible({ timeout: 30_000 });

  await groupMessages.getByRole("textbox", { name: "消息内容" }).fill(groupForwardText);
  await groupMessages.getByRole("button", { name: "发送", exact: true }).click();
  const groupRow = groupMessages.locator("article.message-row.outgoing", { hasText: groupForwardText });
  await expect(groupRow).toContainText("已发送", { timeout: 30_000 });
  await openMessageMenu(groupRow);
  await groupRow.getByRole("menuitem", { name: "转发" }).click();
  await forwardDialog.getByRole("radio", { name: new RegExp(peerUserID) }).click();
  await forwardDialog.getByRole("button", { name: "转发", exact: true }).click();
  await page.getByTestId(`conversation-${peerUserID}`).click();
  await expect(singleMessages.locator("article.message-row", { hasText: groupForwardText })).toBeVisible({ timeout: 30_000 });

  await page.reload();
  await expect(page.getByTestId("connection-state")).toHaveText("在线", { timeout: 60_000 });
  await page.getByTestId(`conversation-${peerUserID}`).click();
  await expect(singleMessages.locator("article.message-row", { hasText: webReply })).toContainText(incomingText, { timeout: 30_000 });
  await expect(singleMessages.locator("article.message-row", { hasText: peerReply })).toContainText("消息已撤回");
  await expect(sourceRow).toContainText("你撤回了一条消息");
  await openMessageMenu(incomingRow);
  await incomingRow.getByRole("menuitem", { name: "回复" }).click();
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByLabel("引用回复", { exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  await page.screenshot({ path: "test-results/node2/mobile-message-reply.png", fullPage: true });

  expect(failedResponses).toEqual([]);
  expect(consoleErrors.filter((text) => text !== "updateColumnsConversation no record updated")).toEqual([]);
});
