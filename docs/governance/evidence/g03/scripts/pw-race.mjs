// Playwright's own APIRequestContext (the client behind page.request) against the real built auth app on :3101.
import { request } from "@playwright/test";
const base = "http://127.0.0.1:3101";
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let n = 0;
const create = (ctx) => ctx.post(`${base}/api/v1/__mock/users`, { data: { email: `g03-${Date.now()}-${n++}@example.test`, display_name: "G03 Probe", mfa_enabled: false, email_verified: true, phone_verified: false } })
  .then((r) => ({ ok: true, status: r.status() }), (e) => ({ ok: false, err: String(e.message).split("\n")[0] }));
const delays = process.argv.slice(2).map(Number), tally = {};
for (const d of delays) for (let t = 0; t < 10; t++) {
  const ctx = await request.newContext();
  const a = await create(ctx); await sleep(d); const b = await create(ctx); await ctx.dispose();
  const k = `${d}ms first=${a.ok ? a.status : a.err} second=${b.ok ? b.status : b.err}`; tally[k] = (tally[k] ?? 0) + 1;
}
for (const [k, v] of Object.entries(tally)) console.log(v, k);
