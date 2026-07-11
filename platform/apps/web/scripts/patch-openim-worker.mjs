import { readFile, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const files = ["worker.js", "worker-legacy.js"];
const unsafe = "JSON.parse(e).map";
const normalized = "(JSON.parse(e)??[]).map";
const expected = 5;

for (const file of files) {
  const path = resolve(root, "node_modules/@openim/wasm-client-sdk/lib", file);
  const source = await readFile(path, "utf8");
  const unsafeCount = source.split(unsafe).length - 1;
  const normalizedCount = source.split(normalized).length - 1;
  if (unsafeCount === 0 && normalizedCount === expected) continue;
  if (unsafeCount !== expected || normalizedCount !== 0) {
    throw new Error(`${file} compatibility signature changed: unsafe=${unsafeCount}, normalized=${normalizedCount}`);
  }
  await writeFile(path, source.replaceAll(unsafe, normalized), "utf8");
}

console.log("OpenIM empty batch payloads normalized");
