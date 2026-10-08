import { defineConfig, globalIgnores } from "eslint/config";
import nextVitals from "eslint-config-next/core-web-vitals";
import nextTs from "eslint-config-next/typescript";

// Money must never pass through floating point (CLAUDE.md, docs/MONEY.md §10).
const moneyNumberMessage =
  "Never convert amount_minor to a JS number. Keep it a string/BigInt and format with src/lib/money.ts.";

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
