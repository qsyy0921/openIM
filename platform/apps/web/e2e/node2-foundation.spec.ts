import { execFile } from "node:child_process";
import { resolve } from "node:path";
import { promisify } from "node:util";
import { crc32, deflateSync } from "node:zlib";

import { expect, test } from "@playwright/test";

const execFileAsync = promisify(execFile);
let createdGroupID = "";
let webUserID = "";
test.setTimeout(180_000);

function pngChunk(type: string, data: Buffer): Buffer {
  const name = Buffer.from(type, "ascii");
  const length = Buffer.alloc(4);
  length.writeUInt32BE(data.length);
  const checksum = Buffer.alloc(4);
  checksum.writeUInt32BE(crc32(Buffer.concat([name, data])));
  return Buffer.concat([length, name, data, checksum]);
}

function testPNG(width: number, height: number): Buffer {
  const header = Buffer.alloc(13);
  header.writeUInt32BE(width, 0);
  header.writeUInt32BE(height, 4);
  header.set([8, 2, 0, 0, 0], 8);
  const rows: Buffer[] = [];
  for (let y = 0; y < height; y += 1) {
    const row = Buffer.alloc(1 + width * 3);
    for (let x = 0; x < width; x += 1) {
      const offset = 1 + x * 3;
      const accent = (x > width / 8 && x < width * 7 / 8 && y > height / 4 && y < height * 3 / 4);
      row[offset] = accent ? 49 : 236;
      row[offset + 1] = accent ? 94 : 242;
      row[offset + 2] = accent ? 251 : 249;
    }
    rows.push(row);
  }
  return Buffer.concat([
    Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]),
    pngChunk("IHDR", header),
    pngChunk("IDAT", deflateSync(Buffer.concat(rows))),
    pngChunk("IEND", Buffer.alloc(0))
  ]);
}

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

async function sendMediaFromNode2(
  targetUserID: string,
  type: "image" | "file",
  url: string,
  fileName: string,
  fileSize: number,
  width = 1,
  height = 1
): Promise<void> {
  const script = resolve(process.cwd(), "../../../ops/send-node2-web-e2e-media.ps1");
  await execFileAsync("powershell.exe", [
    "-NoProfile",
    "-NonInteractive",
    "-File",
    script,
    "-TargetUserID",
    targetUserID,
    "-Type",
    type,
    "-URL",
    url,
    "-FileName",
    fileName,
    "-FileSize",
    String(fileSize),
    "-Width",
    String(width),
    "-Height",
    String(height),
    "-SshHost",
    required("OPENIM_E2E_SSH_HOST")
  ], { timeout: 60_000, windowsHide: true });
}

async function cleanupGroupOnNode2(groupID: string): Promise<void> {
  const script = resolve(process.cwd(), "../../../ops/cleanup-node2-web-e2e-group.ps1");
  await execFileAsync("powershell.exe", [
    "-NoProfile",
    "-NonInteractive",
    "-File",
    script,
    "-GroupID",
    groupID,
    "-SshHost",
    required("OPENIM_E2E_SSH_HOST")
  ], { timeout: 60_000, windowsHide: true });
}

async function manageFriendOnNode2(action: "accept" | "reset", userID: string): Promise<void> {
  const script = resolve(process.cwd(), "../../../ops/manage-node2-web-e2e-friend.ps1");
  await execFileAsync("powershell.exe", [
    "-NoProfile",
    "-NonInteractive",
    "-File",
    script,
    "-Action",
    action,
    "-WebUserID",
    userID,
    "-SshHost",
    required("OPENIM_E2E_SSH_HOST")
  ], { timeout: 60_000, windowsHide: true });
}

test.afterEach(async () => {
  const failures: Error[] = [];
  if (createdGroupID) {
    const groupID = createdGroupID;
    createdGroupID = "";
    try { await cleanupGroupOnNode2(groupID); } catch (error) { failures.push(error as Error); }
  }
  if (webUserID) {
    const userID = webUserID;
    webUserID = "";
    try { await manageFriendOnNode2("reset", userID); } catch (error) { failures.push(error as Error); }
  }
  if (failures.length > 0) throw failures[0];
});

