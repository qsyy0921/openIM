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
const searchKeywordStart = 'er.registerMethod("searchMessageByKeyword"';
const searchKeywordEnd = ',er.registerMethod("searchMessageByContentType"';
const searchKeywordLegacySignatures = [
  'async function(e,t,r,n,i,o,s,a,u)',
  'JSON.parse(t),JSON.parse(r),JSON.parse(n),i,o,s,a,u',
  'i.forEach((e,t)=>',
  'n.length&&(f+=`AND send_id IN'
];
const searchKeywordNormalized = `er.registerMethod("searchMessageByKeyword",async function(conversationID,contentTypesJSON,keywordsJSON,keywordMatchType,startTime,endTime,offset,count){try{const db=await Yt(),contentTypes=JSON.parse(contentTypesJSON),keywords=JSON.parse(keywordsJSON),upperBound=endTime||(new Date).getTime(),operator=0===keywordMatchType?"or ":"and ";let keywordCondition="";keywords.forEach((keyword,index)=>{const escaped=String(keyword).replaceAll("'","''");0===index&&(keywordCondition+="And (");index+1>=keywords.length?keywordCondition+="content like '%"+escaped+"%') ":keywordCondition+="content like '%"+escaped+"%' "+operator});const result=db.exec(\`
    SELECT * FROM 'chat_logs_\${conversationID}'
          WHERE send_time between \${startTime} and \${upperBound}
          AND status <=3
          And content_type IN (\${contentTypes.map(value=>Number(value)).join(",")})
          \${keywordCondition}
    ORDER BY send_time DESC LIMIT \${count} OFFSET \${offset};
    \`);return x(P(result[0],"CamelCase",["isRead","isReact","isExternalExtensions"]))}catch(error){return console.error(error),x(void 0,h,JSON.stringify(error))}})`;

function normalizeSearchKeywordABI(source, file) {
  const start = source.indexOf(searchKeywordStart);
  const end = source.indexOf(searchKeywordEnd, start);
  if (start < 0 || end < 0 || source.indexOf(searchKeywordStart, start + 1) >= 0) {
    throw new Error(`${file} search keyword registration boundary changed`);
  }
  const current = source.slice(start, end);
  if (current === searchKeywordNormalized) return source;
  if (!searchKeywordLegacySignatures.every((signature) => current.includes(signature))) {
    throw new Error(`${file} search keyword ABI signature changed`);
  }
  return `${source.slice(0, start)}${searchKeywordNormalized}${source.slice(end)}`;
}

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
  source = normalizeSearchKeywordABI(source, file);
  await writeFile(path, source, "utf8");
}

console.log("OpenIM nullable batches, dynamic history tables, and message-search ABI normalized");
