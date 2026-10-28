// @vitest-environment node
import { ESLint } from "eslint";
import { describe, expect, it } from "vitest";

// G-03: the lint guard that keeps e2e specs on the `test` of e2e/helpers.ts (fresh connections for test-setup API
// requests). Linted with the repository's own eslint.config.mjs, as `npm run lint` does.

const RULE = "no-restricted-imports";
const eslint = new ESLint({ cwd: process.cwd() });

async function restricted(filePath: string, code: string) {
  const [result] = await eslint.lintText(code, { filePath });
  return (result?.messages ?? []).filter((m) => m.ruleId === RULE).map((m) => m.message);
}

describe("e2e test-import guard", () => {
  it("rejects `test` from @playwright/test in an e2e spec, also when aliased", async () => {
    expect(await restricted("e2e/guard-probe.spec.ts", 'import { expect, test } from "@playwright/test";\ntest("x", async () => expect(1).toBe(1));\n'))
      .toEqual([expect.stringContaining("Import `test` from ./helpers")]);
    expect(await restricted("e2e/guard-probe.spec.ts", 'import { test as t } from "@playwright/test";\nt("x", async () => {});\n')).toHaveLength(1);
  }, 60_000);

  it("accepts the helpers `test` with expect and types from @playwright/test", async () => {
    const code = 'import { expect, type Page } from "@playwright/test";\n\nimport { test } from "./helpers";\n\ntest("x", async ({ page }: { page: Page }) => expect(page).toBeTruthy());\n';
    expect(await restricted("e2e/guard-probe.spec.ts", code)).toEqual([]);
  }, 60_000);

  it("leaves the helpers module, the Playwright config and the unit tests alone", async () => {
    expect(await restricted("e2e/helpers.ts", 'import { test as base } from "@playwright/test";\nexport const test = base;\n')).toEqual([]);
    expect(await restricted("playwright.config.ts", 'import { defineConfig, devices } from "@playwright/test";\nexport default defineConfig({ use: { ...devices["Pixel 7"] } });\n')).toEqual([]);
    expect(await restricted("src/__tests__/guard-probe.test.ts", 'import { expect, test } from "vitest";\ntest("x", () => expect(1).toBe(1));\n')).toEqual([]);
  }, 60_000);
});
