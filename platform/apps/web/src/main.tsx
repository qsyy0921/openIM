import { createRoot } from "react-dom/client";

import { App } from "./App";
import { createUserManager } from "./auth";
import { loadConfig } from "./config";
import "./styles.css";

const root = createRoot(document.getElementById("root")!);

try {
  const config = loadConfig(import.meta.env);
  root.render(<App config={config} userManager={createUserManager(config)} />);
} catch (error) {
  const message = error instanceof Error ? error.message : "invalid web configuration";
  root.render(
    <main className="fatal-shell">
      <h1>配置错误</h1>
      <pre>{message}</pre>
    </main>
  );
}
