import { copyFile, mkdir } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const source = resolve(root, "node_modules/@openim/wasm-client-sdk/assets");
const target = resolve(root, "public");
const assets = ["openIM.wasm", "sql-wasm.wasm", "wasm_exec.js"];

await mkdir(target, { recursive: true });
await Promise.all(assets.map((asset) => copyFile(resolve(source, asset), resolve(target, asset))));
console.log("OpenIM WASM assets copied");
