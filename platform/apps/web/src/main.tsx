import { createRoot } from "react-dom/client";

import { createUserManager } from "./auth";
import { loadConfig } from "./config";
import { SignedOutApp } from "./SignedOutApp";
import "./styles.css";

const root = createRoot(document.getElementById("root")!);

async function start() {
  const config = loadConfig(import.meta.env);
  const userManager = createUserManager(config);
  const callbackPath = new URL(config.oidcRedirectURI).pathname;
  const callback = window.location.pathname === callbackPath;
  const identity = callback ? null : await userManager.getUser();

  if (!callback && (!identity || identity.expired)) {
    root.render(<SignedOutApp userManager={userManager} />);
    return;
  }

  const { App } = await import("./App");
  root.render(<App config={config} userManager={userManager} />);
}

void start().catch((error: unknown) => {
  const message = error instanceof Error ? error.message : "invalid web configuration";
  root.render(
    <main className="fatal-shell">
      <h1>配置错误</h1>
      <pre>{message}</pre>
    </main>
  );
});
