import { defineConfig, globalIgnores } from "eslint/config";
import nextVitals from "eslint-config-next/core-web-vitals";
import nextTs from "eslint-config-next/typescript";

// Money must never pass through floating point (CLAUDE.md, docs/MONEY.md §10).
const moneyNumberMessage =
  "Never convert amount_minor to a JS number. Keep it a string/BigInt and format with src/lib/money.ts.";

// Auth state is HttpOnly cookies only (FRONTEND.md §8): nothing sensitive in browser storage.
const storageMessage = "Do not use browser storage: tokens, codes and personal data must never be persisted client-side.";

const eslintConfig = defineConfig([
  ...nextVitals,
  ...nextTs,
  {
    rules: {
      "no-restricted-syntax": [
        "error",
        { selector: "CallExpression[callee.name=/^(Number|parseFloat|parseInt)$/] MemberExpression[property.name='amount_minor']", message: moneyNumberMessage },
        { selector: "CallExpression[callee.object.name='Number'][callee.property.name=/^(parseFloat|parseInt)$/] MemberExpression[property.name='amount_minor']", message: moneyNumberMessage },
        { selector: "UnaryExpression[operator='+'] > MemberExpression[property.name='amount_minor']", message: moneyNumberMessage },
        { selector: "MemberExpression[property.name=/^(localStorage|sessionStorage|indexedDB)$/]", message: storageMessage },
      ],
      "no-restricted-globals": [
        "error",
        { name: "localStorage", message: storageMessage },
        { name: "sessionStorage", message: storageMessage },
        { name: "indexedDB", message: storageMessage },
      ],
    },
  },
  // G-03 (docs/governance/evidence/g03-investigation-report.md): e2e specs must use the `test` of e2e/helpers.ts, whose
  // fixtures give test-setup API requests a fresh connection. Importing `test` from @playwright/test bypasses them.
  {
    files: ["e2e/**/*.spec.ts"],
    rules: {
      "no-restricted-imports": [
        "error",
        {
          paths: [
            {
              name: "@playwright/test",
              importNames: ["test"],
              message: "Import `test` from ./helpers (fresh connections for test-setup API requests, G-03); keep expect and types from @playwright/test.",
            },
          ],
        },
      ],
    },
  },
  // Override default ignores of eslint-config-next.
  globalIgnores([
    // Default ignores of eslint-config-next:
    ".next/**",
    "out/**",
    "build/**",
    "next-env.d.ts",
    // Test artefacts
    "playwright-report/**",
    "test-results/**",
  ]),
]);

export default eslintConfig;
