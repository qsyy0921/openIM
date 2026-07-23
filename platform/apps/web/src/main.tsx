import { createRoot } from "react-dom/client";

import { createUserManager } from "./auth";
import { loadConfig } from "./config";
import "./styles.css";

const root = createRoot(document.getElementById("root")!);

async function start() {
  const config = loadConfig(import.meta.env);
  const userManager = createUserManager(config);
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
