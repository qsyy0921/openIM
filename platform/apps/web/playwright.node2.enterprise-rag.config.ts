import { defineConfig } from "@playwright/test";

const baseURL = process.env.OPENIM_E2E_BASE_URL?.trim();
if (!baseURL) throw new Error("required E2E environment variable OPENIM_E2E_BASE_URL is missing");

export default defineConfig({
  testDir: "./e2e",
  testMatch: "node2-enterprise-rag.spec.ts",
  workers: 1,
  timeout: 900_000,
  outputDir: process.env.OPENIM_E2E_OUTPUT_DIR?.trim() || "test-results/node2-enterprise-rag",
  reporter: "line",
  use: {
    baseURL,
    browserName: "chromium",
    channel: "chrome",
    headless: true,
    ignoreHTTPSErrors: false,
    viewport: { width: 1440, height: 900 },
    screenshot: "off",
    trace: "retain-on-failure",
    video: "off"
  }
});
