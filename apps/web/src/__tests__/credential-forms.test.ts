import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

import { describe, expect, it } from "vitest";

// Stage 18 (DAST finding F-S18-13): a form that carries a password, code or token must never fall back to GET when
// JavaScript is unavailable (the browser would put the credential into the URL, history and server logs).
describe("credential forms", () => {
  it("every form in the auth and account components declares method=post", () => {
    const offenders: string[] = [];
    for (const dir of ["src/components/auth", "src/components/account"]) {
      for (const f of readdirSync(join(process.cwd(), dir)).filter((n) => n.endsWith(".tsx"))) {
        const src = readFileSync(join(process.cwd(), dir, f), "utf8");
        for (const m of src.matchAll(/<form\b[^>]*>/g)) {
          if (!/method="post"/.test(m[0])) offenders.push(`${dir}/${f}: ${m[0].slice(0, 80)}`);
        }
      }
    }
    expect(offenders).toEqual([]);
  });
});
