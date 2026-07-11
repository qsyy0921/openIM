import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";

function required(env: Record<string, string>, key: string): string {
  const value = env[key]?.trim();
  if (!value) {
    throw new Error(`required environment variable ${key} is missing`);
  }
  return value;
}

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "");
  const platformTarget = required(env, "VITE_PLATFORM_API_PROXY_TARGET");
  const openIMTarget = required(env, "VITE_OPENIM_API_PROXY_TARGET");

  return {
    plugins: [react()],
    server: {
      strictPort: true,
      proxy: {
        "/platform-api": {
          target: platformTarget,
          changeOrigin: true,
          rewrite: (path) => path.replace(/^\/platform-api/, ""),
        },
        "/openim-api": {
          target: openIMTarget,
          changeOrigin: true,
          rewrite: (path) => path.replace(/^\/openim-api/, ""),
        }
      }
    },
    build: {
      sourcemap: true
    }
  };
});
