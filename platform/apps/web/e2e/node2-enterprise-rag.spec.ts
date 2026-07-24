import { expect, test, type Page, type TestInfo } from "@playwright/test";
import { readFileSync, renameSync, writeFileSync } from "node:fs";
import { isAbsolute, join } from "node:path";

type FixtureDocument = {
  format: "markdown" | "text" | "pdf" | "docx";
  title: string;
  filename: string;
  marker: string;
  version_file?: string;
  version_marker?: string;
};

type FixtureManifest = {
  schema_version: 1;
  batch_id: string;
  question: string;
  old_version_query: string;
  documents: FixtureDocument[];
  invalid_pdf: string;
};

type UploadedDocument = FixtureDocument & {
  document_id: string;
  version_id: string;
  version_number: number;
  next_version_id?: string;
  next_version_number?: number;
};

type AcceptanceState = {
  schema_version: 1;
  batch_id: string;
  documents: UploadedDocument[];
  invalid_document?: {
    title: string;
    document_id: string;
    version_id: string;
  };
  web: {
    bootstrap_run_id?: string;
    denied_run_id?: string;
    revoked_run_id?: string;
    version_run_id?: string;
  };
};

type UploadResult = {
  document_id: string;
  version_id: string;
  version_number: number;
  ingestion_state: string;
  idempotent: boolean;
};

const phase = process.env.OPENIM_E2E_PHASE?.trim() || "bootstrap";
const allowedPhases = new Set(["bootstrap", "denied", "revoke", "version", "cleanup"]);
if (!allowedPhases.has(phase)) throw new Error(`unsupported OPENIM_E2E_PHASE ${phase}`);

function required(key: string): string {
  const value = process.env[key]?.trim();
  if (!value) throw new Error(`required E2E environment variable ${key} is missing`);
  return value;
}

function fixtureDirectory(): string {
  const value = required("OPENIM_E2E_FIXTURE_DIR");
  if (!isAbsolute(value)) throw new Error("OPENIM_E2E_FIXTURE_DIR must be absolute");
  return value;
}

function statePath(): string {
  const value = required("OPENIM_E2E_STATE_PATH");
  if (!isAbsolute(value)) throw new Error("OPENIM_E2E_STATE_PATH must be absolute");
  return value;
}

function loadManifest(): FixtureManifest {
  const value = JSON.parse(readFileSync(join(fixtureDirectory(), "manifest.json"), "utf8")) as Partial<FixtureManifest>;
  if (value.schema_version !== 1 || typeof value.batch_id !== "string" ||
    typeof value.question !== "string" || typeof value.old_version_query !== "string" ||
    !Array.isArray(value.documents) || value.documents.length !== 4 ||
    !value.documents.every((item) => typeof item.title === "string" &&
      typeof item.filename === "string" && typeof item.marker === "string") ||
    typeof value.invalid_pdf !== "string") {
    throw new Error("enterprise RAG fixture manifest is malformed");
  }
  return value as FixtureManifest;
}

function loadState(manifest: FixtureManifest): AcceptanceState {
  const value = JSON.parse(readFileSync(statePath(), "utf8")) as Partial<AcceptanceState>;
  if (value.schema_version !== 1 || value.batch_id !== manifest.batch_id ||
    !Array.isArray(value.documents) || value.documents.length !== 4 ||
    typeof value.web !== "object" || value.web === null) {
    throw new Error("enterprise RAG acceptance state is malformed or belongs to another batch");
  }
  return value as AcceptanceState;
}

function saveState(value: AcceptanceState): void {
  const target = statePath();
  const temporary = `${target}.tmp`;
  writeFileSync(temporary, `${JSON.stringify(value, null, 2)}\n`, { encoding: "utf8", mode: 0o600 });
  renameSync(temporary, target);
}

async function signIn(page: Page, usernameKey: string, passwordKey: string): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "企业登录" }).click();
  await page.getByRole("textbox", { name: "Username or email" }).fill(required(usernameKey));
  await page.getByRole("textbox", { name: "Password" }).fill(required(passwordKey));
  await page.getByRole("button", { name: "Sign In" }).click();
  await expect(page.getByRole("heading", { name: "消息" })).toBeVisible({ timeout: 60_000 });
}

function collectBrowserFailures(page: Page): {
  consoleErrors: string[];
  failedResponses: Array<{ status: number; url: string }>;
} {
  const consoleErrors: string[] = [];
  const failedResponses: Array<{ status: number; url: string }> = [];
  page.on("console", (message) => {
    if (message.type() === "error") consoleErrors.push(message.text());
  });
  page.on("response", (response) => {
    if (response.status() >= 400) failedResponses.push({ status: response.status(), url: response.url() });
  });
  return { consoleErrors, failedResponses };
}