test("real node2 single and group chat survive send, receive, reconnect, and reload", async ({ page, context }) => {
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
  webUserID = selfUserID;
  await manageFriendOnNode2("reset", selfUserID);
  const adminConversation = page.getByTestId("conversation-imAdmin");
  await expect(adminConversation).toBeVisible({ timeout: 30_000 });
  if (await page.getByTestId("unread-imAdmin").count()) {
    await adminConversation.click();
    await expect(page.getByTestId("unread-imAdmin")).toHaveCount(0);
    await page.reload();
    await expect(page.getByRole("heading", { name: "消息" })).toBeVisible({ timeout: 60_000 });
    await expect(page.getByTestId("connection-state")).toHaveText("在线");
  }
  const initialUnread = Number((await page.getByTestId("total-unread").innerText()).trim());
  expect(Number.isInteger(initialUnread)).toBe(true);
  await expect(page.getByTestId("unread-imAdmin")).toHaveCount(0);

  const nonce = `${Date.now()}`;
  const unreadText = `node2 unread ${nonce}`;
  const outgoingText = `web outbound ${nonce}`;
  const realtimeText = `node2 realtime ${nonce}`;
  const pngWidth = 480;
  const pngHeight = 320;
  const pngBytes = testPNG(pngWidth, pngHeight);
  const outboundFileName = `web-outbound-${nonce}.txt`;
  const inboundFileName = `node2-inbound-${nonce}.txt`;
  const outboundFileContent = `OpenIM media E2E ${nonce}`;
  await sendFromNode2(selfUserID, unreadText);

  await expect(adminConversation).toBeVisible({ timeout: 30_000 });
  await expect(page.getByTestId("unread-imAdmin")).toBeVisible();
  await expect.poll(async () => Number(await page.getByTestId("total-unread").innerText())).toBeGreaterThan(initialUnread);
  await adminConversation.click();
  const messageList = page.getByLabel("单聊消息");
  await expect(messageList.getByText(unreadText, { exact: true })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByTestId("unread-imAdmin")).toHaveCount(0);
  await expect(page.getByTestId("total-unread")).toHaveText(String(initialUnread));

  await page.getByRole("textbox", { name: "消息内容" }).fill(outgoingText);
  await page.getByRole("button", { name: "发送", exact: true }).click();
  const outgoing = page.locator("article.message-row", { hasText: outgoingText });
  await expect(outgoing).toContainText("已发送", { timeout: 30_000 });

  await sendFromNode2(selfUserID, realtimeText);
  await expect(messageList.getByText(realtimeText, { exact: true })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByTestId("total-unread")).toHaveText(String(initialUnread));

  const imageCountBeforeInvalidInput = await messageList.locator(".image-message").count();
  await page.getByLabel("选择图片").setInputFiles({ name: `invalid-${nonce}.png`, mimeType: "image/png", buffer: Buffer.from("not a png", "utf8") });
  await expect(page.getByRole("alert")).toContainText("图片无法解码");
  await expect(messageList.locator(".image-message")).toHaveCount(imageCountBeforeInvalidInput);
  await page.getByRole("alert").getByRole("button", { name: "关闭" }).click();

  await page.getByLabel("选择图片").setInputFiles({ name: `web-outbound-${nonce}.png`, mimeType: "image/png", buffer: pngBytes });
  const outgoingImage = messageList.locator("article.message-row.outgoing", { has: page.locator(".image-message") }).last();
  await expect(outgoingImage).toContainText("已发送", { timeout: 30_000 });
  const outgoingImageElement = outgoingImage.locator("img");
  await expect(outgoingImageElement).toHaveAttribute("src", /^https?:\/\//);
  const imageURL = await outgoingImageElement.getAttribute("src");
  expect(imageURL).toBeTruthy();

  await page.getByLabel("选择文件").setInputFiles({ name: outboundFileName, mimeType: "text/plain", buffer: Buffer.from(outboundFileContent, "utf8") });
  const outgoingFile = messageList.locator("article.message-row.outgoing", { hasText: outboundFileName });
  await expect(outgoingFile).toContainText("已发送", { timeout: 30_000 });
  const fileURL = await outgoingFile.getByRole("link", { name: `下载文件 ${outboundFileName}` }).getAttribute("href");
  expect(fileURL).toMatch(/^https?:\/\//);
  const downloadedFile = await context.request.get(fileURL!);
  expect(downloadedFile.ok()).toBe(true);
  expect(await downloadedFile.text()).toBe(outboundFileContent);

  const incomingImageCount = await messageList.locator("article.message-row.incoming .image-message").count();
  await sendMediaFromNode2(selfUserID, "image", imageURL!, `node2-inbound-${nonce}.png`, pngBytes.length, pngWidth, pngHeight);
  await expect(messageList.locator("article.message-row.incoming .image-message")).toHaveCount(incomingImageCount + 1, { timeout: 30_000 });
  await sendMediaFromNode2(selfUserID, "file", fileURL!, inboundFileName, Buffer.byteLength(outboundFileContent, "utf8"));
  await expect(messageList.locator("article.message-row.incoming", { hasText: inboundFileName })).toBeVisible({ timeout: 30_000 });

  await outgoingImage.getByRole("button", { name: "预览图片" }).click();
  await expect(page.getByRole("dialog", { name: "图片预览" })).toBeVisible();
  await page.screenshot({ path: "test-results/node2/desktop-media-preview.png", fullPage: true });
  await page.getByRole("button", { name: "关闭图片预览" }).click();
  await page.screenshot({ path: "test-results/node2/desktop-single-media.png", fullPage: true });

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
  await expect(messageList.locator("article.message-row.outgoing", { hasText: outboundFileName })).toBeVisible();
  await expect(messageList.locator("article.message-row.incoming", { hasText: inboundFileName })).toBeVisible();
  await expect.poll(async () => messageList.locator("article.message-row.incoming img").evaluateAll(
    (images, expectedURL) => images.some((image) => (image as HTMLImageElement).src === expectedURL),
    imageURL
  )).toBe(true);

  await page.getByRole("button", { name: "通讯录" }).click();
  await expect(page.getByRole("heading", { name: "通讯录" })).toBeVisible();
  await page.getByRole("button", { name: "查找用户" }).click();
  await page.getByRole("textbox", { name: "查找 OpenIM 用户" }).fill("imAdmin");
  await page.getByRole("button", { name: "查找", exact: true }).click();
  await expect(page.getByTestId("lookup-imAdmin")).toBeVisible({ timeout: 30_000 });
  await page.getByTestId("lookup-imAdmin").getByRole("button", { name: "添加好友" }).click();
  await page.getByRole("button", { name: "发出的申请" }).click();
  await expect(page.getByTestId("outgoing-application-imAdmin")).toContainText("待处理", { timeout: 30_000 });

  await manageFriendOnNode2("accept", selfUserID);
  await page.getByRole("button", { name: /^好友/ }).click();
  const adminFriend = page.getByTestId("friend-imAdmin");
  await expect(adminFriend).toBeVisible({ timeout: 30_000 });
  await page.screenshot({ path: "test-results/node2/desktop-contacts.png", fullPage: true });
  await adminFriend.getByRole("button", { name: "发消息" }).click();
  await expect(page.getByTestId("active-peer-id")).toHaveText("imAdmin");

  const groupName = `Web Group ${nonce}`;
  const groupText = `web group ${nonce}`;
  const groupFileName = `group-${nonce}.txt`;
  await page.getByRole("button", { name: "创建群聊" }).click();
  const createGroupDialog = page.getByRole("dialog", { name: "创建群聊" });
  await createGroupDialog.getByLabel("群名称").fill(groupName);
  await expect(createGroupDialog.getByLabel("选择群成员")).toContainText("imAdmin");
  await createGroupDialog.locator("label.member-candidate", { hasText: "imAdmin" }).click();
  await expect(createGroupDialog.getByLabel("已选群成员")).toContainText("imAdmin");
  await page.screenshot({ path: "test-results/node2/desktop-member-picker.png", fullPage: true });
  await createGroupDialog.getByRole("button", { name: "创建", exact: true }).click();
  await expect(page.getByTestId("active-group-id")).toBeVisible({ timeout: 30_000 });
  const groupID = (await page.getByTestId("active-group-id").innerText()).trim();
  expect(groupID).not.toBe("");
  createdGroupID = groupID;
  const groupMessages = page.getByLabel("群聊消息");
  await groupMessages.getByRole("textbox", { name: "消息内容" }).fill(groupText);
  await groupMessages.getByRole("button", { name: "发送", exact: true }).click();
  await expect(groupMessages.locator("article.message-row", { hasText: groupText })).toContainText("已发送", { timeout: 30_000 });
  await groupMessages.getByLabel("选择图片").setInputFiles({ name: `group-${nonce}.png`, mimeType: "image/png", buffer: pngBytes });
  const groupImage = groupMessages.locator("article.message-row.outgoing", { has: page.locator(".image-message") }).last();
  await expect(groupImage).toContainText("已发送", { timeout: 30_000 });
  await groupMessages.getByLabel("选择文件").setInputFiles({ name: groupFileName, mimeType: "text/plain", buffer: Buffer.from(`Group media ${nonce}`, "utf8") });
  await expect(groupMessages.locator("article.message-row.outgoing", { hasText: groupFileName })).toContainText("已发送", { timeout: 30_000 });
  await page.getByRole("button", { name: "查看群成员" }).click();
  await expect(page.locator(".group-member-panel")).toContainText("imAdmin", { timeout: 30_000 });
  await page.screenshot({ path: "test-results/node2/desktop-group-members.png", fullPage: true });
  await page.getByRole("button", { name: "关闭群成员" }).click();

  await page.reload();
  await expect(page.getByRole("heading", { name: "消息" })).toBeVisible({ timeout: 60_000 });
  await expect(page.getByTestId("connection-state")).toHaveText("在线");
  await page.getByTestId(`conversation-${groupID}`).click();
  await expect(page.getByLabel("群聊消息").getByText(groupText, { exact: true })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByLabel("群聊消息").locator("article.message-row.outgoing", { hasText: groupFileName })).toBeVisible();
  await expect(page.getByLabel("群聊消息").locator("article.message-row.outgoing .image-message")).toHaveCount(1);

  const browserState = await page.evaluate(() => ({
    local: Object.entries(localStorage),
    visibleText: document.body.innerText,
    width: { viewport: window.innerWidth, document: document.documentElement.scrollWidth }
  }));
  expect(browserState.local).toEqual([]);
  expect(browserState.visibleText).not.toContain("user_token");
  expect(browserState.width.document).toBeLessThanOrEqual(browserState.width.viewport);
  expect(failedResponses).toEqual([]);
  const knownOpenIMConversationRace = consoleErrors.filter((item) => item.text === "updateColumnsConversation no record updated");
  const unexpectedConsoleErrors = consoleErrors.filter((item) => item.text !== "updateColumnsConversation no record updated");
  expect(unexpectedConsoleErrors).toEqual([]);
  expect(knownOpenIMConversationRace.length).toBeLessThanOrEqual(1);
  await page.screenshot({ path: "test-results/node2/desktop-group-chat.png", fullPage: true });

  await page.getByTestId("conversation-imAdmin").click();
  await expect(messageList.getByText(realtimeText, { exact: true })).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(messageList.getByText(realtimeText, { exact: true })).toBeVisible();
  const mobileWidth = await page.evaluate(() => ({
    viewport: window.innerWidth,
    document: document.documentElement.scrollWidth
  }));
  expect(mobileWidth.document).toBeLessThanOrEqual(mobileWidth.viewport);
  await page.screenshot({ path: "test-results/node2/mobile-single-chat.png", fullPage: true });
  await page.getByRole("button", { name: "返回会话列表" }).click();
  await expect(page.getByRole("heading", { name: "消息" })).toBeVisible();
  await expect(page.getByTestId("conversation-imAdmin")).toBeVisible();
  await page.screenshot({ path: "test-results/node2/mobile-conversation-list.png", fullPage: true });
  await page.getByRole("button", { name: "通讯录" }).click();
  await expect(page.getByTestId("friend-imAdmin")).toBeVisible();
  await page.screenshot({ path: "test-results/node2/mobile-contacts.png", fullPage: true });
  await page.getByRole("button", { name: "消息", exact: true }).click();
  await page.getByRole("button", { name: "创建群聊" }).click();
  const mobileGroupDialog = page.getByRole("dialog", { name: "创建群聊" });
  await mobileGroupDialog.getByLabel("群名称").fill(`Mobile preview ${nonce}`);
  await mobileGroupDialog.locator("label.member-candidate", { hasText: "imAdmin" }).click();
  await page.screenshot({ path: "test-results/node2/mobile-member-picker.png", fullPage: true });
  await mobileGroupDialog.getByRole("button", { name: "关闭创建群聊" }).click();
  await page.getByTestId(`conversation-${groupID}`).click();
  await expect(page.getByLabel("群聊消息").getByText(groupText, { exact: true })).toBeVisible();
  await expect(page.getByLabel("群聊消息").locator("article.message-row.outgoing", { hasText: groupFileName })).toBeVisible();
  await page.screenshot({ path: "test-results/node2/mobile-group-chat.png", fullPage: true });
  await page.getByLabel("群聊消息").getByRole("button", { name: "预览图片" }).click();
  await expect(page.getByRole("dialog", { name: "图片预览" })).toBeVisible();
  await page.screenshot({ path: "test-results/node2/mobile-media-preview.png", fullPage: true });
  await page.getByRole("button", { name: "关闭图片预览" }).click();
  await page.getByRole("button", { name: "返回会话列表" }).click();
  await page.getByTestId("conversation-imAdmin").click();
  await expect(messageList.getByText(realtimeText, { exact: true })).toBeVisible();

  const finalBrowserState = await page.evaluate(() => ({
    local: Object.entries(localStorage),
    width: { viewport: window.innerWidth, document: document.documentElement.scrollWidth }
  }));
  expect(finalBrowserState.local).toEqual([]);
  expect(finalBrowserState.width.document).toBeLessThanOrEqual(finalBrowserState.width.viewport);
  expect(failedResponses).toEqual([]);
  expect(consoleErrors.filter((item) => item.text !== "updateColumnsConversation no record updated")).toEqual([]);
  expect(consoleErrors.filter((item) => item.text === "updateColumnsConversation no record updated").length).toBeLessThanOrEqual(1);

  await page.getByRole("button", { name: "退出" }).click();
  await expect(page.getByRole("heading", { name: "企业协作台" })).toBeVisible({ timeout: 30_000 });
});
