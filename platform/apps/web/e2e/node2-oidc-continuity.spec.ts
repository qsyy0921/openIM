import { execFile } from "node:child_process";
import { resolve } from "node:path";
import { promisify } from "node:util";

import { expect, test } from "@playwright/test";

const execFileAsync = promisify(execFile);

type SessionMetadata = {
  subject: string;
  tenantID: string;
  accessExpiry: number;
  idExpiry: number;
  hasRefreshToken: boolean;
};

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

async function sessionMetadata(page: import("@playwright/test").Page): Promise<SessionMetadata> {
  return page.evaluate(() => {
    for (const key of Object.keys(window.sessionStorage)) {
      if (!key.startsWith("oidc.user:")) continue;
      const raw = window.sessionStorage.getItem(key);
      if (!raw) continue;
      const value = JSON.parse(raw) as {
        refresh_token?: unknown;
        expires_at?: unknown;
        profile?: { sub?: unknown; tenant_id?: unknown; exp?: unknown };
      };
      if (
        typeof value.profile?.sub !== "string" ||
        typeof value.profile.tenant_id !== "string" ||
        typeof value.expires_at !== "number" ||
        typeof value.profile.exp !== "number"
      ) continue;
      return {
        subject: value.profile.sub,
        tenantID: value.profile.tenant_id,
        accessExpiry: value.expires_at,
        idExpiry: value.profile.exp,
        hasRefreshToken: typeof value.refresh_token === "string" && value.refresh_token.length > 0
      };
    }
    throw new Error("renewable OIDC session metadata is missing");
  });
}

test("real Node2 Web session renews OIDC and preserves OpenIM continuity", async ({ page }) => {
  let consoleErrorCount = 0;
  let refreshSuccessCount = 0;
  const refreshFailureStatuses: number[] = [];
  const failedResponses: Array<{ path: string; status: number }> = [];

  page.on("console", (message) => {
    if (message.type() === "error") consoleErrorCount += 1;
  });
  page.on("response", (response) => {
    const url = new URL(response.url());
    const requestBody = response.request().postData();
    if (url.pathname.endsWith("/protocol/openid-connect/token") && requestBody?.includes("grant_type=refresh_token")) {
      if (response.ok()) refreshSuccessCount += 1;
      else refreshFailureStatuses.push(response.status());
    }
    if (response.status() >= 400) failedResponses.push({ path: url.pathname, status: response.status() });
  });

  await page.goto("/");
  await page.getByRole("button", { name: "企业登录" }).click();
  await expect(page.getByRole("heading", { name: "Sign in to your account" })).toBeVisible();
  await page.getByRole("textbox", { name: "Username or email" }).fill(required("OPENIM_E2E_USERNAME"));
  await page.getByRole("textbox", { name: "Password" }).fill(required("OPENIM_E2E_PASSWORD"));
  await page.getByRole("button", { name: "Sign In" }).click();

  await expect(page.getByRole("heading", { name: "消息" })).toBeVisible({ timeout: 60_000 });
  await expect(page.getByTestId("connection-state")).toHaveText("在线");
  const selfUserID = (await page.getByTestId("self-user-id").innerText()).trim();
  expect(selfUserID).not.toBe("");

  const initial = await sessionMetadata(page);
  expect(initial.hasRefreshToken).toBe(true);
  const nowSeconds = Math.floor(Date.now() / 1000);
  expect(initial.accessExpiry).toBeGreaterThan(nowSeconds);
  expect(initial.idExpiry).toBeGreaterThan(nowSeconds);
  const deadline = Math.max(initial.accessExpiry, initial.idExpiry) + 15;
  const waitMilliseconds = (deadline - nowSeconds) * 1000;
  expect(waitMilliseconds).toBeGreaterThan(0);
  expect(waitMilliseconds).toBeLessThanOrEqual(420_000);

  await page.evaluate(() => {
    const target = document.querySelector<HTMLElement>("[data-testid=connection-state]");
    if (!target) throw new Error("OpenIM connection state is missing");
    const states = [target.innerText];
    const observer = new MutationObserver(() => states.push(target.innerText));
    observer.observe(target, { childList: true, characterData: true, subtree: true });
    (window as unknown as { __openimOIDCContinuity?: { states: string[]; observer: MutationObserver } }).__openimOIDCContinuity = { states, observer };
  });

  await page.waitForTimeout(waitMilliseconds);
  await expect(page.getByRole("heading", { name: "消息" })).toBeVisible();
  await expect(page.getByTestId("connection-state")).toHaveText("在线");
  expect(refreshFailureStatuses).toEqual([]);
  expect(refreshSuccessCount).toBeGreaterThanOrEqual(1);

  const renewed = await sessionMetadata(page);
  expect(renewed.subject).toBe(initial.subject);
  expect(renewed.tenantID).toBe(initial.tenantID);
  expect(renewed.idExpiry).toBeGreaterThan(initial.idExpiry);
  expect(renewed.accessExpiry).toBeGreaterThan(initial.accessExpiry);
  console.log(
    `oidc_continuity=renewed wait_seconds=${Math.round(waitMilliseconds / 1000)} ` +
    `refresh_successes=${refreshSuccessCount} access_exp_advance=${renewed.accessExpiry - initial.accessExpiry} ` +
    `id_exp_advance=${renewed.idExpiry - initial.idExpiry}`
  );
  const connectionStates = await page.evaluate(() => {
    const state = (window as unknown as { __openimOIDCContinuity?: { states: string[]; observer: MutationObserver } }).__openimOIDCContinuity;
    state?.observer.disconnect();
    return state?.states ?? [];
  });
  expect([...new Set(connectionStates)]).toEqual(["在线"]);

  const channelStatus = page.waitForResponse((response) =>
    response.request().method() === "GET" &&
    new URL(response.url()).pathname === "/platform-api/v1/agent/channels/telegram/link"
  );
  await page.getByRole("button", { name: "渠道", exact: true }).click();
  expect((await channelStatus).status()).toBe(200);
  await expect(page.getByRole("heading", { name: "渠道连接" })).toBeVisible();
  console.log("oidc_continuity=platform_api_verified status=200");

  await page.getByRole("button", { name: "消息", exact: true }).click();
  const adminConversation = page.getByTestId("conversation-imAdmin");
  await expect(adminConversation).toBeVisible({ timeout: 30_000 });
  await adminConversation.click();
  const nonce = `${Date.now()}`;
  const outgoingText = `oidc renewed outbound ${nonce}`;
  const incomingText = `oidc renewed inbound ${nonce}`;
  await page.getByRole("textbox", { name: "消息内容" }).fill(outgoingText);
  await page.getByRole("button", { name: "发送", exact: true }).click();
  await expect(page.locator("article.message-row", { hasText: outgoingText })).toContainText("已发送", { timeout: 30_000 });
  await sendFromNode2(selfUserID, incomingText);
  await expect(page.getByLabel("单聊消息").getByText(incomingText, { exact: true })).toBeVisible({ timeout: 30_000 });
  console.log("oidc_continuity=openim_verified outbound=sent inbound=received");

  const browserHygiene = await page.evaluate(() => ({
    localStorageEntries: window.localStorage.length,
    query: window.location.search,
    hash: window.location.hash,
    visibleText: document.body.innerText
  }));
  expect(browserHygiene.localStorageEntries).toBe(0);
  expect(browserHygiene.query).toBe("");
  expect(browserHygiene.hash).toBe("");
  expect(browserHygiene.visibleText).not.toMatch(/eyJ[A-Za-z0-9_-]{20,}\./);
  expect(consoleErrorCount).toBe(0);
  expect(failedResponses).toEqual([]);
});
