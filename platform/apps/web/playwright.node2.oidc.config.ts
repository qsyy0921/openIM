import { defineConfig } from "@playwright/test";

const baseURL = process.env.OPENIM_E2E_BASE_URL?.trim();
if (!baseURL) throw new Error("required E2E environment variable OPENIM_E2E_BASE_URL is missing");

export default defineConfig({
  testDir: "./e2e",
  testMatch: "node2-oidc-continuity.spec.ts",
  workers: 1,
  timeout: 600_000,
  outputDir: "test-results/node2-oidc",
  reporter: "line",
  use: {
    baseURL,
    browserName: "chromium",
    channel: "chrome",
    headless: true,
    ignoreHTTPSErrors: false,
    screenshot: "off",
    trace: "off",
    video: "off"
  }
});
