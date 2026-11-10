// @vitest-environment node
//
// Bloem's own web areas show text from Bloem's catalog (docs/bloem/web-messages.md).
// Silo's eslint.config.js checks only the areas Silo has converted. This test
// runs the same two rules over Bloem's areas, so new Bloem text cannot slip
// back to English and Silo's file stays as Silo wrote it.
import path from "node:path";
import { fileURLToPath } from "node:url";

import { ESLint } from "eslint";
import formatjs from "eslint-plugin-formatjs";
import tseslint from "typescript-eslint";
import { expect, it } from "vitest";

const WEB = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

// Bloem's own areas of the web app.
const BLOEM_AREAS = [
  "src/bloem/**/*.{ts,tsx}",
  "src/components/{engagement,livetv,seasonal}/**/*.{ts,tsx}",
  "src/pages/{admin-platform,admin-organization,livetv}/**/*.{ts,tsx}",
  "src/pages/{AdminLiveTV,LiveTV,LiveTVWatch,BloemHome,OrganizationAdminAccessGroups}.tsx",
];

// The rules web/eslint.config.js applies to its I18N_CONVERTED areas.
const RULES = {
  "formatjs/no-literal-string-in-jsx": [
    "error",
    {
      props: {
        include: [
          ["*", "{label,title,subtitle,description,help,placeholder,currentNote,alt,tooltip}"],
        ],
      },
    },
  ],
  "no-restricted-syntax": [
    "error",
    {
      selector:
        "CallExpression:matches([callee.name='toast'], [callee.object.name='toast'])[arguments.0.type=/^(Literal|TemplateLiteral)$/]",
      message: "Toast text comes from the message catalog: toast.success(t(...)).",
    },
  ],
};

it("finds no literal text in Bloem's areas", { timeout: 180_000 }, async () => {
  const eslint = new ESLint({
    cwd: WEB,
    overrideConfigFile: true,
    overrideConfig: [
      {
        files: ["**/*.{ts,tsx}"],
        // Test fixtures render literal text on purpose; the locales are generated.
        ignores: ["**/*.test.*", "**/*.test-support.*", "src/bloem/locales/**"],
        languageOptions: { parser: tseslint.parser },
        linterOptions: { reportUnusedDisableDirectives: "off" },
        plugins: { formatjs },
        rules: RULES,
      },
    ],
  });
  // A pattern that matches no file (a moved page) throws.
  const results = await eslint.lintFiles(BLOEM_AREAS);
  const findings = results.flatMap((result) =>
    result.messages
      .filter((message) => message.fatal || message.ruleId in RULES)
      .map(
        (message) =>
          `${path.relative(WEB, result.filePath)}:${message.line}:${message.column} ${message.ruleId ?? "parse"}: ${message.message}`,
      ),
  );
  expect(findings, "Bloem text must come from Bloem's catalog: bt()/bloemT()").toEqual([]);
});
