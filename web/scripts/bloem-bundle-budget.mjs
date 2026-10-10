// Keep Bloem's measured allowance separate while using upstream budget rules.
import { readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { brotliCompressSync, constants } from "node:zlib";
import { crossOriginRenderBlocking, eagerFiles, vendorChunkFailures } from "./check-bundle-budget.mjs";

// Silo now reports launch bytes without a baseline gate. Bloem retains its
// existing measured allowance here, using Silo's manifest and resource checks.
function budgetFailures(actual, budget) {
  const failures = [];
  const bytes = actual.eagerBrotliBytes;
  const byteBudget = budget.eagerBrotliBytes;
  const update = "node scripts/bloem-bundle-budget.mjs --update";
  if (bytes > byteBudget + 1024) {
    failures.push(
      `eager launch bundle is ${bytes} B brotli, ${bytes - byteBudget} B over the ${byteBudget} B budget. ` +
        `Move the new code behind a lazy import, or raise the budget with ${update} and explain why.`,
    );
  } else if (bytes < byteBudget - 1024) {
    failures.push(
      `eager launch bundle is ${bytes} B brotli, ${byteBudget - bytes} B under the ${byteBudget} B budget. ` +
        `Run ${update} and commit the smaller measured allowance.`,
    );
  }
  const count = actual.crossOriginRenderBlocking;
  const countBudget = budget.crossOriginRenderBlocking;
  if (count > countBudget) {
    failures.push(
      `index.html has ${count} cross-origin render-blocking resources, budget ${countBudget}. ` +
        "Self-host the resource or load it without blocking first paint.",
    );
  } else if (count < countBudget) {
    failures.push(
      `index.html has ${count} cross-origin render-blocking resources, budget ${countBudget}. ` +
        `Run ${update} and commit the smaller resource allowance.`,
    );
  }
  return failures;
}

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