function validateUploadResult(value: unknown): UploadResult {
  const result = value as Partial<UploadResult> | null;
  if (typeof result !== "object" || result === null ||
    typeof result.document_id !== "string" || typeof result.version_id !== "string" ||
    typeof result.version_number !== "number" || typeof result.ingestion_state !== "string" ||
    typeof result.idempotent !== "boolean") {
    throw new Error("knowledge upload response is malformed");
  }
  return result as UploadResult;
}

function documentRow(page: Page, title: string) {
  return page.locator("button.knowledge-document-row").filter({ hasText: title });
}

async function openKnowledge(page: Page): Promise<void> {
  await page.getByRole("button", { name: "知识库" }).click();
  await expect(page.getByRole("heading", { name: "知识库" })).toBeVisible();
}

async function upload(
  page: Page,
  title: string,
  filename: string,
  documentID?: string
): Promise<UploadResult> {
  if (documentID) {
    await page.getByRole("button", { name: "新版本", exact: true }).click();
  } else {
    await page.getByRole("button", { name: "新文档", exact: true }).click();
    await page.getByLabel("标题").fill(title);
    await page.getByLabel("密级").selectOption("internal");
  }
  await page.getByLabel("文件").setInputFiles(join(fixtureDirectory(), filename));
  const responsePromise = page.waitForResponse((response) => {
    if (response.request().method() !== "POST") return false;
    const url = new URL(response.url());
    if (documentID) {
      return url.pathname.endsWith(`/v1/knowledge/documents/${documentID}/versions`);
    }
    return url.pathname.endsWith("/v1/knowledge/documents");
  });
  await page.getByRole("button", { name: "提交解析" }).click();
  const response = await responsePromise;
  expect(response.ok(), `upload failed with HTTP ${response.status()}`).toBe(true);
  const result = validateUploadResult(await response.json());
  if (documentID) expect(result.document_id).toBe(documentID);
  expect(result.idempotent).toBe(false);
  await expect(documentRow(page, title)).toBeVisible({ timeout: 30_000 });
  return result;
}

async function selectDocument(page: Page, title: string): Promise<void> {
  const row = documentRow(page, title);
  await expect(row).toBeVisible();
  await row.click();
  await expect(page.getByRole("heading", { name: title })).toBeVisible();
}

async function waitForIngestion(
  page: Page,
  title: string,
  filename: string,
  expected: "indexed" | "failed",
  failureCode?: string
): Promise<void> {
  await selectDocument(page, title);
  const deadline = Date.now() + 600_000;
  while (Date.now() < deadline) {
    const version = page.locator("article.knowledge-version-row").filter({ hasText: filename }).first();
    const text = await version.textContent().catch(() => "");
    if (expected === "indexed" && text?.includes("已索引")) return;
    if (expected === "failed" && text?.includes("失败") && (!failureCode || text.includes(failureCode))) return;
    if (expected === "indexed" && text?.includes("失败")) {
      throw new Error(`${title} entered a terminal ingestion failure: ${text}`);
    }
    const refresh = page.getByRole("button", { name: "刷新知识库" });
    if (await refresh.isEnabled()) await refresh.click();
    await page.waitForTimeout(2_000);
  }
  throw new Error(`${title} did not reach ${expected} before the acceptance deadline`);
}

async function publish(page: Page, title: string, filename: string): Promise<void> {
  await selectDocument(page, title);
  const version = page.locator("article.knowledge-version-row").filter({ hasText: filename }).first();
  await version.getByRole("button", { name: "发布" }).click();
  await expect(version.getByText("当前发布")).toBeVisible({ timeout: 30_000 });
}

async function setGrant(page: Page, title: string, displayName: string, enabled: boolean): Promise<void> {
  await selectDocument(page, title);
  const member = page.locator("label.knowledge-member-row").filter({ hasText: displayName });
  await expect(member).toBeVisible();
  const checkbox = member.getByRole("checkbox");
  if ((await checkbox.isChecked()) !== enabled) {
    if (enabled) await checkbox.check();
    else await checkbox.uncheck();
  }
  await expect(checkbox).toBeChecked({ checked: enabled });
}

async function askFromKnowledge(page: Page, question: string): Promise<string> {
  await page.getByPlaceholder("输入一个必须由已授权文档回答的问题").fill(question);
  await page.getByRole("button", { name: "发送给智能助手" }).click();
  await expect(page.getByRole("heading", { name: "智能助手" })).toBeVisible();
  const run = page.locator("article.agent-run", { hasText: question });
  await expect(run).toContainText("已完成", { timeout: 360_000 });
  await expect(run).not.toContainText("执行失败");
  const testID = await run.getAttribute("data-testid");
  if (!testID?.startsWith("agent-run-")) throw new Error("completed Agent Run has no durable run ID");
  return testID.slice("agent-run-".length);
}

