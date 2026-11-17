/**
 * Bloem's Vitest settings, applied from the brand plugin's config hook so that
 * vite.config.ts and src/test-setup.ts stay as Silo wrote them.
 */
import type { UserConfig } from "vite";
import type { InlineConfig } from "vitest/node";
import { configDefaults } from "vitest/config";

// These need globals or Node APIs that VM contexts lack, so they join Silo's
// threads project (THREADS_TESTS in vite.config.ts).
export const BLOEM_THREADS_TESTS = [
  // Imports vite.config.ts, whose Tailwind plugin registers a Node module hook at load.
  "bloem-brand-plugin.test.ts",
  // Redefines window.location, which VM contexts do not allow.
  "src/api/bloemClient.test.ts",
  // Runs ESLint, whose module loading fails inside a VM context.
  "scripts/bloem-i18n-coverage.test.mjs",
];

export const BLOEM_TEST_SETUP = "./src/bloem/test-setup.ts";

type ProjectEntry = { test?: InlineConfig };

/** Adds Bloem's test settings to the config Vite loaded from vite.config.ts. */
export function applyBloemTestConfig(config: UserConfig): void {
  const test = (config as UserConfig & { test?: InlineConfig }).test;
  if (!test) return;
  const setupFiles = test.setupFiles ?? [];
  test.setupFiles = [
    ...(Array.isArray(setupFiles) ? setupFiles : [setupFiles]),
    BLOEM_TEST_SETUP,
  ];
  // CI shards run Bloem's and Silo's suites together, four workers each, and
  // Vitest's 5 s default cuts off Silo's heavier page tests (AdminCollections
  // List peeks takes about 4 s on its own).
  test.testTimeout ??= 15_000;
  // Worker recycling reads the root config, even with projects. The host-RAM
  // default can exceed Node's heap limit on large hosts.
  test.vmMemoryLimit ??= "512MiB";
  // WEBTEST_EXCLUDE (comma-separated, from the Makefile's known failures)
  // reaches every project through the shared exclude; the command line's
  // --exclude does not apply to projects.
  test.exclude = [
    ...(test.exclude ?? configDefaults.exclude),
    ...(process.env.WEBTEST_EXCLUDE ?? "").split(",").filter(Boolean),
  ];
  for (const project of (test.projects ?? []) as ProjectEntry[]) {
    const include = project.test?.include;
    if (!include) continue;
    if (project.test?.pool === "threads") include.push(...BLOEM_THREADS_TESTS);
    if (project.test?.pool === "vmThreads")
      include.push(...BLOEM_THREADS_TESTS.map((file) => `!${file}`));
  }
}
