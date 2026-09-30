// Keep Bloem's measured allowance separate while using upstream budget rules.
import { readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { brotliCompressSync, constants } from "node:zlib";
import { budgetFailures, crossOriginRenderBlocking, eagerFiles, vendorChunkFailures } from "./check-bundle-budget.mjs";

const root = new URL("../", import.meta.url);
const manifest = JSON.parse(readFileSync(new URL(".bundle-manifest.json", root), "utf8"));
const { js, css } = eagerFiles(manifest);
const eagerBrotliBytes = [...js, ...css].reduce((total, file) => total + brotliCompressSync(
  readFileSync(new URL("dist/" + file, root)),
  { params: { [constants.BROTLI_PARAM_QUALITY]: 11 } },
).byteLength, 0);
const values = {
  eagerBrotliBytes,
  crossOriginRenderBlocking: crossOriginRenderBlocking(readFileSync(new URL("dist/index.html", root), "utf8")).length,
};
const budgetPath = new URL("bloem-perf-budget.json", root);
const args = process.argv.slice(2);
if (args.some((arg) => arg !== "--update")) throw new Error("Expected only --update");
const failures = vendorChunkFailures(manifest);
if (args.includes("--update")) {
  writeFileSync(budgetPath, JSON.stringify(values, null, 2) + "\n");
  console.log("Updated " + fileURLToPath(budgetPath));
} else failures.push(...budgetFailures(values, JSON.parse(readFileSync(budgetPath, "utf8"))));
console.log(JSON.stringify(values));
for (const failure of failures) console.error(failure);
if (failures.length) process.exitCode = 1;
