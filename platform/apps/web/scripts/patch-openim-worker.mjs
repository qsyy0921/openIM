import { readFile, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const files = ["worker.js", "worker-legacy.js"];
const unsafe = "JSON.parse(e).map";
const normalized = "(JSON.parse(e)??[]).map";
const expected = 5;
const historyUnsafe = 'er.registerMethod("getMessageList",async function(e,t,r,n,i,o=!1){if(r<=0)return Vt(e,t,o);try{const s=function(e,t,r,n,i,o,s){return e.exec(';
const historyNormalized = 'er.registerMethod("getMessageList",async function(e,t,r,n,i,o=!1){if(r<=0)return Vt(e,t,o);try{const s=function(e,t,r,n,i,o,s){return b(e,t),e.exec(';
const historyIDsUnsafe = 'er.registerMethod("getMessagesByClientMsgIDs",async function(e,t){try{const r=function(e,t,r){return e.exec(';
const historyIDsNormalized = 'er.registerMethod("getMessagesByClientMsgIDs",async function(e,t){try{const r=function(e,t,r){return b(e,t),e.exec(';

for (const file of files) {
  const path = resolve(root, "node_modules/@openim/wasm-client-sdk/lib", file);
  let source = await readFile(path, "utf8");
  const unsafeCount = source.split(unsafe).length - 1;
  const normalizedCount = source.split(normalized).length - 1;
  if (unsafeCount === expected && normalizedCount === 0) {
    source = source.replaceAll(unsafe, normalized);
  } else if (unsafeCount !== 0 || normalizedCount !== expected) {
    throw new Error(`${file} compatibility signature changed: unsafe=${unsafeCount}, normalized=${normalizedCount}`);
  }

  const historyUnsafeCount = source.split(historyUnsafe).length - 1;
  const historyNormalizedCount = source.split(historyNormalized).length - 1;
  if (historyUnsafeCount === 1 && historyNormalizedCount === 0) {
    source = source.replace(historyUnsafe, historyNormalized);
  } else if (historyUnsafeCount !== 0 || historyNormalizedCount !== 1) {
    throw new Error(`${file} history-table signature changed: unsafe=${historyUnsafeCount}, normalized=${historyNormalizedCount}`);
  }

  const historyIDsUnsafeCount = source.split(historyIDsUnsafe).length - 1;
  const historyIDsNormalizedCount = source.split(historyIDsNormalized).length - 1;
  if (historyIDsUnsafeCount === 1 && historyIDsNormalizedCount === 0) {
    source = source.replace(historyIDsUnsafe, historyIDsNormalized);
  } else if (historyIDsUnsafeCount !== 0 || historyIDsNormalizedCount !== 1) {
    throw new Error(`${file} history-id-table signature changed: unsafe=${historyIDsUnsafeCount}, normalized=${historyIDsNormalizedCount}`);
  }
  await writeFile(path, source, "utf8");
}

console.log("OpenIM nullable batches and dynamic history tables normalized");