async function askFromAgent(page: Page, question: string): Promise<string> {
  await page.getByRole("button", { name: "智能助手" }).click();
  await expect(page.getByRole("heading", { name: "智能助手" })).toBeVisible();
  await page.getByRole("textbox", { name: "向 Agent 提问" }).fill(question);
  await page.getByRole("button", { name: "发送给 Agent" }).click();
  const run = page.locator("article.agent-run", { hasText: question });
  await expect(run).toContainText("已完成", { timeout: 360_000 });
  await expect(run).not.toContainText("执行失败");
  const testID = await run.getAttribute("data-testid");
  if (!testID?.startsWith("agent-run-")) throw new Error("completed Agent Run has no durable run ID");
  return testID.slice("agent-run-".length);
}

async function verifyNoTargetEvidence(page: Page, runID: string, manifest: FixtureManifest): Promise<void> {
  const run = page.getByTestId(`agent-run-${runID}`);
  const response = run.locator(".agent-response");
  for (const document of manifest.documents) {
    await expect(response).not.toContainText(document.marker);
    await expect(response.locator("section.citation-section")).not.toContainText(document.title);
  }
}

async function verifyLayoutAndScreenshot(page: Page, testInfo: TestInfo, name: string): Promise<void> {
  const desktop = await page.evaluate(() => ({
    viewport: window.innerWidth,
    document: document.documentElement.scrollWidth,
    localStorage: Object.entries(localStorage)
  }));
  expect(desktop.localStorage).toEqual([]);
  expect(desktop.document).toBeLessThanOrEqual(desktop.viewport);
  await page.screenshot({ path: testInfo.outputPath(`${name}-desktop.png`), fullPage: true });

  await page.setViewportSize({ width: 390, height: 844 });
  const mobile = await page.evaluate(() => ({
    viewport: window.innerWidth,
    document: document.documentElement.scrollWidth
  }));
  expect(mobile.document).toBeLessThanOrEqual(mobile.viewport);
  await page.screenshot({ path: testInfo.outputPath(`${name}-mobile.png`), fullPage: true });
}

const manifest = loadManifest();

test("bootstrap four real formats through Web and preserve acceptance state", async ({ page }, testInfo) => {
  test.skip(phase !== "bootstrap", `phase is ${phase}`);
  const browser = collectBrowserFailures(page);
  await signIn(page, "OPENIM_E2E_USERNAME", "OPENIM_E2E_PASSWORD");
  await openKnowledge(page);
  const memberDisplayName = required("OPENIM_E2E_MEMBER_DISPLAY_NAME");

  const uploaded: UploadedDocument[] = [];
  for (const document of manifest.documents) {
    const result = await upload(page, document.title, document.filename);
    await waitForIngestion(page, document.title, document.filename, "indexed");
    await publish(page, document.title, document.filename);
    await setGrant(page, document.title, memberDisplayName, true);
    uploaded.push({ ...document, ...result });
  }

  await selectDocument(page, uploaded[0].title);
  const bootstrapRunID = await askFromKnowledge(page, manifest.question);
  const citationRegion = page.getByTestId(`agent-run-${bootstrapRunID}`)
    .getByRole("region", { name: "引用来源" });
  const answer = page.getByTestId(`agent-run-${bootstrapRunID}`).locator(".agent-response");
  await expect(citationRegion).toBeVisible();
  for (const document of uploaded) {
    await expect(answer).toContainText(document.marker);
    await expect(citationRegion).toContainText(document.title);
  }

  await openKnowledge(page);
  const invalidTitle = `E2E RAG Invalid PDF ${manifest.batch_id}`;
  const invalid = await upload(page, invalidTitle, manifest.invalid_pdf);
  await waitForIngestion(page, invalidTitle, manifest.invalid_pdf, "failed", "PDF_STRUCTURE_INVALID");
  const invalidVersion = page.locator("article.knowledge-version-row").filter({ hasText: manifest.invalid_pdf });
  await expect(invalidVersion.getByRole("button", { name: "发布" })).toHaveCount(0);

  saveState({
    schema_version: 1,
    batch_id: manifest.batch_id,
    documents: uploaded,
    invalid_document: {
      title: invalidTitle,
      document_id: invalid.document_id,
      version_id: invalid.version_id
    },
    web: { bootstrap_run_id: bootstrapRunID }
  });
  await verifyLayoutAndScreenshot(page, testInfo, "bootstrap");
  expect(browser.failedResponses).toEqual([]);
  expect(browser.consoleErrors).toEqual([]);
});

test("denied member cannot see the Knowledge module or target evidence", async ({ page }, testInfo) => {
  test.skip(phase !== "denied", `phase is ${phase}`);
  const state = loadState(manifest);
  const browser = collectBrowserFailures(page);
  await signIn(page, "OPENIM_E2E_DENIED_USERNAME", "OPENIM_E2E_DENIED_PASSWORD");
  await expect(page.getByRole("button", { name: "知识库" })).toHaveCount(0);
  const runID = await askFromAgent(page, manifest.question);
  await verifyNoTargetEvidence(page, runID, manifest);
  state.web.denied_run_id = runID;
  saveState(state);
  await verifyLayoutAndScreenshot(page, testInfo, "denied");
  const unexpected = browser.failedResponses.filter((item) =>
    !(item.status === 403 && item.url.includes("/v1/knowledge/documents")));
  expect(unexpected).toEqual([]);
  expect(browser.consoleErrors).toEqual([]);
});

test("revocation is effective for the immediately following Web query", async ({ page }, testInfo) => {
  test.skip(phase !== "revoke", `phase is ${phase}`);
  const state = loadState(manifest);
  const browser = collectBrowserFailures(page);
  await signIn(page, "OPENIM_E2E_USERNAME", "OPENIM_E2E_PASSWORD");
  await openKnowledge(page);
  const memberDisplayName = required("OPENIM_E2E_MEMBER_DISPLAY_NAME");
  for (const document of state.documents) {
    await setGrant(page, document.title, memberDisplayName, false);
  }
  await selectDocument(page, state.documents[0].title);
  const runID = await askFromKnowledge(page, manifest.question);
  await verifyNoTargetEvidence(page, runID, manifest);
  state.web.revoked_run_id = runID;
  saveState(state);
  await verifyLayoutAndScreenshot(page, testInfo, "revoked");
  expect(browser.failedResponses).toEqual([]);
  expect(browser.consoleErrors).toEqual([]);
});

test("new immutable version retires the old citation identity", async ({ page }, testInfo) => {
  test.skip(phase !== "version", `phase is ${phase}`);
  const state = loadState(manifest);
  const browser = collectBrowserFailures(page);
  await signIn(page, "OPENIM_E2E_USERNAME", "OPENIM_E2E_PASSWORD");
  await openKnowledge(page);
  const memberDisplayName = required("OPENIM_E2E_MEMBER_DISPLAY_NAME");
  const markdown = state.documents.find((item) => item.format === "markdown");
  if (!markdown?.version_file || !markdown.version_marker) throw new Error("Markdown version fixture is missing");
  await setGrant(page, markdown.title, memberDisplayName, true);
  const next = await upload(page, markdown.title, markdown.version_file, markdown.document_id);
  await waitForIngestion(page, markdown.title, markdown.version_file, "indexed");
  await publish(page, markdown.title, markdown.version_file);
  markdown.next_version_id = next.version_id;
  markdown.next_version_number = next.version_number;

  await selectDocument(page, markdown.title);
  const runID = await askFromKnowledge(page, manifest.old_version_query);
  const response = page.getByTestId(`agent-run-${runID}`).locator(".agent-response");
  await expect(response).toContainText(markdown.version_marker);
  await expect(response).not.toContainText(markdown.marker);
  state.web.version_run_id = runID;
  saveState(state);
  await verifyLayoutAndScreenshot(page, testInfo, "version");
  expect(browser.failedResponses).toEqual([]);
  expect(browser.consoleErrors).toEqual([]);
});

test("cleanup phase unpublishes every acceptance document", async ({ page }) => {
  test.skip(phase !== "cleanup", `phase is ${phase}`);
  const state = loadState(manifest);
  const browser = collectBrowserFailures(page);
  await signIn(page, "OPENIM_E2E_USERNAME", "OPENIM_E2E_PASSWORD");
  await openKnowledge(page);
  for (const document of state.documents) {
    await selectDocument(page, document.title);
    const granted = page.locator("label.knowledge-member-row input[type=checkbox]:checked");
    for (let index = (await granted.count()) - 1; index >= 0; index -= 1) {
      await granted.nth(index).uncheck();
    }
    await expect(page.locator("label.knowledge-member-row input[type=checkbox]:checked")).toHaveCount(0);
    const button = page.getByRole("button", { name: "撤销发布" });
    if (await button.isVisible()) {
      await button.click();
      await expect(button).toHaveCount(0);
    }
  }
  expect(browser.failedResponses).toEqual([]);
  expect(browser.consoleErrors).toEqual([]);
});
